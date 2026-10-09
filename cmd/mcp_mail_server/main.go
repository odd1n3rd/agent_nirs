// MCP-сервер почты.
//
// Предоставляет функции стороннего сервиса (почтового ящика) в виде
// инструментов по MCP: get_all_incoming_messages, search_messages,
// delete_message. Сам выполняет их вызовы. Почта хранится в памяти процесса,
// письма выдуманные.
//
// Инструменты чтения помечены аннотацией readOnlyHint, delete_message —
// destructiveHint. Харнесс увидит это в tools/list: инструменты без
// readOnlyHint он считает опасными и перед вызовом спрашивает подтверждение.
//
// Запуск: go run ./cmd/mcp_mail_server   (адрес меняется переменной MCP_ADDR)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/wannacry39/agent_nirs/internal/mcp"
)

type message struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type mailbox struct {
	mu       sync.Mutex
	messages []message
}

func (m *mailbox) all() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return toJSON(m.messages)
}

func (m *mailbox) search(query string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	query = strings.ToLower(query)
	found := []message{}
	for _, msg := range m.messages {
		text := strings.ToLower(msg.From + " " + msg.Subject + " " + msg.Body)
		if strings.Contains(text, query) {
			found = append(found, msg)
		}
	}
	return toJSON(found)
}

func (m *mailbox) delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, msg := range m.messages {
		if msg.ID == id {
			m.messages = append(m.messages[:i], m.messages[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("письмо %q не найдено", id)
}

func main() {
	box := &mailbox{messages: []message{
		{"1", "boss@company.com", "Отчёт по проекту", "Пришли, пожалуйста, отчёт до пятницы."},
		{"2", "noreply@shop.ru", "Скидка 50%", "Только сегодня скидка на всё!"},
		{"3", "anna@company.com", "Встреча в четверг", "Переносим встречу на 15:00."},
		{"4", "boss@company.com", "Отпуск", "Согласовал твой отпуск с 1 по 14 декабря."},
	}}

	server := mcp.NewServer("mail-server", "1.0.0")
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: ptr(true)}

	server.AddTool(mcp.ServerTool{
		Info: mcp.ToolInfo{
			Name:        "get_all_incoming_messages",
			Description: "Возвращает все входящие письма: id, отправитель, тема, текст",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Annotations: readOnly,
		},
		Handler: func(context.Context, map[string]any) (string, error) { return box.all(), nil },
	})

	server.AddTool(mcp.ServerTool{
		Info: mcp.ToolInfo{
			Name:        "search_messages",
			Description: "Ищет входящие письма, в отправителе, теме или тексте которых встречается строка",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Строка для поиска, например адрес отправителя"},
				},
				"required": []string{"query"},
			},
			Annotations: readOnly,
		},
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			query, _ := args["query"].(string)
			return box.search(query), nil
		},
	})

	server.AddTool(mcp.ServerTool{
		Info: mcp.ToolInfo{
			Name:        "delete_message",
			Description: "Удаляет письмо по его id",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message_id": map[string]any{"type": "string", "description": "id письма"},
				},
				"required": []string{"message_id"},
			},
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: ptr(false), DestructiveHint: ptr(true)},
		},
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			id := fmt.Sprint(args["message_id"])
			if err := box.delete(id); err != nil {
				return "", err
			}
			return "письмо " + id + " удалено", nil
		},
	})

	addr := os.Getenv("MCP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8000"
	}
	http.Handle("/mcp", server)
	log.Printf("[MCP-сервер] почта слушает http://%s/mcp", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func ptr(v bool) *bool { return &v }

func toJSON(v any) string {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw)
}
