package cache

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rauni-rainy/ai-gateway/internal/models"
	"github.com/redis/go-redis/v9"
)

type Cache struct {
	client *redis.Client
	ttl    time.Duration
}

func New(redisURL string, ttl time.Duration) (*Cache, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis url: %w", err)
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	return &Cache{
		client: client,
		ttl:    ttl,
	}, nil
}

func (c *Cache) Client() *redis.Client {
	return c.client
}

func (c *Cache) RequestHash(req *models.GatewayRequest) string {
	// Create a temporary struct with only the fields that affect the output.
	// We omit Stream, as it doesn't change the final text content.
	hashData := struct {
		Provider     string           `json:"provider"`
		Model        string           `json:"model"`
		SystemPrompt string           `json:"system_prompt,omitempty"`
		Messages     []models.Message `json:"messages"`
		MaxTokens    int              `json:"max_tokens"`
		Temperature  float64          `json:"temperature"`
	}{
		Provider:     req.Provider,
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
		Messages:     req.Messages,
		MaxTokens:    req.MaxTokens,
		Temperature:  req.Temperature,
	}

	data, _ := json.Marshal(hashData)
	h := sha256.Sum256(data)
	return fmt.Sprintf("cache:%x", h)
}

func (c *Cache) Get(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
	key := c.RequestHash(req)

	val, err := c.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil // Cache miss is a normal scenario, not an error
		}
		return nil, fmt.Errorf("redis get error: %w", err) // Real connection/timeout error
	}

	var resp models.GatewayResponse
	if err := json.Unmarshal([]byte(val), &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cached response: %w", err)
	}

	resp.Cached = true
	return &resp, nil
}

func (c *Cache) Set(ctx context.Context, req *models.GatewayRequest, resp *models.GatewayResponse) error {
	key := c.RequestHash(req)

	// Create a copy and zero out request-specific properties
	cacheResp := *resp
	cacheResp.LatencyMS = 0
	cacheResp.Cached = false

	data, err := json.Marshal(cacheResp)
	if err != nil {
		return fmt.Errorf("failed to marshal response for cache: %w", err)
	}

	if err := c.client.Set(ctx, key, data, c.ttl).Err(); err != nil {
		return fmt.Errorf("redis set error: %w", err)
	}

	return nil
}

func (c *Cache) Delete(ctx context.Context, req *models.GatewayRequest) error {
	key := c.RequestHash(req)
	if err := c.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis del error: %w", err)
	}
	return nil
}

var ErrLockTimeout = errors.New("lock wait timeout exceeded")

func (c *Cache) GetOrLock(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, bool, error) {
	hash := c.RequestHash(req)
	lockKey := "lock:" + hash
	channel := "notify:" + hash

	// 1. Try acquiring the lock
	acquired, err := c.client.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil {
		return nil, false, fmt.Errorf("failed to setnx: %w", err)
	}

	if acquired {
		return nil, true, nil // Caller owns the lock and must make the provider call
	}

	// 2. Lock not acquired, we are a subscriber
	pubsub := c.client.Subscribe(ctx, channel)
	defer pubsub.Close()

	// Wait for the result
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-time.After(35 * time.Second):
		return nil, false, ErrLockTimeout
	case msg := <-pubsub.Channel():
		var resp models.GatewayResponse
		if err := json.Unmarshal([]byte(msg.Payload), &resp); err != nil {
			return nil, false, fmt.Errorf("failed to unmarshal pubsub payload: %w", err)
		}
		resp.Cached = true
		return &resp, false, nil
	}
}

func (c *Cache) PublishResult(ctx context.Context, req *models.GatewayRequest, resp *models.GatewayResponse) error {
	hash := c.RequestHash(req)
	
	// Copy and zero out fields to match cache schema
	respCopy := *resp
	respCopy.LatencyMS = 0
	respCopy.Cached = false

	jsonData, err := json.Marshal(&respCopy)
	if err != nil {
		return fmt.Errorf("failed to marshal for publish: %w", err)
	}

	// Persist to cache
	if err := c.Set(ctx, req, resp); err != nil {
		return fmt.Errorf("failed to set cache: %w", err)
	}

	// Publish to channel
	if err := c.client.Publish(ctx, "notify:"+hash, jsonData).Err(); err != nil {
		return fmt.Errorf("failed to publish: %w", err)
	}

	// Delete lock
	if err := c.client.Del(ctx, "lock:"+hash).Err(); err != nil {
		return fmt.Errorf("failed to delete lock: %w", err)
	}

	return nil
}

