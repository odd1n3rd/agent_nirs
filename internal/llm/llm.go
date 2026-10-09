// Package llm — LLM (большая языковая модель).
//
// Нейросеть, которая по входному тексту генерирует ответ токен за токеном.
// На ней держатся два свойства всей архитектуры:
//
//  1. Модель ничего не помнит между вызовами. Всё нужное (системный промпт,
//     историю, список инструментов) приходится передавать ей заново
//     в каждом запросе.
//  2. Модель ничего не выполняет. Если ей передан список инструментов, она
//     может только вернуть вызов инструмента (tool call): имя и параметры
//     в JSON. Выполнить этот вызов должен кто-то другой.
//
// Вызов модели — это обычный HTTP-запрос с JSON к OpenAI-совместимому API,
// поэтому подходит любой провайдер с таким API (OpenAI, OpenRouter,
// локальная Ollama и т. д.).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Message — одно сообщение в контексте модели.
type Message struct {
	Role       string     `json:"role"` // system, user, assistant или tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // вызовы инструментов, которые запросила модель
	ToolCallID string     `json:"tool_call_id,omitempty"` // к какому вызову относится результат инструмента
}

// ToolCall — вызов инструмента (tool call): структурированный ответ модели
// с именем инструмента и параметрами. Модель только просит его выполнить.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall — имя инструмента и параметры вызова в виде JSON-строки.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolSpec — описание инструмента в формате API модели.
type ToolSpec struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

// FunctionSpec — имя, описание и схема параметров инструмента.
type FunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Response — ответ модели на один вызов.
type Response struct {
	Message     Message // текст ответа или вызовы инструментов
	TotalTokens int     // сколько токенов ушло на этот вызов
}

// Client — клиент LLM. Один вызов Complete = один HTTP-запрос к модели.
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// NewFromEnv создаёт клиент по переменным окружения:
// LLM_BASE_URL, LLM_API_KEY, LLM_MODEL.
func NewFromEnv() *Client {
	return &Client{
		BaseURL: envOr("LLM_BASE_URL", "https://api.openai.com/v1"),
		APIKey:  os.Getenv("LLM_API_KEY"),
		Model:   envOr("LLM_MODEL", "gpt-4o-mini"),
		HTTP:    &http.Client{Timeout: 120 * time.Second},
	}
}

// Complete отправляет модели сообщения и (необязательно) список инструментов.
func (c *Client) Complete(ctx context.Context, messages []Message, tools []ToolSpec) (Response, error) {
	body, err := json.Marshal(struct {
		Model    string     `json:"model"`
		Messages []Message  `json:"messages"`
		Tools    []ToolSpec `json:"tools,omitempty"`
	}{c.Model, messages, tools})
	if err != nil {
		return Response{}, err
	}

	url := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("запрос к LLM: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("LLM вернула %d: %s", resp.StatusCode, raw)
	}

	var parsed struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("разбор ответа LLM: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("LLM вернула пустой ответ: %s", raw)
	}
	return Response{Message: parsed.Choices[0].Message, TotalTokens: parsed.Usage.TotalTokens}, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
