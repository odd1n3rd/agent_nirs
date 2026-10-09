package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
)

// ServerTool — инструмент MCP-сервера: описание и реализация.
type ServerTool struct {
	Info    ToolInfo
	Handler func(ctx context.Context, args map[string]any) (string, error)
}

// Server — MCP-сервер: предоставляет функции стороннего сервиса в виде
// инструментов по MCP и сам выполняет их вызовы.
//
// Сервер принимает POST-запросы с JSON-RPC 2.0 и отвечает JSON.
// Это подмножество транспорта Streamable HTTP из спецификации MCP:
// потоковые ответы (SSE) сервер не использует.
type Server struct {
	name, version string

	mu       sync.Mutex
	tools    []ServerTool
	sessions map[string]bool
}

// NewServer создаёт MCP-сервер.
func NewServer(name, version string) *Server {
	return &Server{name: name, version: version, sessions: map[string]bool{}}
}

// AddTool регистрирует инструмент.
func (s *Server) AddTool(tool ServerTool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = append(s.tools, tool)
}

// ServeHTTP обрабатывает одно сообщение JSON-RPC.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete: // клиент закрывает сессию
		s.mu.Lock()
		delete(s.sessions, r.Header.Get(headerSessionID))
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		// Отдельный поток событий (GET + SSE) этот сервер не поддерживает,
		// спецификация разрешает в этом случае отвечать 405.
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, Response{JSONRPC: "2.0", Error: &RPCError{codeParseError, "некорректный JSON"}})
		return
	}

	// Уведомление (без ID): ответ не нужен.
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if req.Method != "initialize" && !s.knownSession(r.Header.Get(headerSessionID)) {
		http.Error(w, "неизвестная сессия, сначала вызовите initialize", http.StatusNotFound)
		return
	}

	result, rpcErr := s.handle(r.Context(), w, req)
	resp := Response{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
	if rpcErr == nil {
		resp.Result, _ = json.Marshal(result)
	}
	writeJSON(w, resp)
}

func (s *Server) handle(ctx context.Context, w http.ResponseWriter, req Request) (any, *RPCError) {
	switch req.Method {
	case "initialize":
		sessionID := newSessionID()
		s.mu.Lock()
		s.sessions[sessionID] = true
		s.mu.Unlock()
		w.Header().Set(headerSessionID, sessionID)
		log.Printf("[MCP-сервер] новая сессия %s", sessionID)
		return initializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      implementation{s.name, s.version},
		}, nil

	case "ping":
		return map[string]any{}, nil

	case "tools/list":
		s.mu.Lock()
		infos := make([]ToolInfo, 0, len(s.tools))
		for _, tool := range s.tools {
			infos = append(infos, tool.Info)
		}
		s.mu.Unlock()
		log.Printf("[MCP-сервер] tools/list: отдаю %d инструментов", len(infos))
		return listToolsResult{Tools: infos}, nil

	case "tools/call":
		var params callToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &RPCError{codeInvalidParams, "некорректные параметры"}
		}
		tool, ok := s.findTool(params.Name)
		if !ok {
			return nil, &RPCError{codeInvalidParams, "неизвестный инструмент: " + params.Name}
		}
		log.Printf("[MCP-сервер] tools/call: выполняю %s %v", params.Name, params.Arguments)
		text, err := tool.Handler(ctx, params.Arguments)
		if err != nil {
			// Ошибка выполнения — это результат инструмента, а не ошибка протокола.
			return callToolResult{Content: []Content{{Type: "text", Text: err.Error()}}, IsError: true}, nil
		}
		return callToolResult{Content: []Content{{Type: "text", Text: text}}}, nil

	default:
		return nil, &RPCError{codeMethodNotFound, "метод не поддерживается: " + req.Method}
	}
}

func (s *Server) findTool(name string) (ServerTool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tool := range s.tools {
		if tool.Info.Name == name {
			return tool, true
		}
	}
	return ServerTool{}, false
}

func (s *Server) knownSession(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func newSessionID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
