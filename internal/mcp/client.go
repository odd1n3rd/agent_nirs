package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

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
	url       string
	transport Transport

	mu     sync.Mutex
	nextID int
	tools  []tools.Tool
}

// NewClient создаёт клиент для MCP-сервера по адресу url (транспорт HTTP).
func NewClient(url string) *Client {
	return &Client{url: url, transport: NewHTTPTransport(url)}
}

// Connect — подключение к серверу: initialize, затем список инструментов.
func (c *Client) Connect(ctx context.Context) error {
	if err := c.initialize(ctx); err != nil {
		return err
	}
	return c.listTools(ctx)
}

// initialize — знакомство с сервером, после него сервер выдаёт сессию.
func (c *Client) initialize(ctx context.Context) error {
	_, err := c.send(ctx, "initialize", initializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      implementation{"agent_nirs", "1.0"},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	notification := Request{JSONRPC: "2.0", Method: "notifications/initialized"}
	if _, err := c.transport.Send(ctx, notification); err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}
	return nil
}

// listTools получает список инструментов (с учётом страниц) и переводит
// описания из формата MCP в общий формат инструментов.
func (c *Client) listTools(ctx context.Context) error {
	var converted []tools.Tool
	cursor := ""
	seen := map[string]bool{}
	for {
		raw, err := c.request(ctx, "tools/list", listToolsParams{Cursor: cursor})
		if err != nil {
			return fmt.Errorf("tools/list: %w", err)
		}
		var page listToolsResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return fmt.Errorf("разбор tools/list: %w", err)
		}
		for _, info := range page.Tools {
			converted = append(converted, tools.Tool{
				Name:        info.Name,
				Description: info.Description,
				InputSchema: info.InputSchema,
				Dangerous:   info.Dangerous(),
			})
		}
		if page.NextCursor == "" {
			break
		}
		if seen[page.NextCursor] {
			return fmt.Errorf("tools/list: сервер повторил курсор %q", page.NextCursor)
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	c.mu.Lock()
	c.tools = converted
	c.mu.Unlock()
	return nil
}

// Name — для логов харнесса.
func (c *Client) Name() string { return "MCP-сервер " + c.url }

// Tools возвращает копию списка инструментов, полученного от сервера.
func (c *Client) Tools() []tools.Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tools.Tool(nil), c.tools...)
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
func (c *Client) Close(ctx context.Context) { c.transport.Close(ctx) }

// request отправляет запрос. Если сервер забыл сессию, клиент по спецификации
// подключается заново (initialize), заново получает список инструментов
// (сервер мог перезапуститься с другим набором) и повторяет запрос один раз.
func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	result, err := c.send(ctx, method, params)
	if !errors.Is(err, ErrSessionExpired) {
		return result, err
	}
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	if method != "tools/list" {
		if err := c.listTools(ctx); err != nil {
			return nil, err
		}
	}
	if method == "tools/call" && !c.hasTool(params) {
		return nil, fmt.Errorf("после переподключения сервер больше не предоставляет этот инструмент")
	}
	return c.send(ctx, method, params)
}

func (c *Client) hasTool(params any) bool {
	call, ok := params.(callToolParams)
	if !ok {
		return true
	}
	for _, tool := range c.Tools() {
		if tool.Name == call.Name {
			return true
		}
	}
	return false
}

// send отправляет запрос JSON-RPC с новым ID и возвращает результат.
func (c *Client) send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.nextID++
	id := json.RawMessage(strconv.Itoa(c.nextID))
	c.mu.Unlock()

	resp, err := c.transport.Send(ctx, Request{JSONRPC: "2.0", ID: id, Method: method, Params: rawParams})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("ошибка JSON-RPC %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}
