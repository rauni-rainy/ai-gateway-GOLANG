//go:build ignore

package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
	"github.com/rauni-rainy/ai-gateway/internal/models"
)

func main() {
	godotenv.Load(".env")
	dbURL := os.Getenv("DATABASE_URL")
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		panic(err)
	}

	h := sha256.Sum256([]byte("my-admin-secret"))
	keyHash := fmt.Sprintf("%x", h)

	var key models.APIKey
	err = conn.QueryRow(ctx, `
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
		fmt.Printf("SCAN ERROR: %v\n", err)
	} else {
		fmt.Printf("SUCCESS: %+v\n", key)
	}
}
