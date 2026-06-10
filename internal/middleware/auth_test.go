package middleware_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rauni-rainy/ai-gateway/internal/middleware"
	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/rauni-rainy/ai-gateway/internal/store"
)

func TestAuth(t *testing.T) {
	tests := []struct {
		name           string
		authHeader     string
		mockStore      *store.MockStore
		expectedStatus int
		verifyKeyName  string
	}{
		{
			name:           "missing Authorization header",
			authHeader:     "",
			mockStore:      &store.MockStore{},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Token xyz prefix (not Bearer)",
			authHeader:     "Token xyz",
			mockStore:      &store.MockStore{},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:       "valid key lookup returns APIKey",
			authHeader: "Bearer valid-key",
			mockStore: &store.MockStore{
				GetAPIKeyResult: &models.APIKey{Name: "TestKey"},
			},
			expectedStatus: http.StatusOK,
			verifyKeyName:  "TestKey",
		},
		{
			name:       "store.ErrKeyNotFound",
			authHeader: "Bearer invalid-key",
			mockStore: &store.MockStore{
				GetAPIKeyErr: store.ErrKeyNotFound,
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:       "store returns unexpected error",
			authHeader: "Bearer valid-key",
			mockStore: &store.MockStore{
				GetAPIKeyErr: errors.New("db error"),
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A simple next handler to verify context injection
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				apiKey, ok := middleware.APIKeyFromContext(r.Context())
				if !ok || apiKey == nil {
					t.Errorf("expected API key in context")
				} else if apiKey.Name != tt.verifyKeyName {
					t.Errorf("expected API key name %q in context, got %q", tt.verifyKeyName, apiKey.Name)
				}
				w.WriteHeader(http.StatusOK)
			})

			// Create middleware
			mw := middleware.Auth(tt.mockStore)
			handler := mw(nextHandler)

			// Execute request
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			// Assert Status
			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rec.Code)
			}

			// Assert Headers for success case
			if tt.expectedStatus == http.StatusOK {
				gotHeader := rec.Header().Get("X-API-Key-Name")
				if gotHeader != tt.verifyKeyName {
					t.Errorf("expected X-API-Key-Name %q, got %q", tt.verifyKeyName, gotHeader)
				}
			}
		})
	}
}
