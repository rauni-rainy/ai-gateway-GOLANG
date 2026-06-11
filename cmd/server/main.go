package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/rauni-rainy/ai-gateway/internal/cache"
	"github.com/rauni-rainy/ai-gateway/internal/config"
	"github.com/rauni-rainy/ai-gateway/internal/middleware"
	"github.com/rauni-rainy/ai-gateway/internal/proxy"
	"github.com/rauni-rainy/ai-gateway/internal/store"
)

func requestLogger() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			
			next.ServeHTTP(ww, r)
			
			slog.Info("HTTP Request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"latency", time.Since(start).String(),
				"req_id", chimiddleware.GetReqID(r.Context()),
				"ip", r.RemoteAddr,
			)
		})
	}
}

func main() {
	godotenv.Load()

	cfg := config.LoadConfig()

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	slog.Info("Initializing AI Gateway...", "config", cfg.String())

	// Init dependencies
	storeLayer, err := store.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer storeLayer.Close()

	cacheLayer, err := cache.New(cfg.RedisURL, time.Duration(cfg.CacheTTLSeconds)*time.Second)
	if err != nil {
		log.Fatalf("failed to initialize redis cache: %v", err)
	}

	// Build providers map
	providers := make(map[string]proxy.Provider)
	if cfg.GroqKey != "" {
		providers["groq"] = proxy.NewGroq(cfg.GroqKey)
	}
	if cfg.GeminiKey != "" {
		providers["gemini"] = proxy.NewGemini(cfg.GeminiKey)
	}

	rateLimiter := middleware.NewRateLimiter(cacheLayer.Client())
	budgetEnforcer := middleware.NewBudgetEnforcer(storeLayer, cacheLayer.Client())
	proxyHandler := proxy.NewHandler(cacheLayer, storeLayer, providers)

	r := chi.NewRouter()

	// Global Middleware
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(requestLogger())

	startTime := time.Now()

	// Public routes
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		dbStatus := "ok"
		if err := storeLayer.Ping(ctx); err != nil {
			dbStatus = fmt.Sprintf("error: %v", err)
		}

		redisStatus := "ok"
		if err := cacheLayer.Client().Ping(ctx).Err(); err != nil {
			redisStatus = fmt.Sprintf("error: %v", err)
		}

		status := "ok"
		if dbStatus != "ok" || redisStatus != "ok" {
			status = "degraded"
		}

		resp := map[string]interface{}{
			"status":         status,
			"postgres":       dbStatus,
			"redis":          redisStatus,
			"uptime_seconds": int(time.Since(startTime).Seconds()),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	// Protected routes group
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(storeLayer))
		r.Use(middleware.RateLimit(rateLimiter))
		r.Use(middleware.EnforceBudget(budgetEnforcer))
		r.Post("/v1/complete", proxyHandler.ServeHTTP)
	})

	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	// Graceful shutdown context
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("Server starting", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server listen error: %v", err)
		}
	}()

	// Block until a signal is received
	<-ctx.Done()

	slog.Info("Shutting down gracefully, press Ctrl+C again to force")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	slog.Info("Server exiting")
}
