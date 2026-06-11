CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE semantic_cache (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(), 
    provider TEXT NOT NULL, 
    model TEXT NOT NULL, 
    prompt_text TEXT NOT NULL, 
    prompt_embedding vector(128), 
    response_json TEXT NOT NULL, 
    hit_count INTEGER DEFAULT 0, 
    created_at TIMESTAMPTZ DEFAULT NOW(), 
    last_hit_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX ON semantic_cache USING ivfflat (prompt_embedding vector_cosine_ops) WITH (lists = 10);
CREATE INDEX ON semantic_cache(provider, model);
