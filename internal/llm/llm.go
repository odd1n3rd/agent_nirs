// Package llm — LLM.
//
// LLM — большая языковая модель: нейросеть, обученная на больших объёмах
// текстов, которая по входному тексту генерирует ответ токен за токеном.
// Знания о языке и мире она получила при обучении, и во время работы они не
// обновляются, поэтому о событиях после обучения и о приватных данных
// пользователя модель сама не знает. Каждый вызов для неё независим: между
// вызовами она ничего не запоминает, поэтому всё нужное приходится
// передавать ей заново.
//
// Из определения следуют два свойства всей архитектуры:
//
//  1. Модель ничего не помнит между вызовами. Контекст (системный промпт,
//     историю, список инструментов) приходится передавать ей заново
//     в каждом запросе. Свежие и приватные данные она может получить только
//     из контекста: из памяти или через инструменты.
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
	Message      Message // текст ответа или вызовы инструментов
	TotalTokens  int     // сколько токенов ушло на этот вызов
	FinishReason string  // почему модель остановилась: stop, tool_calls, length...
}

// Client — клиент LLM. Один вызов Complete = один HTTP-запрос к модели.
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	Project string // заголовок OpenAI-Project (нужен, например, Yandex AI Studio)
	HTTP    *http.Client
}

// NewFromEnv создаёт клиент по переменным окружения:
// LLM_BASE_URL, LLM_API_KEY, LLM_MODEL и необязательной LLM_PROJECT.
func NewFromEnv() *Client {
	return &Client{
		BaseURL: envOr("LLM_BASE_URL", "https://api.openai.com/v1"),
		APIKey:  os.Getenv("LLM_API_KEY"),
		Model:   envOr("LLM_MODEL", "gpt-4o-mini"),
		Project: os.Getenv("LLM_PROJECT"),
		// Локальная модель на процессоре может отвечать минутами.
		HTTP: &http.Client{Timeout: 5 * time.Minute},
	}
}

// Complete отправляет модели сообщения и (необязательно) список инструментов.
func (c *Client) Complete(ctx context.Context, messages []Message, tools []ToolSpec) (Response, error) {
	return c.complete(ctx, messages, tools, "")
}

// CompleteAnswer — то же, но просит модель ответить текстом, без вызова
// инструментов (tool_choice: none). Список инструментов всё равно
// передаётся: в истории есть их вызовы.
func (c *Client) CompleteAnswer(ctx context.Context, messages []Message, tools []ToolSpec) (Response, error) {
	choice := ""
	if len(tools) > 0 {
		choice = "none"
	}
	return c.complete(ctx, messages, tools, choice)
}

func (c *Client) complete(ctx context.Context, messages []Message, tools []ToolSpec, toolChoice string) (Response, error) {
	body, err := json.Marshal(struct {
		Model      string     `json:"model"`
		Messages   []Message  `json:"messages"`
		Tools      []ToolSpec `json:"tools,omitempty"`
		ToolChoice string     `json:"tool_choice,omitempty"`
	}{c.Model, messages, tools, toolChoice})
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
	if c.Project != "" {
		req.Header.Set("OpenAI-Project", c.Project)
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
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
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
	tokens := parsed.Usage.TotalTokens
	if tokens == 0 {
		// Провайдер не сообщил расход токенов. Оцениваем грубо (около
		// 4 байт на токен), чтобы лимит токенов не отключался молча.
		tokens = (len(body) + len(raw)) / 4
	}
	choice := parsed.Choices[0]
	return Response{Message: choice.Message, TotalTokens: tokens, FinishReason: choice.FinishReason}, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
