package middleware_test

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/rauni-rainy/ai-gateway/internal/middleware"
	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/redis/go-redis/v9"
)

func TestRateLimiter_Allow(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer s.Close()

	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	limiter := middleware.NewRateLimiter(rdb)

	apiKey := &models.APIKey{
		ID:           "test-api-key-123",
		RateLimitRPS: 5,
	}

	ctx := context.Background()

	// Make 10 rapid requests. Limit is 5 RPS.
	// Since these execute in under a millisecond, the first 5 should pass, 6-10 should fail.
	for i := 1; i <= 10; i++ {
		allowed, remaining, err := limiter.Allow(ctx, apiKey)
		if err != nil {
			t.Fatalf("unexpected error on request %d: %v", i, err)
		}

		if i <= 5 {
			if !allowed {
				t.Errorf("request %d should be allowed", i)
			}
			expectedRemaining := 5 - i
			if remaining != expectedRemaining {
				t.Errorf("request %d: expected remaining %d, got %d", i, expectedRemaining, remaining)
			}
		} else {
			if allowed {
				t.Errorf("request %d should not be allowed", i)
			}
			if remaining != 0 {
				t.Errorf("request %d: expected remaining 0, got %d", i, remaining)
			}
		}
	}
}
