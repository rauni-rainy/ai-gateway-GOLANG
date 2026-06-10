package proxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/rauni-rainy/ai-gateway/internal/proxy"
)

func TestGroqProvider_Complete(t *testing.T) {
	t.Run("success case", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth := r.Header.Get("Authorization"); auth != "Bearer test-api-key" {
				t.Errorf("expected Bearer test-api-key, got %s", auth)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Return OpenAI/Groq compatible response format
			w.Write([]byte(`{
				"choices": [{"message": {"content": "hello from groq"}}],
				"usage": {"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}
			}`))
		}))
		defer ts.Close()

		p := proxy.NewGroq("test-api-key")
		p.BaseURL = ts.URL // Point to our mock server instead of real Groq

		req := &models.GatewayRequest{Model: "llama3", Messages: []models.Message{{Role: "user", Content: "hi"}}}
		resp, err := p.Complete(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resp.Content != "hello from groq" {
			t.Errorf("got %s, want 'hello from groq'", resp.Content)
		}
		if resp.Usage.PromptTokens != 10 {
			t.Errorf("got %d prompt tokens, want 10", resp.Usage.PromptTokens)
		}
	})

	t.Run("500 from provider -> ProviderError", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`internal provider error`))
		}))
		defer ts.Close()

		p := proxy.NewGroq("test-api-key")
		p.BaseURL = ts.URL

		req := &models.GatewayRequest{Model: "llama3", Messages: []models.Message{{Role: "user", Content: "hi"}}}
		_, err := p.Complete(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		pErr, ok := proxy.IsProviderError(err)
		if !ok {
			t.Fatalf("expected ProviderError, got %T: %v", err, err)
		}
		if pErr.StatusCode != http.StatusInternalServerError {
			t.Errorf("got status %d, want 500", pErr.StatusCode)
		}
		if pErr.Message != "internal provider error" {
			t.Errorf("got message %q, want 'internal provider error'", pErr.Message)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{bad json structure`))
		}))
		defer ts.Close()

		p := proxy.NewGroq("test-api-key")
		p.BaseURL = ts.URL

		req := &models.GatewayRequest{Model: "llama3", Messages: []models.Message{{Role: "user", Content: "hi"}}}
		_, err := p.Complete(context.Background(), req)
		if err == nil {
			t.Fatal("expected error on malformed JSON, got nil")
		}
	})
}
