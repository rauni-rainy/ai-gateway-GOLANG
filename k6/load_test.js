import http from 'k6/http';
import { check } from 'k6';
import { Rate } from 'k6/metrics';

export const rateLimitRate = new Rate('rate_limit_rate');

const API_KEY = __ENV.API_KEY || 'missing_key';

export const options = {
    scenarios: {
        cache_hit: {
            executor: 'constant-vus',
            vus: 100,
            duration: '60s',
            exec: 'cacheHitScenario',
        },
        cache_miss: {
            executor: 'constant-vus',
            vus: 10,
            duration: '60s',
            exec: 'cacheMissScenario',
        },
    },
    thresholds: {
        'http_req_duration{scenario:cache_hit}': ['p(95) < 10'],
        'http_req_duration{scenario:cache_miss}': ['p(95) < 3000'],
    },
};

export function cacheHitScenario() {
    const url = 'http://localhost:8080/v1/complete';
    const payload = JSON.stringify({
        provider: 'groq',
        model: 'llama-3.1-8b-instant',
        max_tokens: 100,
        messages: [{ role: 'user', content: 'What is the speed of light?' }],
    });

    const params = {
        headers: {
            'Content-Type': 'application/json',
            'Authorization': `Bearer ${API_KEY}`,
        },
        tags: { scenario: 'cache_hit' }
    };

    const res = http.post(url, payload, params);

    rateLimitRate.add(res.status === 429);

    check(res, {
        'status is 200 or 429': (r) => r.status === 200 || r.status === 429,
    });
}

export function cacheMissScenario() {
    const url = 'http://localhost:8080/v1/complete';
    const randomSeed = Math.random().toString(36).substring(7);
    const payload = JSON.stringify({
        provider: 'groq',
        model: 'llama-3.1-8b-instant',
        max_tokens: 100,
        messages: [{ role: 'user', content: `What is the speed of light? Seed: ${randomSeed}` }],
    });

    const params = {
        headers: {
            'Content-Type': 'application/json',
            'Authorization': `Bearer ${API_KEY}`,
        },
        tags: { scenario: 'cache_miss' }
    };

    const res = http.post(url, payload, params);

    rateLimitRate.add(res.status === 429);

    check(res, {
        'status is 200 or 429': (r) => r.status === 200 || r.status === 429,
    });
}
