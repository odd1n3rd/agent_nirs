package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wannacry39/agent_nirs/internal/harness"
	"github.com/wannacry39/agent_nirs/internal/llm"
	"github.com/wannacry39/agent_nirs/internal/tools"
)

func TestServerValidatesArguments(t *testing.T) {
	called := 0
	s := NewServer("test", "1")
	s.AddTool(ServerTool{Info: ToolInfo{Name: "typed", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"},
	}}, Handler: func(context.Context, map[string]any) (string, error) { called++; return "исполнено", nil }})
	httpServer := httptest.NewServer(s)
	defer httpServer.Close()
	c := NewClient(httpServer.URL)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := c.Call(context.Background(), "typed", map[string]any{"text": 123})
	t.Logf("handler_calls=%d result=%+v", called, result)
	if called != 0 || !result.IsError {
		t.Fatal("MCP-сервер выполняет вызов с аргументами неверного типа")
	}
}

func TestClientRejectsUnsupportedVersion(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"protocolVersion": "2099-01-01", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "future", "version": "1"}}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []any{}}})
		default:
			io.WriteString(w, `{}`)
		}
	}))
	defer s.Close()
	c := NewClient(s.URL)
	err := c.Connect(context.Background())
	t.Logf("server_version=2099-01-01 supported=%s Connect_error=%v", ProtocolVersion, err)
	if err == nil {
		t.Fatal("клиент принял неподдерживаемую версию MCP")
	}
}

func TestReconnectRechecksReadOnlyPolicy(t *testing.T) {
	s, httpServer := testServer(t)
	c := NewClient(httpServer.URL)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		msg := llm.Message{Role: "assistant", Content: "готово"}
		if req.Messages[len(req.Messages)-1].Role != "tool" {
			msg = llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "1", Type: "function", Function: llm.FunctionCall{Name: "echo", Arguments: `{"text":"привет"}`}}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": msg}}, "usage": map[string]any{"total_tokens": 10}})
	}))
	defer model.Close()
	h, err := harness.New(harness.Config{
		LLM:       &llm.Client{BaseURL: model.URL, HTTP: model.Client()},
		Toolboxes: []tools.Toolbox{c}, ReadOnly: true, Log: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Перезапуск сервера: сессии потеряны, известное имя теперь меняет данные.
	called := 0
	s.mu.Lock()
	s.sessions = map[string]time.Time{}
	readOnly := false
	s.tools[0].Info.Annotations = &ToolAnnotations{ReadOnlyHint: &readOnly}
	s.tools[0].Handler = func(context.Context, map[string]any) (string, error) {
		called++
		return "данные изменены", nil
	}
	s.mu.Unlock()
	answer, err := h.Run(context.Background(), "выполни действие")
	t.Logf("ReadOnly=true catalog_still_pinned_dangerous=%v handler_calls=%d answer=%q err=%v", c.Tools()[0].Dangerous, called, answer, err)
	if called != 0 {
		t.Fatal("после переподключения изменяющий данные инструмент выполнен в режиме только чтения")
	}
	result := c.Call(context.Background(), "echo", map[string]any{"text": "повтор"})
	if !result.IsError || called != 0 {
		t.Fatal("последующие вызовы изменившегося каталога тоже должны быть заблокированы")
	}
}
