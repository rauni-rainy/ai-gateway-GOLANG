package proxy_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rauni-rainy/ai-gateway/internal/cache"
	"github.com/rauni-rainy/ai-gateway/internal/middleware"
	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/rauni-rainy/ai-gateway/internal/proxy"
	"github.com/rauni-rainy/ai-gateway/internal/store"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestGatewayE2E(t *testing.T) {
	ctx := context.Background()

	// 1. Spin up real PostgreSQL container
	postgresContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "postgres:16-alpine",
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_DB":       "testdb",
				"POSTGRES_USER":     "user",
				"POSTGRES_PASSWORD": "password",
			},
			WaitingFor: wait.ForListeningPort("5432/tcp"),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	defer postgresContainer.Terminate(ctx)

	pgHost, _ := postgresContainer.Host(ctx)
	pgPort, _ := postgresContainer.MappedPort(ctx, "5432")
	dbURL := fmt.Sprintf("postgres://user:password@%s:%s/testdb?sslmode=disable", pgHost, pgPort.Port())

	// 2. Spin up real Redis container
	redisContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForListeningPort("6379/tcp"),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("failed to start redis container: %v", err)
	}
	defer redisContainer.Terminate(ctx)

	redisHost, _ := redisContainer.Host(ctx)
	redisPort, _ := redisContainer.MappedPort(ctx, "6379")
	redisURL := fmt.Sprintf("redis://%s:%s", redisHost, redisPort.Port())

	// 3. Connect layers
	storeLayer, err := store.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("failed to connect to db: %v", err)
	}
	defer storeLayer.Close()

	cacheLayer, err := cache.New(redisURL, time.Minute)
	if err != nil {
		t.Fatalf("failed to connect to redis: %v", err)
	}

	// 4. Run Migration
	schemaBytes, err := os.ReadFile("../../migrations/001_schema.sql")
	if err != nil {
		t.Fatalf("failed to read migration file: %v", err)
	}
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("failed to connect to run migrations: %v", err)
	}
	if _, err := conn.Exec(ctx, string(schemaBytes)); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}
	
	// 5. Insert test API key (limit 20 RPS)
	keyHash := fmt.Sprintf("%x", sha256.Sum256([]byte("test-key-e2e")))
	apiKeyID := uuid.New().String()
	_, err = conn.Exec(ctx, `
		INSERT INTO api_keys (id, key_hash, name, rate_limit_rps, is_active) 
		VALUES ($1, $2, 'E2E Test Key', 20, TRUE)`, 
		apiKeyID, keyHash)
	if err != nil {
		t.Fatalf("failed to insert test key: %v", err)
	}
	conn.Close(ctx)

	// 6. Build mock provider for E2E routing
	mockProv := &MockProvider{
		CompleteFunc: func(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
			return &models.GatewayResponse{
				Provider: req.Provider,
				Model:    req.Model,
				Content:  "e2e fixed response",
			}, nil
		},
	}
	providers := map[string]proxy.Provider{
		"mock": mockProv,
	}

	rateLimiter := middleware.NewRateLimiter(cacheLayer.Client())
	proxyHandler := proxy.NewHandler(cacheLayer, nil, storeLayer, providers)

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(storeLayer))
		r.Use(middleware.RateLimit(rateLimiter))
		r.Post("/v1/complete", proxyHandler.ServeHTTP)
	})

	server := httptest.NewServer(r)
	defer server.Close()

	sendReq := func(token string, body map[string]interface{}) *http.Response {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/complete", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("http request failed: %v", err)
		}
		return resp
	}

	// 7. Initial request (Cache Miss)
	reqBody := map[string]interface{}{
		"provider":   "mock",
		"model":      "test-model",
		"max_tokens": 100,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	}
	resp1 := sendReq("test-key-e2e", reqBody)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on first request, got %d", resp1.StatusCode)
	}
	if resp1.Header.Get("X-Cache") != "MISS" {
		t.Errorf("expected X-Cache MISS, got %s", resp1.Header.Get("X-Cache"))
	}
	var gwResp models.GatewayResponse
	json.NewDecoder(resp1.Body).Decode(&gwResp)
	resp1.Body.Close()
	if gwResp.Content != "e2e fixed response" {
		t.Errorf("expected fixed response, got %s", gwResp.Content)
	}

	// Wait briefly for background cache.Set
	time.Sleep(100 * time.Millisecond)

	// 8. Identical request (Cache Hit)
	start := time.Now()
	resp2 := sendReq("test-key-e2e", reqBody)
	latency := time.Since(start).Milliseconds()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on second request, got %d", resp2.StatusCode)
	}
	if resp2.Header.Get("X-Cache") != "HIT" {
		t.Errorf("expected X-Cache HIT, got %s", resp2.Header.Get("X-Cache"))
	}
	resp2.Body.Close()
	
	// Running inside virtualization sometimes adds a few ms overhead, 
	// but generally should be < 5ms for an instant cache hit
	if latency >= 10 {
		t.Logf("Warning: cache hit latency was %dms", latency)
	}

	// 9. Rate Limit Test (25 rapid requests, limit is 20)
	var tooManyReqs bool
	for i := 0; i < 25; i++ {
		resp := sendReq("test-key-e2e", map[string]interface{}{
			"provider":   "mock",
			"model":      "test-model-spam", // bust cache to guarantee DB/rate limit hits
			"max_tokens": 100,
			"messages":   []map[string]string{{"role": "user", "content": fmt.Sprintf("msg %d", i)}},
		})
		if resp.StatusCode == http.StatusTooManyRequests {
			tooManyReqs = true
			if resp.Header.Get("Retry-After") != "1" {
				t.Errorf("expected Retry-After 1, got %s", resp.Header.Get("Retry-After"))
			}
		}
		resp.Body.Close()
	}
	if !tooManyReqs {
		t.Errorf("expected to hit rate limit and get 429")
	}

	// 10. Bad API key
	respBadKey := sendReq("wrong-key", reqBody)
	if respBadKey.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad key, got %d", respBadKey.StatusCode)
	}
	respBadKey.Body.Close()

	// 11. Unknown provider
	respBadProv := sendReq("test-key-e2e", map[string]interface{}{
		"provider":   "unknown_prov",
		"model":      "test",
		"max_tokens": 100,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	})
	if respBadProv.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown provider, got %d", respBadProv.StatusCode)
	}
	respBadProv.Body.Close()
}
