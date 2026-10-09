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

	"github.com/wannacry39/agent_nirs/internal/tools"
)

// Client — MCP-клиент: компонент приложения, работающего с LLM, через
// который оно связано с MCP-сервером. Клиент подключается к серверу,
// получает от него список инструментов, отправляет ему вызовы по MCP
// и принимает ответы.
//
// Client реализует tools.Toolbox, поэтому харнесс работает с MCP-инструментами
// так же, как с локальными. Разница только в том, где выполняется вызов:
// локальный инструмент выполняет харнесс, MCP-инструмент — MCP-сервер.
type Client struct {
	url  string
	http *http.Client

	mu        sync.Mutex
	nextID    int
	sessionID string
	tools     []tools.Tool
}

// NewClient создаёт клиент для MCP-сервера по адресу url.
func NewClient(url string) *Client {
	return &Client{url: url, http: &http.Client{Timeout: 60 * time.Second}}
}

// Connect — подключение к серверу: initialize, затем tools/list.
func (c *Client) Connect(ctx context.Context) error {
	_, err := c.request(ctx, "initialize", initializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      implementation{"agent_nirs", "1.0"},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if err := c.notify(ctx, "notifications/initialized"); err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}

	raw, err := c.request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	var list listToolsResult
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("разбор tools/list: %w", err)
	}

	// Переводим описания из формата MCP в общий формат инструментов.
	converted := make([]tools.Tool, 0, len(list.Tools))
	for _, info := range list.Tools {
		dangerous := info.Annotations != nil && info.Annotations.DestructiveHint != nil && *info.Annotations.DestructiveHint
		converted = append(converted, tools.Tool{
			Name:        info.Name,
			Description: info.Description,
			InputSchema: info.InputSchema,
			Dangerous:   dangerous,
		})
	}
	c.mu.Lock()
	c.tools = converted
	c.mu.Unlock()
	return nil
}

// Name — для логов харнесса.
func (c *Client) Name() string { return "MCP-сервер " + c.url }

// Tools возвращает список инструментов, полученный от сервера.
func (c *Client) Tools() []tools.Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tools
}

// Call отправляет серверу tools/call. Выполняет инструмент сервер.
func (c *Client) Call(ctx context.Context, name string, args map[string]any) tools.Result {
	raw, err := c.request(ctx, "tools/call", callToolParams{Name: name, Arguments: args})
	if err != nil {
		return tools.Result{Content: "ошибка MCP: " + err.Error(), IsError: true}
	}
	var result callToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return tools.Result{Content: "ошибка разбора ответа MCP: " + err.Error(), IsError: true}
	}
	var texts []string
	for _, part := range result.Content {
		if part.Type == "text" {
			texts = append(texts, part.Text)
		}
	}
	return tools.Result{Content: strings.Join(texts, "\n"), IsError: result.IsError}
}

// Close закрывает сессию на сервере.
func (c *Client) Close(ctx context.Context) {
	if c.sessionID == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.url, nil)
	if err != nil {
		return
	}
	req.Header.Set(headerSessionID, c.sessionID)
	if resp, err := c.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

// request отправляет запрос JSON-RPC и ждёт ответ с тем же ID.
func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	resp, err := c.post(ctx, method, id, params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if sessionID := resp.Header.Get(headerSessionID); sessionID != "" {
		c.sessionID = sessionID
	}

	rpc, err := readResponse(resp, id)
	if err != nil {
		return nil, err
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("ошибка JSON-RPC %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	return rpc.Result, nil
}

// notify отправляет уведомление JSON-RPC (без ID, ответа не ждём).
func (c *Client) notify(ctx context.Context, method string) error {
	resp, err := c.post(ctx, method, nil, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) post(ctx context.Context, method string, id any, params any) (*http.Response, error) {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		msg["id"] = id
	}
	if params != nil {
		msg["params"] = params
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// По спецификации клиент должен принимать и JSON, и поток SSE.
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(headerProtocolVersion, ProtocolVersion)
	if c.sessionID != "" {
		req.Header.Set(headerSessionID, c.sessionID)
	}
	return c.http.Do(req)
}

// readResponse читает ответ сервера. Сервер может ответить обычным JSON
// или потоком SSE (так делают, например, серверы на официальном SDK).
func readResponse(resp *http.Response, id int) (Response, error) {
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType != "text/event-stream" {
		var rpc Response
		err := json.NewDecoder(resp.Body).Decode(&rpc)
		return rpc, err
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "" && data.Len() > 0: // конец события
			var rpc Response
			if err := json.Unmarshal([]byte(data.String()), &rpc); err == nil && fmt.Sprint(rpc.ID) == fmt.Sprint(id) {
				return rpc, nil
			}
			data.Reset()
		}
	}
	if data.Len() > 0 {
		var rpc Response
		if err := json.Unmarshal([]byte(data.String()), &rpc); err == nil && fmt.Sprint(rpc.ID) == fmt.Sprint(id) {
			return rpc, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return Response{}, err
	}
	return Response{}, errors.New("в потоке SSE нет ответа на запрос")
}
