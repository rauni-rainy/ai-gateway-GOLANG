CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key_hash TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    rate_limit_rps INTEGER NOT NULL DEFAULT 10,
    daily_budget_usd NUMERIC(10,4) DEFAULT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE request_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,
    provider TEXT NOT NULL CHECK (provider IN ('groq', 'gemini')),
    model TEXT NOT NULL,
    prompt_tokens INTEGER,
    completion_tokens INTEGER,
    latency_ms BIGINT,
    cached BOOLEAN NOT NULL DEFAULT FALSE,
    status_code INTEGER,
    cost_usd NUMERIC(10,6),
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_request_logs_api_key_id ON request_logs(api_key_id);
CREATE INDEX idx_request_logs_created_at_desc ON request_logs(created_at DESC);
CREATE INDEX idx_request_logs_provider ON request_logs(provider);
CREATE INDEX idx_request_logs_uncached ON request_logs(created_at DESC) WHERE cached = FALSE;

-- Example: How to insert a test API key using PostgreSQL's pgcrypto
-- This creates a hash for the raw key 'test-key-123'
-- 
-- INSERT INTO api_keys (key_hash, name) 
-- VALUES (encode(digest('test-key-123', 'sha256'), 'hex'), 'Test Key');
