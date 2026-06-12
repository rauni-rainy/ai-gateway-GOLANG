//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load(".env")
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Println("DATABASE_URL not found in .env")
		os.Exit(1)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Printf("Unable to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	files, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		fmt.Printf("Error finding migrations: %v\n", err)
		os.Exit(1)
	}

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			fmt.Printf("Error reading %s: %v\n", file, err)
			os.Exit(1)
		}

		fmt.Printf("Running migration: %s\n", file)
		_, err = conn.Exec(ctx, string(content))
		if err != nil {
			fmt.Printf("Error executing %s: %v\n", file, err)
			os.Exit(1)
		}
	}

	fmt.Println("All migrations completed successfully!")
}
