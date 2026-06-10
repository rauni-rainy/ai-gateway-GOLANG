package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/rauni-rainy/ai-gateway/internal/cache"
	"github.com/rauni-rainy/ai-gateway/internal/models"
)

// Define RequestHasher interface for testing boundaries
type RequestHasher interface {
	RequestHash(req *models.GatewayRequest) string
}

// MockRequestHasher satisfies the RequestHasher interface for future middleware tests
type MockRequestHasher struct {
	HashToReturn string
}

func (m *MockRequestHasher) RequestHash(req *models.GatewayRequest) string {
	return m.HashToReturn
}

func TestRequestHash(t *testing.T) {
	c := &cache.Cache{} // Instantiating just to call the method

	reqBase := &models.GatewayRequest{
		Provider: "groq",
		Model:    "llama3",
		Messages: []models.Message{{Role: "user", Content: "hello"}},
	}
	hashBase := c.RequestHash(reqBase)

	tests := []struct {
		name     string
		req      *models.GatewayRequest
		wantSame bool
	}{
		{
			name: "same request twice -> same hash",
			req: &models.GatewayRequest{
				Provider: "groq",
				Model:    "llama3",
				Messages: []models.Message{{Role: "user", Content: "hello"}},
			},
			wantSame: true,
		},
		{
			name: "different provider -> different hash",
			req: &models.GatewayRequest{
				Provider: "gemini",
				Model:    "llama3",
				Messages: []models.Message{{Role: "user", Content: "hello"}},
			},
			wantSame: false,
		},
		{
			name: "different messages -> different hash",
			req: &models.GatewayRequest{
				Provider: "groq",
				Model:    "llama3",
				Messages: []models.Message{{Role: "user", Content: "hi"}},
			},
			wantSame: false,
		},
		{
			name: "temperature 0.0 vs not set -> same hash",
			req: &models.GatewayRequest{
				Provider:    "groq",
				Model:       "llama3",
				Messages:    []models.Message{{Role: "user", Content: "hello"}},
				Temperature: 0.0, // Same as zero value in reqBase
			},
			wantSame: true,
		},
		{
			name: "adding a message -> different hash",
			req: &models.GatewayRequest{
				Provider: "groq",
				Model:    "llama3",
				Messages: []models.Message{
					{Role: "user", Content: "hello"},
					{Role: "assistant", Content: "hi"},
				},
			},
			wantSame: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.RequestHash(tt.req)
			isSame := got == hashBase
			if isSame != tt.wantSame {
				t.Errorf("got same=%v, want same=%v (hash1=%s, hash2=%s)", isSame, tt.wantSame, hashBase, got)
			}
		})
	}
}

func TestCache_GetMiss(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer s.Close()

	c, err := cache.New("redis://"+s.Addr(), time.Minute)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	req := &models.GatewayRequest{
		Provider: "groq",
		Model:    "llama3",
		Messages: []models.Message{{Role: "user", Content: "hello"}},
	}

	resp, err := c.Get(context.Background(), req)
	if err != nil {
		t.Errorf("expected no error on cache miss, got: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil response on cache miss, got: %+v", resp)
	}
}

func TestCache_SetAndGet(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer s.Close()

	c, err := cache.New("redis://"+s.Addr(), time.Minute)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	req := &models.GatewayRequest{
		Provider: "groq",
		Model:    "llama3",
		Messages: []models.Message{{Role: "user", Content: "hello"}},
	}

	resp := &models.GatewayResponse{
		ID:        "resp-123",
		Provider:  "groq",
		Model:     "llama3",
		Content:   "response text",
		LatencyMS: 500,   // Should be zeroed out
		Cached:    false, // Should be zeroed out during set
	}

	// 1. Set the response
	err = c.Set(context.Background(), req, resp)
	if err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	// 2. Get the response back
	cachedResp, err := c.Get(context.Background(), req)
	if err != nil {
		t.Fatalf("failed to get from cache: %v", err)
	}
	if cachedResp == nil {
		t.Fatalf("expected response, got nil")
	}

	// 3. Verify zeroed fields and set fields
	if !cachedResp.Cached {
		t.Errorf("expected Cached to be true after retrieval, got false")
	}
	if cachedResp.LatencyMS != 0 {
		t.Errorf("expected LatencyMS to be 0 from cache, got %d", cachedResp.LatencyMS)
	}
	if cachedResp.Content != "response text" {
		t.Errorf("expected content 'response text', got '%s'", cachedResp.Content)
	}
}
