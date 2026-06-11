package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/rauni-rainy/ai-gateway/internal/cache"
	"github.com/rauni-rainy/ai-gateway/internal/middleware"
	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/rauni-rainy/ai-gateway/internal/proxy"
)

func TestCacheStampedePrevention(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer s.Close()

	c, _ := cache.New("redis://"+s.Addr(), time.Minute)
	mockStore := &MockStore{}

	var providerCalls int32
	mockProv := &MockProvider{
		CompleteFunc: func(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
			atomic.AddInt32(&providerCalls, 1)
			
			// Simulate a slow API call (e.g. LLM generation) to ensure other requests pile up
			time.Sleep(50 * time.Millisecond)
			
			return &models.GatewayResponse{
				Provider: "groq",
				Model:    req.Model,
				Content:  "stampede prevented response",
			}, nil
		},
	}

	providers := map[string]proxy.Provider{
		"groq": mockProv,
	}

	handler := proxy.NewHandler(c, nil, mockStore, providers)
	
	// Wrap in auth middleware
	mw := middleware.Auth(mockStore)
	server := mw(handler)

	// Build the request body
	reqBody := map[string]interface{}{
		"provider":   "groq",
		"model":      "llama3",
		"max_tokens": 100,
		"messages":   []map[string]string{{"role": "user", "content": "solve a hard math problem"}},
	}
	b, _ := json.Marshal(reqBody)

	// Fire 50 concurrent requests
	const numConcurrent = 50
	var wg sync.WaitGroup
	wg.Add(numConcurrent)

	// Use a start barrier to ensure they all fire at the exact same nanosecond
	startBarrier := make(chan struct{})

	for i := 0; i < numConcurrent; i++ {
		go func() {
			defer wg.Done()
			
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer valid")
			rec := httptest.NewRecorder()

			<-startBarrier // wait for the signal

			server.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
				return
			}
			
			var gwResp models.GatewayResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &gwResp); err != nil {
				t.Errorf("failed to decode response: %v", err)
			}
			if gwResp.Content != "stampede prevented response" {
				t.Errorf("unexpected content: %s", gwResp.Content)
			}
		}()
	}

	// Release the hounds
	close(startBarrier)
	
	// Wait for all requests to finish
	wg.Wait()

	// Assert provider was called exactly once
	if calls := atomic.LoadInt32(&providerCalls); calls != 1 {
		t.Errorf("expected provider to be called exactly 1 time, but was called %d times", calls)
	}
}
