//go:build ignore

package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load(".env")
	dbURL := os.Getenv("DATABASE_URL")

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Printf("Unable to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	keyHash := fmt.Sprintf("%x", sha256.Sum256([]byte("my-admin-secret")))
	apiKeyID := uuid.New().String()

	_, err = conn.Exec(ctx, `
		INSERT INTO api_keys (id, key_hash, name, rate_limit_rps, daily_budget_usd, is_active, is_admin) 
		VALUES ($1, $2, 'Master Admin', 1000, 50.00, true, true)
		ON CONFLICT DO NOTHING`,
		apiKeyID, keyHash)
	
	if err != nil {
		fmt.Printf("Error inserting key: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully seeded 'my-admin-secret' into the database!")
}
