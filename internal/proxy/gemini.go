package proxy

import (
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

type GeminiProvider struct {
	apiKey     string
	httpClient *http.Client
	BaseURL    string // Exported for easy overriding in tests
}

func NewGemini(apiKey string) *GeminiProvider {
	return &GeminiProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		BaseURL: "https://generativelanguage.googleapis.com",
	}
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
}

type geminiErrorResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

func (p *GeminiProvider) Complete(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
	var contents []geminiContent
	var systemParts []geminiPart

	if req.SystemPrompt != "" {
		systemParts = append(systemParts, geminiPart{Text: req.SystemPrompt})
	}

	for _, m := range req.Messages {
		if m.Role == "system" {
			systemParts = append(systemParts, geminiPart{Text: m.Content})
			continue
		}

		role := m.Role
		// Map OpenAI/Anthropic "assistant" role to Gemini "model" role
		if role == "assistant" {
			role = "model"
		}

		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		})
	}

	apiReq := geminiRequest{
		Contents: contents,
	}

	if len(systemParts) > 0 {
		apiReq.SystemInstruction = &geminiContent{
			Parts: systemParts,
		}
	}

	bodyBytes, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal gemini request: %w", err)
	}

	// Build URL: https://generativelanguage.googleapis.com/v1beta/models/<model>:generateContent?key=<apiKey>
	// Use strings.TrimSuffix to allow testing with localhost URLs smoothly.
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", strings.TrimSuffix(p.BaseURL, "/"), req.Model, p.apiKey)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

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
		var errResp geminiErrorResponse
		msg := string(respBody)
		// Parse the detailed Google error message if available
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error.Message != "" {
			msg = errResp.Error.Message
		}

		return nil, &ProviderError{
			StatusCode: resp.StatusCode,
			Message:    msg,
			Provider:   "gemini",
		}
	}

	var apiResp geminiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode gemini response: %w", err)
	}

	if len(apiResp.Candidates) == 0 || len(apiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no content returned from gemini")
	}

	in := apiResp.UsageMetadata.PromptTokenCount
	out := apiResp.UsageMetadata.CandidatesTokenCount
	cost := float64(in)*0.00000035 + float64(out)*0.00000105

	return &models.GatewayResponse{
		Provider: "gemini",
		Model:    req.Model,
		Content:  apiResp.Candidates[0].Content.Parts[0].Text,
		Usage: models.Usage{
			PromptTokens:     in,
			CompletionTokens: out,
			TotalTokens:      in + out,
		},
		CostUSD: cost,
	}, nil
}

// TODO: streaming via Gemini SSE
func (p *GeminiProvider) CompleteStream(ctx context.Context, req *models.GatewayRequest, w http.ResponseWriter) (*models.Usage, error) {
	return nil, fmt.Errorf("gemini streaming not yet implemented")
}
