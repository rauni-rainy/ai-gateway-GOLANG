package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rauni-rainy/ai-gateway/internal/models"
)

type GroqProvider struct {
	apiKey     string
	httpClient *http.Client
	BaseURL    string // Exported for easy overriding in tests
}

func NewGroq(apiKey string) *GroqProvider {
	return &GroqProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			// 60-second timeout is extremely important to prevent goroutines from blocking forever 
			// if the LLM provider hangs during a request.
			Timeout: 60 * time.Second,
		},
		BaseURL: "https://api.groq.com/openai/v1/chat/completions",
	}
}

type groqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type groqRequest struct {
	Model       string        `json:"model"`
	Messages    []groqMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type groqResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (p *GroqProvider) Complete(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
	var groqMsgs []groqMessage

	// If there's a top-level SystemPrompt, prepend it as a system message (Groq/OpenAI format)
	if req.SystemPrompt != "" {
		groqMsgs = append(groqMsgs, groqMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	for _, m := range req.Messages {
		groqMsgs = append(groqMsgs, groqMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	apiReq := groqRequest{
		Model:       req.Model,
		Messages:    groqMsgs,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	bodyBytes, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal groq request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &ProviderError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
			Provider:   "groq",
		}
	}

	var apiResp groqResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode groq response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned from groq")
	}

	in := apiResp.Usage.PromptTokens
	out := apiResp.Usage.CompletionTokens

	// TODO: make pricing configurable. Placeholders used below.
	cost := float64(in)*0.000003 + float64(out)*0.000015

	return &models.GatewayResponse{
		Provider: "groq",
		Model:    req.Model,
		Content:  apiResp.Choices[0].Message.Content,
		Usage: models.Usage{
			PromptTokens:     in,
			CompletionTokens: out,
			TotalTokens:      in + out,
		},
		CostUSD: cost,
	}, nil
}

func (p *GroqProvider) CompleteStream(ctx context.Context, req *models.GatewayRequest, w http.ResponseWriter) (*models.Usage, error) {
	var groqMsgs []groqMessage

	if req.SystemPrompt != "" {
		groqMsgs = append(groqMsgs, groqMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	for _, m := range req.Messages {
		groqMsgs = append(groqMsgs, groqMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	apiReq := groqRequest{
		Model:       req.Model,
		Messages:    groqMsgs,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      true,
	}

	bodyBytes, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal groq request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	// Set response headers before reading
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &ProviderError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
			Provider:   "groq",
		}
	}

	scanner := bufio.NewScanner(resp.Body)
	var finalUsage models.Usage

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				if line == "data: [DONE]" {
					fmt.Fprintf(w, "data: [DONE]\n\n")
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					return &finalUsage, nil
				}
				
				fmt.Fprintf(w, "%s\n\n", line)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading stream: %w", err)
	}

	return &finalUsage, nil
}
