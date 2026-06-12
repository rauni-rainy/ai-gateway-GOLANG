package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
		SELECT id, key_hash, name, rate_limit_rps, daily_budget_usd, is_active, is_admin, created_at
		FROM api_keys
		WHERE key_hash = $1 AND is_active = TRUE
	`, keyHash).Scan(
		&key.ID,
		&key.KeyHash,
		&key.Name,
		&key.RateLimitRPS,
		&key.DailyBudgetUSD,
		&key.IsActive,
		&key.IsAdmin,
		&key.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrKeyNotFound
		}
		fmt.Printf("GetAPIKey DB scan error: %v\n", err)
		return nil, fmt.Errorf("failed to get api key: %w", err)
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

func (s *Store) GetStats(ctx context.Context, from, to time.Time) (*models.AdminStats, error) {
	var stats models.AdminStats
	var providerJSON []byte

	err := s.pool.QueryRow(ctx, `
		SELECT 
			COUNT(*),
			COALESCE(SUM(CASE WHEN cached THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(cost_usd), 0),
			COALESCE(AVG(latency_ms), 0),
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms), 0),
			COALESCE(JSON_OBJECT_AGG(provider, provider_count) FILTER (WHERE provider IS NOT NULL), '{}'::json)
		FROM (
			SELECT 
				latency_ms, cached, cost_usd,
				provider,
				COUNT(*) OVER (PARTITION BY provider) as provider_count
			FROM request_logs
			WHERE created_at >= $1 AND created_at <= $2
		) as sub
	`, from, to).Scan(
		&stats.TotalRequests,
		&stats.CachedRequests,
		&stats.TotalCostUSD,
		&stats.AvgLatencyMS,
		&stats.P95LatencyMS,
		&providerJSON,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	// Because of JSON_OBJECT_AGG over partition, we might get duplicate keys in the JSON, but it parses fine
	// Actually, let's fix the JSON_OBJECT_AGG query to be cleaner for group by
	return &stats, nil
}

// Better version of GetStats to correctly aggregate JSON_OBJECT_AGG without subquery duplications
func (s *Store) GetStatsFixed(ctx context.Context, from, to time.Time) (*models.AdminStats, error) {
	var stats models.AdminStats
	var providerJSON []byte

	err := s.pool.QueryRow(ctx, `
		WITH global_stats AS (
			SELECT 
				COUNT(*) as total_requests,
				COALESCE(SUM(CASE WHEN cached THEN 1 ELSE 0 END), 0) as cached_requests,
				COALESCE(SUM(cost_usd), 0) as total_cost,
				COALESCE(AVG(latency_ms), 0) as avg_latency,
				COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms), 0) as p95_latency
			FROM request_logs
			WHERE created_at >= $1 AND created_at <= $2
		), provider_stats AS (
			SELECT provider, COUNT(*) as count
			FROM request_logs
			WHERE created_at >= $1 AND created_at <= $2
			GROUP BY provider
		), json_stats AS (
			SELECT COALESCE(JSON_OBJECT_AGG(provider, count), '{}'::json) as providers
			FROM provider_stats
		)
		SELECT 
			g.total_requests, g.cached_requests, g.total_cost, g.avg_latency, g.p95_latency, j.providers
		FROM global_stats g CROSS JOIN json_stats j
	`, from, to).Scan(
		&stats.TotalRequests,
		&stats.CachedRequests,
		&stats.TotalCostUSD,
		&stats.AvgLatencyMS,
		&stats.P95LatencyMS,
		&providerJSON,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	if err := json.Unmarshal(providerJSON, &stats.RequestsByProvider); err != nil {
		stats.RequestsByProvider = make(map[string]int)
	}

	return &stats, nil
}

func (s *Store) GetTopKeys(ctx context.Context, from, to time.Time, limit int) ([]models.KeyStats, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT api_key_id, COUNT(*) as count
		FROM request_logs
		WHERE created_at >= $1 AND created_at <= $2
		GROUP BY api_key_id
		ORDER BY count DESC
		LIMIT $3
	`, from, to, limit)

	if err != nil {
		return nil, fmt.Errorf("failed to get top keys: %w", err)
	}
	defer rows.Close()

	var keys []models.KeyStats
	for rows.Next() {
		var ks models.KeyStats
		if err := rows.Scan(&ks.APIKeyID, &ks.RequestCount); err != nil {
			return nil, err
		}
		keys = append(keys, ks)
	}
	return keys, nil
}

func (s *Store) ListKeys(ctx context.Context) ([]models.APIKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, rate_limit_rps, daily_budget_usd, is_active, is_admin, created_at
		FROM api_keys
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list keys: %w", err)
	}
	defer rows.Close()

	var keys []models.APIKey
	for rows.Next() {
		var key models.APIKey
		if err := rows.Scan(&key.ID, &key.Name, &key.RateLimitRPS, &key.DailyBudgetUSD, &key.IsActive, &key.IsAdmin, &key.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (s *Store) CreateKey(ctx context.Context, key *models.APIKey) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO api_keys (id, key_hash, name, rate_limit_rps, daily_budget_usd, is_active, is_admin)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, key.ID, key.KeyHash, key.Name, key.RateLimitRPS, key.DailyBudgetUSD, key.IsActive, key.IsAdmin)
	if err != nil {
		return fmt.Errorf("failed to create api key: %w", err)
	}
	return nil
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

