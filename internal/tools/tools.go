// Package tools — инструменты.
//
// Инструмент — функция, выполнение которой модель может запросить в ходе
// работы, чтобы получить данные или совершить действие во внешнем мире.
// Каждый инструмент описывается полями: имя (name), описание (description)
// и схема параметров (input_schema), где указаны параметры, их типы и какие
// из них обязательны (required).
//
// В контекст модели попадает только описание. Реализацию модель не видит,
// поэтому локальный инструмент и MCP-инструмент для неё неотличимы. Где
// инструмент выполняется, знает только харнесс: он хранит, какой Toolbox
// отвечает за какое имя.
package tools

import (
	"context"
	"fmt"

	"github.com/wannacry39/agent_nirs/internal/llm"
)

// Tool — описание инструмента.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	// Dangerous — опасный инструмент (удаление, отправка и т. п.).
	// Харнесс спросит подтверждение у пользователя перед вызовом.
	Dangerous bool
}

// Spec возвращает описание в формате, который понимает API модели.
func (t Tool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		},
	}
}

// Required возвращает список обязательных параметров из схемы.
func (t Tool) Required() []string {
	var names []string
	switch list := t.InputSchema["required"].(type) {
	case []string:
		names = list
	case []any:
		for _, name := range list {
			if s, ok := name.(string); ok {
				names = append(names, s)
			}
		}
	}
	return names
}

// Result — результат инструмента: запрошенные данные или сообщение об ошибке.
type Result struct {
	Content string
	IsError bool
}

// Toolbox — источник инструментов, с которым работает харнесс.
// Реализации: LocalToolbox (локальные инструменты, выполняет сам харнесс)
// и mcp.Client (MCP-инструменты, выполняет MCP-сервер).
type Toolbox interface {
	Name() string
	Tools() []Tool
	Call(ctx context.Context, name string, args map[string]any) Result
}

// Handler — реализация локального инструмента.
type Handler func(ctx context.Context, args map[string]any) (string, error)

// LocalToolbox — локальные инструменты: реализованы и выполняются внутри
// самой программы, без обращения к MCP-серверу.
type LocalToolbox struct {
	tools    []Tool
	handlers map[string]Handler
}

// NewLocalToolbox создаёт пустой набор локальных инструментов.
func NewLocalToolbox() *LocalToolbox {
	return &LocalToolbox{handlers: map[string]Handler{}}
}

// Register добавляет инструмент и его реализацию.
func (b *LocalToolbox) Register(tool Tool, handler Handler) {
	b.tools = append(b.tools, tool)
	b.handlers[tool.Name] = handler
}

// Name — для логов харнесса.
func (b *LocalToolbox) Name() string { return "локально" }

// Tools возвращает описания инструментов.
func (b *LocalToolbox) Tools() []Tool { return b.tools }

// Call выполняет инструмент прямо в этом процессе.
func (b *LocalToolbox) Call(ctx context.Context, name string, args map[string]any) Result {
	handler, ok := b.handlers[name]
	if !ok {
		return Result{Content: fmt.Sprintf("инструмент %q не найден", name), IsError: true}
	}
	content, err := handler(ctx, args)
	if err != nil {
		return Result{Content: "ошибка: " + err.Error(), IsError: true}
	}
	return Result{Content: content}
}
