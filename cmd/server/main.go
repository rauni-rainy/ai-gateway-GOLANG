package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/joho/godotenv"
	"github.com/rauni-rainy/ai-gateway/internal/config"
)

type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	resp := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().Format(time.RFC3339),
	}

	json.NewEncoder(w).Encode(resp)
}

func main() {
	_ = godotenv.Load() // Load .env file if it exists, ignore error if missing

	cfg := config.LoadConfig()
	log.Printf("Loaded config: %s", cfg.String())

	http.HandleFunc("/health", healthHandler)

	addr := ":" + cfg.Port
	log.Printf("AI Gateway starting on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
