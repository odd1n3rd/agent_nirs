package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	server := NewServer("test", "1")
	readOnly := true
	server.AddTool(ServerTool{
		Info: ToolInfo{
			Name:        "echo",
			InputSchema: map[string]any{"type": "object", "required": []string{"text"}},
			Annotations: &ToolAnnotations{ReadOnlyHint: &readOnly},
		},
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			return "эхо: " + args["text"].(string), nil
		},
	})
	server.AddTool(ServerTool{
		Info: ToolInfo{Name: "fail", InputSchema: map[string]any{"type": "object"}},
		Handler: func(context.Context, map[string]any) (string, error) {
			return "", errors.New("не получилось")
		},
	})
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return server, httpServer
}

func TestClientServerRoundTrip(t *testing.T) {
	_, httpServer := testServer(t)
	ctx := context.Background()
	client := NewClient(httpServer.URL)
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close(ctx)

	list := client.Tools()
	if len(list) != 2 || list[0].Dangerous || !list[1].Dangerous {
		t.Fatalf("список инструментов: %+v (без readOnlyHint инструмент опасный)", list)
	}
	if got := client.Call(ctx, "echo", map[string]any{"text": "привет"}); got.Content != "эхо: привет" || got.IsError {
		t.Fatalf("результат %+v", got)
	}
	if got := client.Call(ctx, "fail", nil); !got.IsError {
		t.Fatalf("ошибка инструмента должна прийти с IsError: %+v", got)
	}
}

func TestClientReconnectsWhenSessionLost(t *testing.T) {
	server, httpServer := testServer(t)
	ctx := context.Background()
	client := NewClient(httpServer.URL)
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	// Сервер «забыл» все сессии (например, перезапустился).
	server.mu.Lock()
	for id := range server.sessions {
		delete(server.sessions, id)
	}
	server.mu.Unlock()

	if got := client.Call(ctx, "echo", map[string]any{"text": "снова"}); got.IsError {
		t.Fatalf("клиент должен переподключиться: %+v", got)
	}
}

func TestServerRejectsForeignOriginAndMissingSession(t *testing.T) {
	_, httpServer := testServer(t)
	post := func(origin, session, body string) int {
		req, _ := http.NewRequest(http.MethodPost, httpServer.URL, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if session != "" {
			req.Header.Set(headerSessionID, session)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	if code := post("https://evil.example", "", list); code != http.StatusForbidden {
		t.Fatalf("чужой Origin: %d", code)
	}
	if code := post("", "", list); code != http.StatusBadRequest {
		t.Fatalf("без сессии: %d", code)
	}
	if code := post("", "unknown", list); code != http.StatusNotFound {
		t.Fatalf("неизвестная сессия: %d", code)
	}
}
