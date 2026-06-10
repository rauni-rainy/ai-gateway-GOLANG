package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/redis/go-redis/v9"
)

type RateLimiter struct {
	rdb *redis.Client
}

func NewRateLimiter(rdb *redis.Client) *RateLimiter {
	return &RateLimiter{rdb: rdb}
}

func (l *RateLimiter) Allow(ctx context.Context, apiKey *models.APIKey) (allowed bool, remaining int, err error) {
	key := "ratelimit:" + apiKey.ID
	now := time.Now().UnixMilli()
	windowMs := int64(1000) // 1-second sliding window

	// Generate a unique member for this specific request
	member := uuid.New().String()

	// Use a transaction pipeline for atomic execution
	pipe := l.rdb.TxPipeline()
	
	// 1. Remove expired entries (older than now - 1 second)
	pipe.ZRemRangeByScore(ctx, key, "0", strconv.FormatInt(now-windowMs, 10))
	
	// 2. Count remaining valid entries BEFORE adding this new one
	countCmd := pipe.ZCard(ctx, key)
	
	// 3. Add this current request
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: member})
	
	// 4. Set TTL to 2 seconds so the key eventually cleans itself up when idle
	pipe.Expire(ctx, key, 2*time.Second)

	if _, err := pipe.Exec(ctx); err != nil {
		return false, 0, fmt.Errorf("redis pipeline error: %w", err)
	}

	count := countCmd.Val()
	allowed = count < int64(apiKey.RateLimitRPS)

	if !allowed {
		// If we exceeded the rate limit, we must remove the request we just added.
		// Otherwise, rejected requests would count against the user's limit, essentially 
		// locking them out indefinitely if they keep spamming.
		if err := l.rdb.ZRem(ctx, key, member).Err(); err != nil {
			return false, 0, fmt.Errorf("redis zrem error: %w", err)
		}
		remaining = 0
	} else {
		// Remaining is Limit - (count of old requests + 1 for the current request)
		rem := int(int64(apiKey.RateLimitRPS) - count - 1)
		if rem < 0 {
			rem = 0
		}
		remaining = rem
	}

	return allowed, remaining, nil
}

func RateLimit(limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey, ok := APIKeyFromContext(r.Context())
			if !ok {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(apiKey.RateLimitRPS))

			allowed, remaining, err := limiter.Allow(r.Context(), apiKey)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"internal server error"}`))
				return
			}

			if !allowed {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("Retry-After", "1")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"rate limit exceeded","retry_after_seconds":1}`))
				return
			}

			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			next.ServeHTTP(w, r)
		})
	}
}
