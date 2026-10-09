// Package mcp — MCP (Model Context Protocol), MCP-сервер и MCP-клиент.
//
// MCP — протокол, по которому приложение, работающее с LLM, подключает
// инструменты сторонних сервисов в едином формате. Сообщения передаются
// по транспорту HTTP, а внутри используется JSON-RPC 2.0.
//
// Здесь протокол реализован вручную на стандартной библиотеке, чтобы было
// видно, из чего он состоит. Используются три метода:
//
//	initialize  — знакомство клиента и сервера;
//	tools/list  — клиент запрашивает список инструментов;
//	tools/call  — клиент просит выполнить инструмент.
//
// В реальном проекте вместо этого берут официальный SDK
// (github.com/modelcontextprotocol/go-sdk).
package mcp

import "encoding/json"

// ProtocolVersion — версия протокола MCP.
const ProtocolVersion = "2025-06-18"

// Заголовки HTTP, которые использует MCP.
const (
	headerSessionID       = "Mcp-Session-Id"
	headerProtocolVersion = "MCP-Protocol-Version"
)

// --- JSON-RPC 2.0 ---

// Request — запрос JSON-RPC 2.0. Если ID нет, это уведомление: ответа не ждут.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response — ответ JSON-RPC 2.0: либо Result, либо Error.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError — ошибка JSON-RPC 2.0.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Стандартные коды ошибок JSON-RPC 2.0.
const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// --- Сообщения MCP ---

// ToolInfo — описание инструмента в MCP (поле схемы называется inputSchema).
type ToolInfo struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	InputSchema map[string]any   `json:"inputSchema"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations — подсказки о поведении инструмента.
type ToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"` // инструмент что-то удаляет или меняет
}

type listToolsResult struct {
	Tools []ToolInfo `json:"tools"`
}

type callToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// Content — часть результата инструмента. Здесь используется только текст.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type callToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      implementation `json:"clientInfo"`
}

type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      implementation `json:"serverInfo"`
}
