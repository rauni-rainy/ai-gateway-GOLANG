package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/rauni-rainy/ai-gateway/internal/models"
)

type Provider interface {
	Complete(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error)
	CompleteStream(ctx context.Context, req *models.GatewayRequest, w http.ResponseWriter) (*models.Usage, error)
}

type ProviderError struct {
	StatusCode int
	Message    string
	Provider   string
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("provider %s returned %d: %s", e.Provider, e.StatusCode, e.Message)
}

func IsProviderError(err error) (*ProviderError, bool) {
	var pErr *ProviderError
	if errors.As(err, &pErr) {
		return pErr, true
	}
	return nil, false
}
