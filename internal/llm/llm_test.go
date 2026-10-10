package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func testClient(url string) *Client {
	return &Client{BaseURL: url, Model: "m", HTTP: http.DefaultClient, Retries: 3, Log: io.Discard}
}

const okBody = `{"choices":[{"message":{"role":"assistant","content":"ок"},"finish_reason":"stop"}],"usage":{"total_tokens":7}}`

func TestRetriesOnRateLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, okBody)
	}))
	defer server.Close()

	resp, err := testClient(server.URL).Complete(context.Background(), []Message{{Role: "user", Content: "привет"}}, nil)
	if err != nil || resp.Message.Content != "ок" || resp.TotalTokens != 7 {
		t.Fatalf("ответ %+v, ошибка %v", resp, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("запросов %d, ожидалось 2 (один повтор)", calls.Load())
	}
}

func TestNoRetryOnClientError(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"error":{"message":"No endpoints found that support tool use"}}`, http.StatusNotFound)
	}))
	defer server.Close()

	_, err := testClient(server.URL).Complete(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "не поддерживает вызов инструментов") {
		t.Fatalf("ошибка %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("ошибку 404 не нужно повторять, запросов %d", calls.Load())
	}
}

func TestErrorInsideOKResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"error":{"message":"upstream provider error"}}`)
	}))
	defer server.Close()

	_, err := testClient(server.URL).Complete(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "upstream provider error") {
		t.Fatalf("ошибка %v", err)
	}
}
