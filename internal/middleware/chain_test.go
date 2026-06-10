package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/rauni-rainy/ai-gateway/internal/middleware"
	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/rauni-rainy/ai-gateway/internal/store"
	"github.com/redis/go-redis/v9"
)

// dynamicMockStore returns ErrKeyNotFound for "invalid-key" and a valid key for anything else.
type dynamicMockStore struct {
	store.MockStore
	apiKey *models.APIKey
}

func (m *dynamicMockStore) GetAPIKey(ctx context.Context, rawKey string) (*models.APIKey, error) {
	if rawKey == "invalid-key" {
		return nil, store.ErrKeyNotFound
	}
	return m.apiKey, nil
}

func TestMiddlewareChain(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer s.Close()

	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	rateLimiter := middleware.NewRateLimiter(rdb)

	apiKey := &models.APIKey{
		ID:           "test-key-chain",
		Name:         "ChainTestKey",
		RateLimitRPS: 10,
	}

	mockStore := &dynamicMockStore{
		apiKey: apiKey,
	}

	r := chi.NewRouter()
	r.Use(middleware.Auth(mockStore))
	r.Use(middleware.RateLimit(rateLimiter))

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})

	t.Run("missing auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("invalid key", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer invalid-key")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("first request passes", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer valid-key")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d. body: %s", rec.Code, rec.Body.String())
		}
		if rem := rec.Header().Get("X-RateLimit-Remaining"); rem != "9" {
			t.Errorf("expected X-RateLimit-Remaining 9, got %s", rem)
		}
	})

	t.Run("rate limit breach", func(t *testing.T) {
		// We already made 1 valid request. Make 9 more to exhaust limit.
		for i := 0; i < 9; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Authorization", "Bearer valid-key")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200 on warmup request %d, got %d", i, rec.Code)
			}
		}

		// Now the 11th request in the same second window
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer valid-key")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusTooManyRequests { // 429
			t.Fatalf("expected 429, got %d", rec.Code)
		}
		if retry := rec.Header().Get("Retry-After"); retry != "1" {
			t.Errorf("expected Retry-After 1, got %s", retry)
		}
	})

	t.Run("rate resets after window", func(t *testing.T) {
		// Advance miniredis internal time to trigger TTLs, AND sleep actual time so `time.Now()` advances 
		// for the application's sliding window calculation logic.
		s.FastForward(1001 * time.Millisecond)
		time.Sleep(1001 * time.Millisecond)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer valid-key")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 after time advance, got %d", rec.Code)
		}
		if rem := rec.Header().Get("X-RateLimit-Remaining"); rem != "9" {
			t.Errorf("expected X-RateLimit-Remaining 9 after reset, got %s", rem)
		}
	})
}

func TestRateLimiterConcurrency(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer s.Close()

	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	rateLimiter := middleware.NewRateLimiter(rdb)

	apiKey := &models.APIKey{
		ID:           "test-concurrent-key",
		Name:         "ConcurrentTestKey",
		RateLimitRPS: 10,
	}

	mockStore := &dynamicMockStore{
		apiKey: apiKey,
	}

	r := chi.NewRouter()
	r.Use(middleware.Auth(mockStore))
	r.Use(middleware.RateLimit(rateLimiter))

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var wg sync.WaitGroup
	var allowedCount int32
	var rejectedCount int32

	totalRequests := 100
	wg.Add(totalRequests)

	for i := 0; i < totalRequests; i++ {
		go func() {
			defer wg.Done()

			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Authorization", "Bearer concurrent-key")
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code == http.StatusOK {
				atomic.AddInt32(&allowedCount, 1)
			} else if rec.Code == http.StatusTooManyRequests {
				atomic.AddInt32(&rejectedCount, 1)
			}
		}()
	}

	wg.Wait()

	if allowedCount != 10 {
		t.Errorf("expected exactly 10 requests to be allowed, got %d", allowedCount)
	}
	if rejectedCount != 90 {
		t.Errorf("expected exactly 90 requests to be rejected, got %d", rejectedCount)
	}
}
