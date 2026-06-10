package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
)

type Config struct {
	Port            string
	DatabaseURL     string
	RedisURL        string
	GroqKey         string
	GeminiKey       string
	DefaultRPS      int
	CacheTTLSeconds int
	LogLevel        string
}

func getEnvOrDefault(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}

func getEnvOrDefaultInt(key string, fallback int) int {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			return parsed
		}
	}
	return fallback
}

func maskKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return key[:4] + "****"
}

func (c *Config) String() string {
	return fmt.Sprintf(
		"Config{Port: %s, DatabaseURL: %s, RedisURL: %s, GroqKey: %s, GeminiKey: %s, DefaultRPS: %d, CacheTTLSeconds: %d, LogLevel: %s}",
		c.Port,
		c.DatabaseURL,
		c.RedisURL,
		maskKey(c.GroqKey),
		maskKey(c.GeminiKey),
		c.DefaultRPS,
		c.CacheTTLSeconds,
		c.LogLevel,
	)
}

func LoadConfig() *Config {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	groqKey := os.Getenv("GROQ_KEY")
	if groqKey == "" {
		log.Println("WARN: GROQ_KEY not set — Groq calls will fail")
	}

	geminiKey := os.Getenv("GEMINI_KEY")
	if geminiKey == "" {
		log.Println("WARN: GEMINI_KEY not set — Gemini calls will fail")
	}

	return &Config{
		Port:            getEnvOrDefault("PORT", "8080"),
		DatabaseURL:     dbURL,
		RedisURL:        getEnvOrDefault("REDIS_URL", "redis://localhost:6379"),
		GroqKey:         groqKey,
		GeminiKey:       geminiKey,
		DefaultRPS:      getEnvOrDefaultInt("DEFAULT_RPS", 10),
		CacheTTLSeconds: getEnvOrDefaultInt("CACHE_TTL_SECONDS", 3600),
		LogLevel:        getEnvOrDefault("LOG_LEVEL", "info"),
	}
}
