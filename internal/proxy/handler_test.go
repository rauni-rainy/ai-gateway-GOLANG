package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/rauni-rainy/ai-gateway/internal/cache"
	"github.com/rauni-rainy/ai-gateway/internal/middleware"
	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/rauni-rainy/ai-gateway/internal/proxy"
	"github.com/redis/go-redis/v9"
)

type MockProvider struct {
	CompleteFunc func(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error)
}

func (m *MockProvider) Complete(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
	if m.CompleteFunc != nil {
		return m.CompleteFunc(ctx, req)
	}
	return &models.GatewayResponse{Provider: "groq", Content: "mocked"}, nil
}

type MockStore struct{}

func (m *MockStore) GetAPIKey(ctx context.Context, rawKey string) (*models.APIKey, error) {
	return &models.APIKey{ID: "test-key-id"}, nil
}

func (m *MockStore) InsertLog(ctx context.Context, log *models.RequestLog) error {
	return nil
}

func TestProxyHandler(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer s.Close()

	c, _ := cache.New("redis://"+s.Addr(), time.Minute)
	mockStore := &MockStore{}

	var providerCalled bool
	mockProv := &MockProvider{
		CompleteFunc: func(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
			providerCalled = true
			return &models.GatewayResponse{
				Provider: "groq",
				Content:  "fresh response",
			}, nil
		},
	}

	providers := map[string]proxy.Provider{
		"groq": mockProv,
	}

	handler := proxy.NewHandler(c, mockStore, providers)
	
	// Wrap in auth middleware so context has APIKey injected cleanly
	mw := middleware.Auth(mockStore)
	server := mw(handler)

	// Helper to send requests
	sendReq := func(body map[string]interface{}) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer valid")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		return rec
	}

	t.Run("cache miss calls provider and returns MISS", func(t *testing.T) {
		providerCalled = false
		rec := sendReq(map[string]interface{}{
			"provider": "groq",
			"model": "llama3",
			"messages": []map[string]string{{"role": "user", "content": "hello"}},
		})

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Cache") != "MISS" {
			t.Errorf("expected X-Cache: MISS")
		}
		if !providerCalled {
			t.Errorf("expected provider to be called on cache miss")
		}
		
		// Wait a bit for the fire-and-forget goroutine cache Set to complete
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("cache hit returns HIT and does not call provider", func(t *testing.T) {
		providerCalled = false
		rec := sendReq(map[string]interface{}{
			"provider": "groq",
			"model": "llama3",
			"messages": []map[string]string{{"role": "user", "content": "hello"}},
		})

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Cache") != "HIT" {
			t.Errorf("expected X-Cache: HIT")
		}
		if providerCalled {
			t.Errorf("expected provider NOT to be called on cache hit")
		}
	})

	t.Run("unknown provider -> 400", func(t *testing.T) {
		rec := sendReq(map[string]interface{}{
			"provider": "unknown",
			"model": "llama3",
			"messages": []map[string]string{{"role": "user", "content": "hello"}},
		})

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("provider ProviderError propagates exact status code", func(t *testing.T) {
		mockProv.CompleteFunc = func(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
			return nil, &proxy.ProviderError{StatusCode: 429, Message: "too many requests"}
		}

		rec := sendReq(map[string]interface{}{
			"provider": "groq",
			"model": "llama3-new", // different model to bust cache
			"messages": []map[string]string{{"role": "user", "content": "hello"}},
		})

		if rec.Code != 429 {
			t.Fatalf("expected 429, got %d", rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte("too many requests")) {
			t.Errorf("expected body to contain error message, got %s", rec.Body.String())
		}
	})

	t.Run("provider generic error -> 502", func(t *testing.T) {
		mockProv.CompleteFunc = func(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
			return nil, errors.New("connection reset by peer")
		}

		rec := sendReq(map[string]interface{}{
			"provider": "groq",
			"model": "llama3-fail", // different model to bust cache
			"messages": []map[string]string{{"role": "user", "content": "hello"}},
		})

		if rec.Code != http.StatusBadGateway {
			t.Fatalf("expected 502, got %d", rec.Code)
		}
	})
}
