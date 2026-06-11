package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/redis/go-redis/v9"
)

type SpendChecker interface {
	GetDailySpend(ctx context.Context, apiKeyID string) (float64, error)
}

type BudgetEnforcer struct {
	store SpendChecker
	rdb   *redis.Client
}

func NewBudgetEnforcer(s SpendChecker, rdb *redis.Client) *BudgetEnforcer {
	return &BudgetEnforcer{
		store: s,
		rdb:   rdb,
	}
}

type BudgetExceededError struct {
	Spent    float64
	Limit    float64
	ResetsAt time.Time
}

func (e BudgetExceededError) Error() string {
	return fmt.Sprintf("daily budget exceeded: spent %f, limit %f", e.Spent, e.Limit)
}

func (b *BudgetEnforcer) CheckBudget(ctx context.Context, apiKey *models.APIKey) error {
	if apiKey.DailyBudgetUSD == nil {
		return nil // Unlimited budget
	}

	today := time.Now().UTC().Format("2006-01-02")
	cacheKey := fmt.Sprintf("budget:%s:%s", apiKey.ID, today)

	var currentSpend float64

	// Try cache
	val, err := b.rdb.Get(ctx, cacheKey).Result()
	if err == nil && val != "" {
		if parsed, parseErr := strconv.ParseFloat(val, 64); parseErr == nil {
			currentSpend = parsed
		}
	} else {
		// Cache miss
		spent, err := b.store.GetDailySpend(ctx, apiKey.ID)
		if err != nil {
			return fmt.Errorf("failed to get daily spend: %w", err)
		}
		currentSpend = spent

		// Cache for 60 seconds
		b.rdb.Set(ctx, cacheKey, strconv.FormatFloat(currentSpend, 'f', -1, 64), 60*time.Second)
	}

	if currentSpend >= *apiKey.DailyBudgetUSD {
		now := time.Now().UTC()
		tomorrowMidnightUTC := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		return BudgetExceededError{
			Spent:    currentSpend,
			Limit:    *apiKey.DailyBudgetUSD,
			ResetsAt: tomorrowMidnightUTC,
		}
	}

	return nil
}

func EnforceBudget(enforcer *BudgetEnforcer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey, ok := APIKeyFromContext(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			err := enforcer.CheckBudget(r.Context(), apiKey)
			if err != nil {
				if bErr, isBudgetErr := err.(BudgetExceededError); isBudgetErr {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusPaymentRequired) // 402
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error":      "daily budget exceeded",
						"spent_usd":  bErr.Spent,
						"limit_usd":  bErr.Limit,
						"resets_at":  bErr.ResetsAt.Format(time.RFC3339),
					})
					return
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "failed to check budget"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
