package models

import (
	"fmt"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type GatewayRequest struct {
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
	Messages     []Message `json:"messages"`
	MaxTokens    int       `json:"max_tokens"`
	Temperature  float64   `json:"temperature"`
	Stream       bool      `json:"stream"`
}

func (r *GatewayRequest) Validate() error {
	if r.Provider != "groq" && r.Provider != "gemini" && r.Provider != "mock" {
		return fmt.Errorf("provider must be groq, gemini, or mock, got: %s", r.Provider)
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("messages must not be empty")
	}
	if r.MaxTokens <= 0 {
		return fmt.Errorf("max_tokens must be greater than 0")
	}
	return nil
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type GatewayResponse struct {
	ID        string  `json:"id"`
	Provider  string  `json:"provider"`
	Model     string  `json:"model"`
	Content   string  `json:"content"`
	Usage     Usage   `json:"usage"`
	Cached    bool    `json:"cached"`
	LatencyMS int64   `json:"latency_ms"`
	CostUSD   float64 `json:"cost_usd"`
}

type APIKey struct {
	ID             string    `json:"id"`
	KeyHash        string    `json:"-"`
	Name           string    `json:"name"`
	RateLimitRPS   int       `json:"rate_limit_rps"`
	DailyBudgetUSD *float64  `json:"daily_budget_usd"`
	IsActive       bool      `json:"is_active"`
	IsAdmin        bool      `json:"is_admin"`
	CreatedAt      time.Time `json:"created_at"`
}

type RequestLog struct {
	ID               string
	APIKeyID         string
	Provider         string
	Model            string
	ErrorMessage     string
	PromptTokens     int
	CompletionTokens int
	LatencyMS        int64
	Cached           bool
	StatusCode       int
	CostUSD          float64
	CreatedAt        time.Time
}

type AdminStats struct {
	TotalRequests      int               `json:"total_requests"`
	CachedRequests     int               `json:"cached_requests"`
	TotalCostUSD       float64           `json:"total_cost_usd"`
	AvgLatencyMS       float64           `json:"avg_latency_ms"`
	P95LatencyMS       float64           `json:"p95_latency_ms"`
	RequestsByProvider map[string]int    `json:"requests_by_provider"`
}

type KeyStats struct {
	APIKeyID     string `json:"api_key_id"`
	RequestCount int    `json:"request_count"`
}
