package store

import (
	"context"

	"github.com/rauni-rainy/ai-gateway/internal/models"
)

type MockStore struct {
	GetAPIKeyResult *models.APIKey
	GetAPIKeyErr    error
	InsertLogErr    error
	InsertLogCalled int
}

func (m *MockStore) GetAPIKey(ctx context.Context, rawKey string) (*models.APIKey, error) {
	return m.GetAPIKeyResult, m.GetAPIKeyErr
}

func (m *MockStore) InsertLog(ctx context.Context, log *models.RequestLog) error {
	m.InsertLogCalled++
	return m.InsertLogErr
}
