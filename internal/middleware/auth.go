package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/rauni-rainy/ai-gateway/internal/store"
)

// We define a private unexported type for the context key.
// If we used a plain string like "api_key", any other package in our project
// (or a third-party dependency) could accidentally also use context.WithValue(ctx, "api_key", something_else).
// Because they are both the exact same string type, the other package would overwrite our API key,
// leading to silent bugs, panics from failed type assertions, or security vulnerabilities.
// By defining an unexported type, it becomes mathematically impossible for any other package
// to create a key that collides with this one, even if they use the identical string value "api_key".
type contextKey string

const apiKeyCtxKey contextKey = "api_key"

func APIKeyFromContext(ctx context.Context) (*models.APIKey, bool) {
	key, ok := ctx.Value(apiKeyCtxKey).(*models.APIKey)
	return key, ok
}

func Auth(getter store.APIKeyGetter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"missing or invalid authorization header"}`))
				return
			}

			rawKey := strings.TrimPrefix(authHeader, "Bearer ")

			apiKey, err := getter.GetAPIKey(r.Context(), rawKey)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				if errors.Is(err, store.ErrKeyNotFound) {
					w.WriteHeader(http.StatusUnauthorized)
					w.Write([]byte(`{"error":"invalid api key"}`))
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"internal server error"}`))
				return
			}

			ctx := context.WithValue(r.Context(), apiKeyCtxKey, apiKey)
			w.Header().Set("X-API-Key-Name", apiKey.Name)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
