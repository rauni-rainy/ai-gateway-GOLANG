package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	"github.com/rauni-rainy/ai-gateway/internal/models"
)

type Embedder interface {
	Embed(text string) []float32
}

// TFIDFEmbedder builds a simple 128-dimensional embedding from word frequencies.
// TODO: replace with sentence-transformers API or Anthropic embeddings in production.
type TFIDFEmbedder struct{}

func (e *TFIDFEmbedder) Embed(text string) []float32 {
	vec := make([]float32, 128)
	
	// Tokenize
	text = strings.ToLower(text)
	f := func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsNumber(c)
	}
	words := strings.FieldsFunc(text, f)
	
	if len(words) == 0 {
		return vec
	}

	// Calculate basic hash-based frequencies into 128 buckets
	for _, word := range words {
		var hash uint32
		for _, char := range word {
			hash = hash*31 + uint32(char)
		}
		bucket := hash % 128
		vec[bucket]++
	}

	// Normalize to unit vector via L2 norm
	var sumSquares float32
	for _, val := range vec {
		sumSquares += val * val
	}
	
	if sumSquares > 0 {
		norm := float32(math.Sqrt(float64(sumSquares)))
		for i := range vec {
			vec[i] /= norm
		}
	}
	
	return vec
}

type SemanticCache struct {
	db        *pgxpool.Pool
	embedder  Embedder
	threshold float64
}

func NewSemanticCache(db *pgxpool.Pool, embedder Embedder) *SemanticCache {
	return &SemanticCache{
		db:        db,
		embedder:  embedder,
		threshold: 0.92, // Default similarity threshold
	}
}

func (c *SemanticCache) FindSimilar(ctx context.Context, req *models.GatewayRequest) (*models.GatewayResponse, error) {
	// Concatenate all message content
	var promptBuilder strings.Builder
	for _, msg := range req.Messages {
		promptBuilder.WriteString(msg.Content)
		promptBuilder.WriteString(" ")
	}
	promptText := promptBuilder.String()

	emb := c.embedder.Embed(promptText)
	vector := pgvector.NewVector(emb)

	var responseJSON string
	var similarity float64
	var id string

	// Using Cosine Similarity (<=>)
	err := c.db.QueryRow(ctx, `
		SELECT id, response_json, 1 - (prompt_embedding <=> $1) as similarity 
		FROM semantic_cache 
		WHERE provider = $2 AND model = $3 AND 1 - (prompt_embedding <=> $1) > $4 
		ORDER BY similarity DESC 
		LIMIT 1
	`, vector, req.Provider, req.Model, c.threshold).Scan(&id, &responseJSON, &similarity)

	if err != nil {
		return nil, nil // No similar cached response found
	}

	var resp models.GatewayResponse
	if err := json.Unmarshal([]byte(responseJSON), &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal semantic response: %w", err)
	}

	resp.Cached = true

	// Async update hit count and timestamp
	go func() {
		_, err := c.db.Exec(context.Background(), `
			UPDATE semantic_cache 
			SET hit_count = hit_count + 1, last_hit_at = NOW() 
			WHERE id = $1
		`, id)
		if err != nil {
			fmt.Printf("failed to update semantic cache hits: %v\n", err)
		}
	}()

	return &resp, nil
}

func (c *SemanticCache) Store(ctx context.Context, req *models.GatewayRequest, resp *models.GatewayResponse) error {
	var promptBuilder strings.Builder
	for _, msg := range req.Messages {
		promptBuilder.WriteString(msg.Content)
		promptBuilder.WriteString(" ")
	}
	promptText := promptBuilder.String()

	emb := c.embedder.Embed(promptText)
	vector := pgvector.NewVector(emb)

	respCopy := *resp
	respCopy.Cached = false
	respCopy.LatencyMS = 0

	jsonData, err := json.Marshal(&respCopy)
	if err != nil {
		return fmt.Errorf("failed to marshal semantic response: %w", err)
	}

	_, err = c.db.Exec(ctx, `
		INSERT INTO semantic_cache (provider, model, prompt_text, prompt_embedding, response_json)
		VALUES ($1, $2, $3, $4, $5)
	`, req.Provider, req.Model, promptText, vector, jsonData)
	
	if err != nil {
		return fmt.Errorf("failed to insert semantic cache: %w", err)
	}

	return nil
}
