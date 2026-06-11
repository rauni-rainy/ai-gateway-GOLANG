CREATE MATERIALIZED VIEW IF NOT EXISTS daily_spend_mv AS 
SELECT 
    api_key_id, 
    SUM(cost_usd) as total_usd, 
    DATE(created_at AT TIME ZONE 'UTC') as day 
FROM request_logs 
GROUP BY api_key_id, DATE(created_at AT TIME ZONE 'UTC');

CREATE UNIQUE INDEX ON daily_spend_mv(api_key_id, day);
CREATE INDEX ON daily_spend_mv(day);
