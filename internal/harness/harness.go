// Package harness — харнесс и цикл агента.
//
// Харнесс — обвязка кода вокруг LLM, которая делает из модели агента:
// крутит цикл агента, исполняет вызовы инструментов, хранит историю и память
// и управляет контекстом. Вызовы локальных инструментов харнесс выполняет
// сам, а MCP-инструменты вызывает через MCP-клиент; историю передаёт модели
// в каждом запросе, потому что у самой модели нет памяти между вызовами;
// из памяти передаёт в контекст релевантные данные; записывает в контекст
// выбранные skills; контекст обрезает, сжимает или дополняет. К памяти
// обращается харнесс, а не LLM. Модель лишь предлагает вызвать инструмент,
// а харнесс решает, будет ли он вызван и как.
//
// Цикл агента — повторяющаяся последовательность, в которой контекст
// передаётся LLM, модель выбирает шаг, шаг выполняется, а его результат
// добавляется в контекст, пока не выполнены критерии завершения или не
// исчерпаны лимиты. Перед запуском цикла запрос один раз проверяется на
// валидность и на безопасность, после чего собирается контекст.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/wannacry39/agent_nirs/internal/agentcontext"
	"github.com/wannacry39/agent_nirs/internal/llm"
	"github.com/wannacry39/agent_nirs/internal/memory"
	"github.com/wannacry39/agent_nirs/internal/skills"
	"github.com/wannacry39/agent_nirs/internal/tools"
)

// Limits — лимиты: предельные значения ресурсов, при достижении которых
// работа над запросом принудительно останавливается.
type Limits struct {
	MaxSteps          int // сколько раз за один запрос можно вызвать модель
	MaxTokens         int // сколько токенов можно потратить на один запрос
	MaxRequestRunes   int // максимальная длина запроса пользователя
	MaxHistoryMessage int // сколько сообщений истории держать в контексте
}

// DefaultLimits — лимиты по умолчанию.
var DefaultLimits = Limits{MaxSteps: 8, MaxTokens: 50_000, MaxRequestRunes: 2000, MaxHistoryMessage: 30}

// Config — из чего собран харнесс.
type Config struct {
	LLM          *llm.Client
	SystemPrompt string
	Toolboxes    []tools.Toolbox // источники инструментов: локальные и/или MCP
	Memory       *memory.Memory  // nil — без памяти
	Skills       *skills.Library // nil — без skills
	Limits       Limits
	Confirm      func(question string) bool // подтверждение опасных вызовов; nil — запрещать их
	Log          io.Writer                  // куда писать ход работы; nil — в stderr
}

// Harness — харнесс.
type Harness struct {
	cfg     Config
	context *agentcontext.Context
	route   map[string]tools.Toolbox // какой Toolbox выполняет инструмент с этим именем
	byName  map[string]tools.Tool
}

// Имя встроенного инструмента харнесса для загрузки skill.
const loadSkillTool = "load_skill"

// New собирает харнесс.
func New(cfg Config) (*Harness, error) {
	if cfg.Limits == (Limits{}) {
		cfg.Limits = DefaultLimits
	}
	if cfg.Log == nil {
		cfg.Log = os.Stderr
	}
	h := &Harness{
		cfg:     cfg,
		context: &agentcontext.Context{SystemPrompt: cfg.SystemPrompt},
		route:   map[string]tools.Toolbox{},
		byName:  map[string]tools.Tool{},
	}

	// Собираем общий список инструментов и запоминаем, кто какой выполняет.
	for _, box := range cfg.Toolboxes {
		for _, tool := range box.Tools() {
			if _, exists := h.byName[tool.Name]; exists {
				return nil, fmt.Errorf("инструмент %q объявлен дважды", tool.Name)
			}
			h.route[tool.Name] = box
			h.byName[tool.Name] = tool
			h.context.Tools = append(h.context.Tools, tool.Spec())
		}
	}

	// Skills: в контекст идут только названия и описания, а полный текст
	// модель загружает встроенным инструментом харнесса load_skill.
	if !cfg.Skills.Empty() {
		h.context.SkillsCatalog = cfg.Skills.Catalog()
		skillTool := tools.Tool{
			Name:        loadSkillTool,
			Description: "Загружает полный текст skill с инструкциями по его названию",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "Название skill из списка доступных"},
				},
				"required": []string{"name"},
			},
		}
		h.byName[loadSkillTool] = skillTool
		h.context.Tools = append(h.context.Tools, skillTool.Spec())
	}
	return h, nil
}

// Run обрабатывает один запрос пользователя и возвращает ответ.
func (h *Harness) Run(ctx context.Context, request string) (string, error) {
	request = strings.TrimSpace(request)

	// 1. Проверка на валидность.
	if err := h.validate(request); err != nil {
		h.logf("[харнесс] запрос отклонён: %v", err)
		return "Запрос отклонён: " + err.Error(), nil
	}
	// 2. Проверка на безопасность.
	if err := checkSecurity(request); err != nil {
		h.logf("[харнесс] запрос отклонён: %v", err)
		return "Запрос отклонён: " + err.Error(), nil
	}
	// Команда «запомни: ...» обрабатывается харнессом без участия модели:
	// модель о памяти ничего не знает.
	if fact, ok := cutPrefixFold(request, "запомни:"); ok && h.cfg.Memory != nil {
		fact = strings.TrimSpace(fact)
		if err := h.cfg.Memory.Remember(fact); err != nil {
			return "", err
		}
		h.logf("[харнесс] записал в память: %s", fact)
		return "Запомнил.", nil
	}

	// 3. Сборка контекста: запрос и релевантные данные из памяти.
	h.context.AddUser(request)
	h.context.Relevant = nil
	if h.cfg.Memory != nil {
		h.context.Relevant = h.cfg.Memory.Relevant(request, 3)
		if len(h.context.Relevant) > 0 {
			h.logf("[харнесс] добавил в контекст из памяти: %q", h.context.Relevant)
		}
	}

	// 4. Цикл агента.
	tokens := 0
	for step := 1; step <= h.cfg.Limits.MaxSteps; step++ {
		if err := h.manageContext(ctx); err != nil {
			return "", err
		}

		messages := h.context.Messages()
		h.logf("[харнесс] шаг %d: отправляю в LLM контекст (%d сообщений, %d инструментов)",
			step, len(messages), len(h.context.Tools))
		resp, err := h.cfg.LLM.Complete(ctx, messages, h.context.Tools)
		if err != nil {
			return "", err
		}
		tokens += resp.TotalTokens
		h.context.AddAssistant(resp.Message)

		// Модель ответила текстом, без вызова инструментов: по критериям
		// завершения из системного промпта задача решена.
		if len(resp.Message.ToolCalls) == 0 {
			h.logf("[LLM] ответила текстом, цикл завершён (токенов за запрос: %d)", tokens)
			return resp.Message.Content, nil
		}

		// Модель выбрала шаг — вызвать инструменты. Выполняет их харнесс.
		for _, call := range resp.Message.ToolCalls {
			h.logf("[LLM] предлагает вызвать %s(%s)", call.Function.Name, call.Function.Arguments)
			result := h.execute(ctx, call)
			h.logf("[харнесс] результат %s: %s", call.Function.Name, preview(result.Content))
			h.context.AddToolResult(call.ID, result.Content)
		}

		if tokens > h.cfg.Limits.MaxTokens {
			h.logf("[харнесс] исчерпан лимит токенов (%d)", tokens)
			return "Остановлено: исчерпан лимит токенов на запрос.", nil
		}
	}
	h.logf("[харнесс] исчерпан лимит шагов (%d)", h.cfg.Limits.MaxSteps)
	return "Остановлено: исчерпан лимит шагов на запрос.", nil
}

// execute решает, выполнять ли вызов, и выполняет его.
func (h *Harness) execute(ctx context.Context, call llm.ToolCall) tools.Result {
	name := call.Function.Name

	tool, known := h.byName[name]
	if !known {
		return tools.Result{Content: fmt.Sprintf("инструмент %q не существует", name), IsError: true}
	}

	var args map[string]any
	if strings.TrimSpace(call.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return tools.Result{Content: "параметры не являются корректным JSON: " + err.Error(), IsError: true}
		}
	}
	for _, required := range tool.Required() {
		if _, ok := args[required]; !ok {
			return tools.Result{Content: fmt.Sprintf("не передан обязательный параметр %q", required), IsError: true}
		}
	}

	// Встроенный инструмент харнесса: загрузить skill в контекст.
	if name == loadSkillTool {
		skillName, _ := args["name"].(string)
		text, err := h.cfg.Skills.Read(skillName)
		if err != nil {
			return tools.Result{Content: err.Error(), IsError: true}
		}
		h.logf("[харнесс] загружаю в контекст skill %q", skillName)
		return tools.Result{Content: text}
	}

	// Опасный инструмент выполняется только с подтверждения пользователя.
	if tool.Dangerous {
		question := fmt.Sprintf("Модель хочет выполнить %s(%s). Разрешить?", name, call.Function.Arguments)
		if h.cfg.Confirm == nil || !h.cfg.Confirm(question) {
			h.logf("[харнесс] вызов %s запрещён", name)
			return tools.Result{Content: "пользователь запретил этот вызов", IsError: true}
		}
	}

	box := h.route[name]
	h.logf("[харнесс] выполняю %s (%s)", name, box.Name())
	return box.Call(ctx, name, args)
}

// manageContext следит за объёмом контекста: если история слишком длинная,
// старая часть сжимается в краткое содержание (его готовит сама модель),
// а затем обрезается.
func (h *Harness) manageContext(ctx context.Context) error {
	drop := h.context.OldestTurns(h.cfg.Limits.MaxHistoryMessage)
	if drop == 0 {
		return nil
	}
	old := h.context.History[:drop]
	var transcript strings.Builder
	if h.context.Summary != "" {
		transcript.WriteString("Ранее: " + h.context.Summary + "\n")
	}
	for _, message := range old {
		if message.Content != "" {
			fmt.Fprintf(&transcript, "%s: %s\n", message.Role, message.Content)
		}
	}
	resp, err := h.cfg.LLM.Complete(ctx, []llm.Message{
		{Role: "system", Content: "Сожми переписку в краткое содержание из 2–4 предложений. Сохрани факты, важные для продолжения диалога."},
		{Role: "user", Content: transcript.String()},
	}, nil)
	if err != nil {
		return fmt.Errorf("сжатие контекста: %w", err)
	}
	h.context.Summary = resp.Message.Content
	h.context.Drop(drop)
	h.logf("[харнесс] контекст сжат: %d старых сообщений заменены кратким содержанием", drop)
	return nil
}

func (h *Harness) validate(request string) error {
	if request == "" {
		return errors.New("пустой запрос")
	}
	if utf8.RuneCountInString(request) > h.cfg.Limits.MaxRequestRunes {
		return fmt.Errorf("запрос длиннее %d символов", h.cfg.Limits.MaxRequestRunes)
	}
	return nil
}

// Простые признаки промпт-инъекции. В реальной системе проверка сложнее,
// здесь важно показать, где в цикле она стоит.
var injectionPatterns = regexp.MustCompile(`(?i)(забудь|игнорируй|отмени)\s+(все\s+)?(предыдущие|прежние)\s+(инструкции|правила)|ignore\s+(all\s+)?previous\s+instructions|покажи\s+(свой\s+)?системный\s+промпт`)

func checkSecurity(request string) error {
	if injectionPatterns.MatchString(request) {
		return errors.New("похоже на промпт-инъекцию")
	}
	return nil
}

func (h *Harness) logf(format string, args ...any) {
	fmt.Fprintf(h.cfg.Log, format+"\n", args...)
}

func preview(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	if runes := []rune(text); len(runes) > 160 {
		return string(runes[:160]) + "…"
	}
	return text
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}
