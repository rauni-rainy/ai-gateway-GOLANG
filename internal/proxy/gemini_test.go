package proxy_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/rauni-rainy/ai-gateway/internal/proxy"
)

func TestGeminiProvider_Complete(t *testing.T) {
	t.Run("success case", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("key") != "test-api-key" {
				t.Errorf("expected key=test-api-key, got %s", r.URL.Query().Get("key"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"candidates": [{"content": {"parts": [{"text": "hello from gemini"}]}}],
				"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 20}
			}`))
		}))
		defer ts.Close()

		p := proxy.NewGemini("test-api-key")
		p.BaseURL = ts.URL

		req := &models.GatewayRequest{Model: "gemini-1.5-flash", Messages: []models.Message{{Role: "user", Content: "hi"}}}
		resp, err := p.Complete(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resp.Content != "hello from gemini" {
			t.Errorf("got %s, want 'hello from gemini'", resp.Content)
		}
		if resp.Usage.PromptTokens != 10 {
			t.Errorf("got %d prompt tokens, want 10", resp.Usage.PromptTokens)
		}
	})

	t.Run("500 from provider -> ProviderError", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":{"message":"internal gemini error"}}`))
		}))
		defer ts.Close()

		p := proxy.NewGemini("test-api-key")
		p.BaseURL = ts.URL

		req := &models.GatewayRequest{Model: "gemini-1.5-flash", Messages: []models.Message{{Role: "user", Content: "hi"}}}
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
		if pErr.Message != "internal gemini error" {
			t.Errorf("got message %q, want 'internal gemini error'", pErr.Message)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{bad json`))
		}))
		defer ts.Close()

		p := proxy.NewGemini("test-api-key")
		p.BaseURL = ts.URL

		req := &models.GatewayRequest{Model: "gemini-1.5-flash", Messages: []models.Message{{Role: "user", Content: "hi"}}}
		_, err := p.Complete(context.Background(), req)
		if err == nil {
			t.Fatal("expected error on malformed JSON, got nil")
		}
	})
}

func TestProviderRoleMapping(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		
		var rawBody map[string]interface{}
		json.Unmarshal(bodyBytes, &rawBody)

		contents := rawBody["contents"].([]interface{})
		
		// We expect the second message (originally 'assistant') to be mapped to 'model'
		secondMsg := contents[1].(map[string]interface{})
		if secondMsg["role"] != "model" {
			t.Errorf("expected role 'model', got '%v'", secondMsg["role"])
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"candidates": [{"content": {"parts": [{"text": "ok"}]}}],
			"usageMetadata": {"promptTokenCount": 1, "candidatesTokenCount": 1}
		}`))
	}))
	defer ts.Close()

	p := proxy.NewGemini("test-key")
	p.BaseURL = ts.URL

	req := &models.GatewayRequest{
		Model: "gemini-1.5-flash",
		Messages: []models.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
	}

	_, err := p.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
