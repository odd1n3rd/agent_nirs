package harness

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wannacry39/agent_nirs/internal/llm"
	"github.com/wannacry39/agent_nirs/internal/tools"
)

func TestCompressionRespectsTimeBudget(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-time.After(time.Second):
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"сводка"}}],"usage":{"total_tokens":7}}`)
		}
	}))
	defer server.Close()
	h, err := New(Config{
		LLM: &llm.Client{BaseURL: server.URL, HTTP: server.Client()}, Log: io.Discard,
		Limits: Limits{MaxDuration: 50 * time.Millisecond}, Context: ContextPolicy{MaxHistoryMessages: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.context.AddUser("старый запрос")
	h.context.AddAssistant(llm.Message{Role: "assistant", Content: "старый ответ"})
	answer, err := h.Run(context.Background(), "новый запрос")
	if err != nil || !strings.Contains(answer, "лимит времени") || len(h.context.History) != 3 {
		t.Fatalf("ответ %q, ошибка %v, сообщений истории %d", answer, err, len(h.context.History))
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("запрос сжатия должен отменяться при исчерпании времени")
	}
}

func TestLastStepRejectsToolCallsInsteadOfAnswer(t *testing.T) {
	box, calls, _ := testTools()
	f := &fakeLLM{reply: func(r request) llm.Message {
		// Модель игнорирует запрет tool_choice: none.
		return llm.Message{ToolCalls: []llm.ToolCall{
			{ID: "a", Type: "function", Function: llm.FunctionCall{Name: "get_weather", Arguments: "{}"}},
			{ID: "b", Type: "function", Function: llm.FunctionCall{Name: "get_weather", Arguments: "{}"}},
			{ID: "c", Type: "function", Function: llm.FunctionCall{Name: "get_weather", Arguments: "{}"}},
		}}
	}}
	h := newHarness(t, f, Config{Toolboxes: []tools.Toolbox{box}, Limits: Limits{MaxSteps: 3}})
	answer, err := h.Run(context.Background(), "погода")
	t.Logf("tools=%d model_requests=%d answer=%q err=%v", *calls, len(f.requests), answer, err)
	if err != nil || !strings.Contains(answer, "не дала финальный ответ") || *calls != 2 || len(f.requests) != 2 {
		t.Fatal("нарушение запрета на инструменты должно остановить цикл без дополнительных вызовов")
	}
}

func TestHarnessRejectsWrongArgumentType(t *testing.T) {
	calls := 0
	box := tools.NewLocalToolbox()
	box.Register(tools.Tool{Name: "typed", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"},
	}}, func(context.Context, map[string]any) (string, error) { calls++; return "исполнено", nil })
	f := &fakeLLM{reply: func(r request) llm.Message {
		if lastRole(r) == "tool" {
			return llm.Message{Content: r.Messages[len(r.Messages)-1].Content}
		}
		return toolCall("a", "typed", `{"text":123}`)
	}}
	h := newHarness(t, f, Config{Toolboxes: []tools.Toolbox{box}})
	answer, err := h.Run(context.Background(), "вызови инструмент")
	t.Logf("handler_calls=%d answer=%q err=%v", calls, answer, err)
	if err != nil || calls != 0 || !strings.Contains(answer, "ожидается string") {
		t.Fatal("аргумент не соответствует схеме, но обработчик выполнен")
	}
}

func TestPartialAnswerIsNotComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Незаконченный ответ, потому что"},"finish_reason":"length"}],"usage":{"total_tokens":7}}`)
	}))
	defer server.Close()
	h, err := New(Config{LLM: &llm.Client{BaseURL: server.URL, HTTP: server.Client()}, Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := h.Run(context.Background(), "объясни")
	t.Logf("answer=%q err=%v", answer, err)
	if err != nil || !strings.Contains(answer, "оборвал") || !strings.Contains(answer, "Незаконченный ответ") {
		t.Fatal("непустой обрезанный ответ выдан как завершённый")
	}
}

func TestEmptySummaryDoesNotDiscardHistory(t *testing.T) {
	f := &fakeLLM{reply: func(r request) llm.Message {
		if strings.Contains(r.Messages[0].Content, "Сожми") {
			return llm.Message{}
		}
		return llm.Message{Content: "ответ"}
	}}
	h := newHarness(t, f, Config{Context: ContextPolicy{MaxHistoryMessages: 2}})
	h.context.AddUser("ВАЖНЫЙ-СТАРЫЙ-ФАКТ")
	h.context.AddAssistant(llm.Message{Role: "assistant", Content: "старый ответ"})
	answer, err := h.Run(context.Background(), "новый запрос")
	retained := strings.Contains(h.context.Summary, "ВАЖНЫЙ-СТАРЫЙ-ФАКТ")
	for _, m := range h.context.History {
		retained = retained || strings.Contains(m.Content, "ВАЖНЫЙ-СТАРЫЙ-ФАКТ")
	}
	t.Logf("summary=%q old_fact_retained=%v answer=%q err=%v", h.context.Summary, retained, answer, err)
	if !retained || err == nil {
		t.Fatal("пустая сводка привела к удалению старой истории")
	}
}
