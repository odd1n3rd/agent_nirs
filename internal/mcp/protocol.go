// Package mcp — MCP (Model Context Protocol), MCP-сервер и MCP-клиент.
//
// MCP (Model Context Protocol) — протокол, по которому приложение,
// работающее с LLM, подключает инструменты сторонних сервисов в едином
// формате. Сообщения в нём передаются по транспорту HTTP, а внутри
// используется JSON-RPC 2.0.
//
// Слои разделены так же, как в определении:
//
//	protocol.go  — сообщения JSON-RPC 2.0 и MCP (что передаётся);
//	transport.go — транспорт HTTP (как доставляется), ничего не знает
//	               о содержании сообщений;
//	client.go    — MCP-клиент; server.go — MCP-сервер.
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

import (
	"bytes"
	"encoding/json"
)

// ProtocolVersion — версия протокола MCP.
const ProtocolVersion = "2025-06-18"

// Заголовки HTTP, которые использует MCP.
const (
	headerSessionID       = "Mcp-Session-Id"
	headerProtocolVersion = "MCP-Protocol-Version"
)

// --- JSON-RPC 2.0 ---
//
// JSON-RPC 2.0 — протокол удалённого вызова функций, в котором сообщения
// записываются в JSON. В запросе указываются имя вызываемого метода,
// параметры и идентификатор, а в ответе с тем же идентификатором
// возвращается результат или описание ошибки.

// Request — запрос JSON-RPC 2.0. Если ID нет, это уведомление: ответа не ждут.
// ID хранится как есть (json.RawMessage), чтобы отличать отсутствие ID от null.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification — запрос без ID: уведомление, на которое не отвечают.
func (r Request) IsNotification() bool { return len(r.ID) == 0 }

// Response — ответ JSON-RPC 2.0 с тем же ID, что у запроса:
// либо Result, либо Error.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// sameID сравнивает идентификаторы запроса и ответа.
func sameID(a, b json.RawMessage) bool {
	return len(a) > 0 && string(bytes.TrimSpace(a)) == string(bytes.TrimSpace(b))
}

// RPCError — ошибка JSON-RPC 2.0.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Стандартные коды ошибок JSON-RPC 2.0.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
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
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`    // инструмент только читает данные
	DestructiveHint *bool `json:"destructiveHint,omitempty"` // инструмент что-то удаляет или перезаписывает
}

// Dangerous — может ли инструмент менять данные. По спецификации MCP
// аннотации — лишь подсказки, а без них инструмент считается изменяющим
// данные (readOnlyHint=false). Поэтому безопасным считается только
// инструмент с явным readOnlyHint=true, остальные требуют подтверждения.
func (info ToolInfo) Dangerous() bool {
	return info.Annotations == nil || info.Annotations.ReadOnlyHint == nil || !*info.Annotations.ReadOnlyHint
}

type listToolsParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type listToolsResult struct {
	Tools      []ToolInfo `json:"tools"`
	NextCursor string     `json:"nextCursor,omitempty"` // есть следующая страница списка
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
