package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/rauni-rainy/ai-gateway/internal/store"
)

type Handler struct {
	store *store.Store
}

func NewHandler(s *store.Store) *Handler {
	return &Handler{store: s}
}

func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	to := time.Now()
	from := to.Add(-24 * time.Hour)

	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		if parsed, err := time.Parse(time.RFC3339, fromStr); err == nil {
			from = parsed
		}
	}
	if toStr := r.URL.Query().Get("to"); toStr != "" {
		if parsed, err := time.Parse(time.RFC3339, toStr); err == nil {
			to = parsed
		}
	}

	stats, err := h.store.GetStatsFixed(r.Context(), from, to)
	if err != nil {
		writeError(w, "failed to get stats", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (h *Handler) ListKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.store.ListKeys(r.Context())
	if err != nil {
		writeError(w, "failed to list keys", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(keys)
}

type createKeyRequest struct {
	Name           string   `json:"name"`
	RateLimitRPS   int      `json:"rate_limit_rps"`
	DailyBudgetUSD *float64 `json:"daily_budget_usd"`
	IsAdmin        bool     `json:"is_admin"`
}

func (h *Handler) CreateKey(w http.ResponseWriter, r *http.Request) {
	var req createKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Generate raw key
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		writeError(w, "failed to generate key", http.StatusInternalServerError)
		return
	}
	rawKey := "gw_" + hex.EncodeToString(bytes)

	// Hash it
	hash := sha256.Sum256([]byte(rawKey))
	keyHash := fmt.Sprintf("%x", hash)

	apiKey := &models.APIKey{
		ID:             uuid.New().String(),
		KeyHash:        keyHash,
		Name:           req.Name,
		RateLimitRPS:   req.RateLimitRPS,
		DailyBudgetUSD: req.DailyBudgetUSD,
		IsActive:       true,
		IsAdmin:        req.IsAdmin,
	}

	if err := h.store.CreateKey(r.Context(), apiKey); err != nil {
		writeError(w, "failed to create api key", http.StatusInternalServerError)
		return
	}

	resp := map[string]interface{}{
		"key":    rawKey,
		"key_id": apiKey.ID,
		"name":   apiKey.Name,
	}

	w.Header().Set("X-Warning", "Store this key securely - it cannot be retrieved again")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

func writeError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
