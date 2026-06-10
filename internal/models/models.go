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
	if r.Provider != "groq" && r.Provider != "gemini" {
		return fmt.Errorf("provider must be groq or gemini, got: %s", r.Provider)
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
	ID             string
	KeyHash        string
	Name           string
	RateLimitRPS   int
	DailyBudgetUSD *float64
	IsActive       bool
	CreatedAt      time.Time
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
