package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
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

func BenchmarkCacheHit(b *testing.B) {
	s, err := miniredis.Run()
	if err != nil {
		b.Fatalf("miniredis fail: %v", err)
	}
	defer s.Close()

	c, _ := cache.New("redis://"+s.Addr(), time.Minute)
	mockStore := &MockStore{}

	mockProv := &MockProvider{
		CompleteFunc: func(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
			panic("provider should not be called on cache hit")
		},
	}
	providers := map[string]proxy.Provider{"mock": mockProv}
	handler := proxy.NewHandler(c, nil, mockStore, providers)

	mw := middleware.Auth(mockStore)
	server := mw(handler)

	reqObj := models.GatewayRequest{
		Provider:  "mock",
		Model:     "llama3",
		MaxTokens: 100,
		Messages:  []models.Message{{Role: "user", Content: "bench hit"}},
	}
	respObj := &models.GatewayResponse{Provider: "mock", Content: "cached response"}
	c.Set(context.Background(), &reqObj, respObj)

	reqBytes, _ := json.Marshal(reqObj)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(reqBytes))
		req.Header.Set("Authorization", "Bearer valid")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("expected 200, got %d", rec.Code)
		}
	}
}

func BenchmarkCacheMiss(b *testing.B) {
	s, err := miniredis.Run()
	if err != nil {
		b.Fatalf("miniredis fail: %v", err)
	}
	defer s.Close()

	c, _ := cache.New("redis://"+s.Addr(), time.Minute)
	mockStore := &MockStore{}

	mockProv := &MockProvider{
		CompleteFunc: func(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
			return &models.GatewayResponse{Provider: "mock", Content: "fresh"}, nil
		},
	}
	providers := map[string]proxy.Provider{"mock": mockProv}
	handler := proxy.NewHandler(c, nil, mockStore, providers)

	mw := middleware.Auth(mockStore)
	server := mw(handler)

	reqObj := models.GatewayRequest{
		Provider:  "mock",
		Model:     "llama3",
		MaxTokens: 100,
		Messages:  []models.Message{{Role: "user", Content: "bench miss"}},
	}
	reqBytes, _ := json.Marshal(reqObj)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(reqBytes))
		req.Header.Set("Authorization", "Bearer valid")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("expected 200, got %d", rec.Code)
		}
	}
}

func BenchmarkRateLimiter(b *testing.B) {
	s, err := miniredis.Run()
	if err != nil {
		b.Fatalf("miniredis fail: %v", err)
	}
	defer s.Close()

	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	limiter := middleware.NewRateLimiter(rdb)

	apiKey := &models.APIKey{ID: "bench-key", RateLimitRPS: 10000000}
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _, err := limiter.Allow(ctx, apiKey)
		if err != nil {
			b.Fatalf("allow failed: %v", err)
		}
	}
}
