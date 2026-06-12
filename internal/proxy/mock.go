package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/rauni-rainy/ai-gateway/internal/models"
)

type MockProvider struct{}

func NewMock() *MockProvider {
	return &MockProvider{}
}

func (p *MockProvider) Complete(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
	// Simulate LLM generation latency (200ms)
	select {
	case <-time.After(200 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return &models.GatewayResponse{
		ID:       uuid.New().String(),
		Provider: "mock",
		Model:    req.Model,
		Content:  "This is a mocked response to test high concurrency without hitting rate limits!",
		Usage: models.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
		CostUSD: 0.0001,
	}, nil
}

func (p *MockProvider) CompleteStream(ctx context.Context, req *models.GatewayRequest, w http.ResponseWriter) (*models.Usage, error) {
	// Not implemented for mock
	return nil, nil
}
