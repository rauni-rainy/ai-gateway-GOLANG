package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rauni-rainy/ai-gateway/internal/models"
)

var ErrKeyNotFound = errors.New("api key not found")

type APIKeyGetter interface {
	GetAPIKey(ctx context.Context, rawKey string) (*models.APIKey, error)
}

type LogInserter interface {
	InsertLog(ctx context.Context, log *models.RequestLog) error
}

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, connString string) (*Store, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse connection string: %w", err)
	}

	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = 1 * time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &Store{pool: pool}, nil
}

func (s *Store) GetAPIKey(ctx context.Context, rawKey string) (*models.APIKey, error) {
	h := sha256.Sum256([]byte(rawKey))
	keyHash := fmt.Sprintf("%x", h)

	var key models.APIKey
	err := s.pool.QueryRow(ctx, `
		SELECT id, key_hash, name, rate_limit_rps, daily_budget_usd, is_active, created_at
		FROM api_keys
		WHERE key_hash = $1 AND is_active = TRUE
	`, keyHash).Scan(
		&key.ID,
		&key.KeyHash,
		&key.Name,
		&key.RateLimitRPS,
		&key.DailyBudgetUSD,
		&key.IsActive,
		&key.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrKeyNotFound
		}
		return nil, fmt.Errorf("failed to query api key: %w", err)
	}

	return &key, nil
}

func (s *Store) InsertLog(ctx context.Context, log *models.RequestLog) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO request_logs (
			api_key_id, provider, model, prompt_tokens, completion_tokens, 
			latency_ms, cached, status_code, cost_usd, error_message
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		log.APIKeyID, log.Provider, log.Model, log.PromptTokens, log.CompletionTokens,
		log.LatencyMS, log.Cached, log.StatusCode, log.CostUSD, log.ErrorMessage,
	)
	if err != nil {
		return fmt.Errorf("failed to insert request log: %w", err)
	}
	return nil
}

func (s *Store) GetDailySpend(ctx context.Context, apiKeyID string) (float64, error) {
	// 1. Try querying the fast materialized view first
	var mvTotal *float64
	err := s.pool.QueryRow(ctx, `
		SELECT total_usd
		FROM daily_spend_mv
		WHERE api_key_id = $1 AND day = DATE(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')
	`, apiKeyID).Scan(&mvTotal)

	// If the view hasn't been refreshed for today yet or has no rows for this key, 
	// fallback to the direct aggregation query.
	if err != nil || mvTotal == nil {
		var total *float64
		fallbackErr := s.pool.QueryRow(ctx, `
			SELECT SUM(cost_usd)
			FROM request_logs
			WHERE api_key_id = $1 AND created_at >= DATE(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')
		`, apiKeyID).Scan(&total)

		if fallbackErr != nil {
			return 0, fmt.Errorf("failed to calculate daily spend (fallback): %w", fallbackErr)
		}

		if total == nil {
			return 0, nil
		}
		return *total, nil
	}

	return *mvTotal, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

