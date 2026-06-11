package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/rauni-rainy/ai-gateway/internal/cache"
	"github.com/rauni-rainy/ai-gateway/internal/middleware"
	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/rauni-rainy/ai-gateway/internal/store"
)

type Handler struct {
	providers map[string]Provider
	cache     *cache.Cache
	store     store.LogInserter
}

func NewHandler(c *cache.Cache, s store.LogInserter, providers map[string]Provider) *Handler {
	return &Handler{
		cache:     c,
		store:     s,
		providers: providers,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req models.GatewayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := req.Validate(); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Safe because Auth middleware guarantees it is populated
	apiKey, _ := middleware.APIKeyFromContext(r.Context())

	// 1. Check Cache with Stampede Protection
	cached, isOwner, err := h.cache.GetOrLock(r.Context(), &req)
	if err != nil {
		// If lock wait times out or pubsub fails, just fall back to making the provider call directly
		fmt.Printf("cache get or lock error: %v\n", err)
		isOwner = true // act as owner to ensure the user gets a response
	}
	if cached != nil {
		// Subscriber got the response via pubsub
		w.Header().Set("X-Cache", "HIT")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached)
		h.logAsync(apiKey.ID, cached, http.StatusOK)
		return
	}

	// 2. Lookup Provider
	provider, ok := h.providers[req.Provider]
	if !ok {
		writeError(w, fmt.Sprintf("unknown provider: %s", req.Provider), http.StatusBadRequest)
		return
	}

	// 3. Execute Request (only the owner gets here)
	start := time.Now()
	resp, err := provider.Complete(r.Context(), &req)
	if err != nil {
		pErr, isProviderErr := IsProviderError(err)
		if isProviderErr {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(pErr.StatusCode)
			json.NewEncoder(w).Encode(map[string]string{"error": pErr.Message})
			return
		}
		writeError(w, "provider request failed", http.StatusBadGateway)
		return
	}

	// 4. Decorate Response
	resp.ID = uuid.New().String()
	resp.LatencyMS = time.Since(start).Milliseconds()

	// 5. Async background tasks
	if isOwner {
		// Background context because request context may cancel before publish completes
		go func() {
			if err := h.cache.PublishResult(context.Background(), &req, resp); err != nil {
				fmt.Printf("cache publish result error: %v\n", err)
			}
		}()
	}

	h.logAsync(apiKey.ID, resp, http.StatusOK)

	// 6. Return payload
	w.Header().Set("X-Cache", "MISS")
	w.Header().Set("X-Latency-Ms", strconv.FormatInt(resp.LatencyMS, 10))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) logAsync(apiKeyID string, resp *models.GatewayResponse, status int) {
	go func() {
		// Enforce a hard 5-second timeout on DB inserts to prevent background goroutine leaks
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		log := &models.RequestLog{
			ID:               uuid.New().String(),
			APIKeyID:         apiKeyID,
			Provider:         resp.Provider,
			Model:            resp.Model,
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			LatencyMS:        resp.LatencyMS,
			Cached:           resp.Cached,
			StatusCode:       status,
			CostUSD:          resp.CostUSD,
			CreatedAt:        time.Now(),
		}

		if err := h.store.InsertLog(ctx, log); err != nil {
			fmt.Printf("failed to insert request log: %v\n", err)
		}
	}()
}

func writeError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
