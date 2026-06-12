# AI Gateway in Go

An ultra-fast, production-ready reverse proxy for LLM APIs (Groq, Gemini) designed for high concurrency and strict rate limiting. It optimizes costs and prevents abuse through exact matching, semantic vector caching, and strict budget enforcement.

## Architecture

![Architecture](file:///C:/Users/rauna/.gemini/antigravity-ide/brain/3a525be5-7158-4ce9-9569-16db43f45d3d/artifacts/architecture.svg)

## Features

- Exact cache with cache stampede (thundering herd) prevention using Redis Pub/Sub locking.
- Semantic cache using pgvector (128-d TF-IDF cosine similarity) to catch similar prompts.
- Rate limiting using a high-performance sliding window over Redis.
- Circuit breaker for graceful failovers between providers.
- Cost budgeting enforced via a fast PostgreSQL materialized view.
- Streaming SSE support with direct unbuffered proxying to clients.
- Admin API for key management and P95 observability metrics.

## Benchmark Results

| Scenario | req/s | p95 latency | allocs/op |
| :--- | :--- | :--- | :--- |
| Cache Hit | ~15,223 | < 5ms | 86 |
| Cache Miss (Provider Latency Excluded) | ~14,516 | < 10ms | 87 |

*(Tests executed on 12th Gen Intel Core i5 with miniredis)*

## Quick Start

```bash
git clone https://github.com/rauni-rainy/ai-gateway-GOLANG.git
cp .env.example .env
docker-compose up -d
make migrate
curl -X POST http://localhost:8080/v1/complete \
     -H "Authorization: Bearer YOUR_API_KEY" \
     -H "Content-Type: application/json" \
     -d '{"provider":"groq", "model":"llama-3.1-8b-instant", "max_tokens":100, "messages":[{"role":"user", "content":"hello"}]}'
```

## API Reference

**POST /v1/complete**

*Request:*
```json
{
  "provider": "groq",
  "model": "llama3-8b-8192",
  "system_prompt": "You are a helpful assistant.",
  "messages": [
    {
      "role": "user",
      "content": "What is the speed of light?"
    }
  ],
  "max_tokens": 100,
  "temperature": 0.7,
  "stream": false
}
```

*Response:*
```json
{
  "id": "123e4567-e89b-12d3-a456-426614174000",
  "provider": "groq",
  "model": "llama3-8b-8192",
  "content": "The speed of light in a vacuum is exactly 299,792,458 meters per second.",
  "usage": {
    "prompt_tokens": 15,
    "completion_tokens": 20,
    "total_tokens": 35
  },
  "cached": true,
  "latency_ms": 4,
  "cost_usd": 0.000000345
}
```

## Design Decisions

- **Sliding window over fixed window for rate limiting** — because it prevents the boundary spike problem where users can burst double their limit exactly at the minute mark reset.
- **pgvector for semantic cache over a standalone vector DB** — because it allows us to use our existing PostgreSQL transactional database, drastically reducing infrastructure overhead and operational complexity.
- **Circuit breaker threshold of 5** — because it prevents cascading failures by immediately failing fast and allowing failovers before our upstream connection pools are saturated.

## Configuration

| Environment Variable | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `PORT` | string | `8080` | The port the HTTP server listens on. |
| `DATABASE_URL` | string | - | PostgreSQL connection string. |
| `REDIS_URL` | string | - | Redis connection string. |
| `GROQ_API_KEY` | string | - | Primary API key for Groq. |
| `GEMINI_API_KEY` | string | - | Fallback API key for Gemini. |
| `CACHE_TTL_SECONDS` | int | `3600` | How long to cache identical responses. |
