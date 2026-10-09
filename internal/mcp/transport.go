package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Transport — транспорт: уровень, который отвечает только за доставку
// сообщений от одной стороны к другой, независимо от их содержания.
// Что именно передаётся (JSON-RPC 2.0, методы MCP), решает MCP-клиент
// поверх транспорта, поэтому клиент не зависит от того, как сообщения
// доставляются.
type Transport interface {
	// Send доставляет сообщение и возвращает ответ на него.
	// Для уведомления (без ID) ответа нет, возвращается nil.
	Send(ctx context.Context, msg Request) (*Response, error)
	// Close завершает соединение (сессию) с сервером.
	Close(ctx context.Context)
}

// ErrSessionExpired — сервер больше не знает сессию: нужно подключиться заново.
var ErrSessionExpired = errors.New("сессия MCP истекла")

// HTTPTransport — транспорт HTTP из спецификации MCP (Streamable HTTP):
// каждое сообщение отправляется POST-запросом, сервер отвечает JSON или
// потоком SSE. Сессию сервер выдаёт в заголовке Mcp-Session-Id при
// initialize, дальше она передаётся в каждом запросе.
type HTTPTransport struct {
	url  string
	http *http.Client

	mu        sync.Mutex
	sessionID string
}

// NewHTTPTransport создаёт транспорт HTTP до сервера по адресу url.
func NewHTTPTransport(url string) *HTTPTransport {
	return &HTTPTransport{url: url, http: &http.Client{Timeout: 60 * time.Second}}
}

func (t *HTTPTransport) session() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessionID
}

func (t *HTTPTransport) setSession(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionID = id
}

// Send отправляет сообщение POST-запросом.
func (t *HTTPTransport) Send(ctx context.Context, msg Request) (*Response, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// По спецификации клиент должен принимать и JSON, и поток SSE.
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(headerProtocolVersion, ProtocolVersion)
	session := t.session()
	if session != "" && msg.Method != "initialize" {
		req.Header.Set(headerSessionID, session)
	}

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound && session != "" && msg.Method != "initialize" {
		t.setSession("")
		return nil, ErrSessionExpired
	}
	if msg.IsNotification() {
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		text, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(text)))
	}
	if msg.Method == "initialize" {
		t.setSession(resp.Header.Get(headerSessionID))
	}
	return readResponse(resp, msg.ID)
}

// Close закрывает сессию на сервере (DELETE).
func (t *HTTPTransport) Close(ctx context.Context) {
	session := t.session()
	if session == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.url, nil)
	if err != nil {
		return
	}
	req.Header.Set(headerSessionID, session)
	if resp, err := t.http.Do(req); err == nil {
		resp.Body.Close()
	}
	t.setSession("")
}

// readResponse читает ответ с нужным ID. Сервер может ответить обычным JSON
// или потоком SSE (так делают, например, серверы на официальном SDK).
func readResponse(resp *http.Response, id json.RawMessage) (*Response, error) {
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType != "text/event-stream" {
		var rpc Response
		if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
			return nil, fmt.Errorf("разбор ответа: %w", err)
		}
		if !sameID(id, rpc.ID) {
			return nil, fmt.Errorf("ответ с чужим ID %s вместо %s", rpc.ID, id)
		}
		return &rpc, nil
	}

	// SSE: события разделены пустой строкой, данные — в строках «data:».
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var data strings.Builder
	match := func() *Response {
		var rpc Response
		if err := json.Unmarshal([]byte(data.String()), &rpc); err == nil && sameID(id, rpc.ID) {
			return &rpc
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "" && data.Len() > 0: // конец события
			if rpc := match(); rpc != nil {
				return rpc, nil
			}
			data.Reset()
		}
	}
	if data.Len() > 0 {
		if rpc := match(); rpc != nil {
			return rpc, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("в потоке SSE нет ответа на запрос")
}
