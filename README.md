# Building an AI API Gateway in Go: A Notebook of Systems Engineering Learnings

Welcome to my API Gateway project! I built this project to act as an ultra-fast, production-ready reverse proxy for LLM APIs like Groq and Gemini. 

When I first started building a tool designed to help schools generate assignments using AI I thought the hardest part was going to be the prompting and the AI itself.

But once real traffic started hitting the application, I quickly realized the actual challenge wasn't the AI at all. It was the infrastructure.

We were hitting provider rate limits almost immediately. Every time a teacher regenerated an assignment, or dozens of students asked the exact same question, we were paying for the exact same expensive tokens and waiting full seconds for identical responses. The system was bleeding latency and money.

So, I took a step back and decided to build my own ultra-fast AI API Gateway in Go to sit between my applications and providers like Groq and Gemini. I wanted to solve three core problems: Cost, Latency, and Resiliency.

 Building a highly concurrent system in Go is a wild ride, and I hit some absolutely fascinating distributed systems bottlenecks along the way. Here is the story of the errors I encountered, how I thought through them, and how I fixed them.

---
<img width="1131" height="593" alt="Screenshot 2026-06-13 000143" src="https://github.com/user-attachments/assets/f5b60e0d-3c38-4d4a-9393-d422c81d0a75" />

## 1. The Circuit Breaker & The 429 Rate Limit

**The Setup:** I wrote a `k6` load test to blast my gateway with 110 concurrent virtual users to see how it would handle heavy traffic. I was routing requests to Groq's API (`llama-3.1-8b-instant`).

**The Error:** Immediately, `k6` started reporting a 100% failure rate. Every single request failed with `{"error":"all providers unavailable","retry_after_seconds":30}`.

**My Thought Process:** Why did every request fail? I realized Groq's free tier has a strict rate limit of about 30 requests per minute. When I blasted it with 110 concurrent requests, Groq instantly returned `429 Too Many Requests`. But my gateway didn't just pass back the 429. It triggered my **Circuit Breaker**! The circuit breaker detected the massive spike in upstream errors, tripped OPEN to protect the system, and started returning `503 Service Unavailable`.

**The Learning:** This was actually a huge success! The gateway worked exactly as intended. It intercepted a DDoS-level spike of traffic, protected my upstream LLM provider from getting spammed (which could get my API key banned), and safely failed the requests.
<img width="1038" height="870" alt="image" src="https://github.com/user-attachments/assets/2254bd20-6300-434b-9789-d31c510d21a5" />

---

## 2. The Cache Stampede & The 39-Second Hang

**The Setup:** I implemented a Redis Pub/Sub locking mechanism to protect against "Cache Stampedes" (the Thundering Herd problem). If 100 users ask the exact same question at the exact same time, only *one* user should become the "owner" and query the LLM, while the other 99 wait in line via Redis PubSub for the owner to cache the answer.

**The Error:** During the load test, the 99 waiting users experienced a mind-blowing `p(95)` latency of **39 seconds** before finally failing.

**My Thought Process:** Why did they hang for 39 seconds? I dug into my Redis locking code. When the 100 users hit the gateway, one became the owner and asked Groq. Groq returned a 429 error, which tripped the circuit breaker (returning 503). *But the owner failed to publish an error message to the Redis channel to wake up the 99 waiting users!* So the 99 users sat in line patiently waiting for a response that would never come, until they eventually hit a connection timeout ~39 seconds later.

**The Correction:** Always ensure your locking mechanism handles failure states gracefully. If the owner crashes or hits an error, it *must* release the lock and notify the subscribers so they don't hang indefinitely.

---

## 3. Creating a "Mock" Provider to Test True Concurrency

**The Problem:** Because Groq and Gemini strictly rate limit free accounts, I realized I couldn't test the true performance of my Go server. Every time I ran `k6`, I hit API limits instantly.

**The Approach:** I built a custom **Mock Provider** directly into the gateway. The Mock Provider simulated an LLM by intentionally sleeping for exactly 200 milliseconds and then returning a standard JSON response. 

**The Result:** This was a game-changer. By routing my load test to `"provider": "mock"`, I completely isolated my Go server from upstream constraints. This allowed me to blast thousands of requests per second with `k6` to see my system's true scaling capabilities!
<img width="1086" height="860" alt="Screenshot 2026-06-13 001441" src="https://github.com/user-attachments/assets/0494b450-d7ac-4466-b149-04ec1cc7f13d" />

---

## 4. Connection Pool Starvation (The Final Boss)

**The Setup:** With the Mock Provider running perfectly, I re-ran the `k6` load test. 

**The Error:** I got a 100% success rate (almost 2,000 requests processed successfully!), BUT my `p(95)` latency was still incredibly high—sitting at around **5.2 seconds** and later to 3.29 seconds. On top of that, my terminal was spamming database errors: `violates check constraint "request_logs_provider_check"`.

**My Thought Process:** This was the most fascinating bug of all. Let's break it down:
1. **The DB Constraint:** My PostgreSQL schema had a safety check: `CHECK (provider IN ('groq', 'gemini'))`. Because I was using the `"mock"` provider, the database violently rejected every attempt to insert a request log!
2. **The 5-Second Latency:** My logging was asynchronous (`go func() { ... }`). So why did a background task slow down the main API requests?
   - I fired 110 concurrent virtual users.
   - The server spawned 2,000 background goroutines all trying to write to the database at once.
   - The default Go PostgreSQL driver (`pgxpool`) only allows a small number of connections (e.g., 8).
   - The 2,000 background logging tasks completely flooded the connection pool!
   - When a *new* incoming API request tried to run `GetAPIKey()` to verify a token, it found all connections occupied by the failing logging tasks, so it was forced to wait 5 seconds in line just to get a connection to Singapore!
<img width="1053" height="821" alt="Screenshot 2026-06-13 001535" src="https://github.com/user-attachments/assets/0d8f7b56-a734-49e4-88d5-f90b43859c53" />

**The Correction:** 
1. **Connection Pool Sizing:** I dynamically scaled up my connection pool by appending `&pool_max_conns=100` to my `DATABASE_URL` in my `.env` file.
2. **Architecture Lesson:** This perfectly illustrated why enterprise logging systems never write directly to a main database! In a production system, you must put a Message Queue (like Kafka or Redis Streams) in the middle. The background goroutines dump logs into the queue (0.1ms), and a separate worker slowly inserts them into the DB in batches without starving the main connection pool.

---

## Conclusion

Building this gateway was a masterclass in distributed systems engineering. I learned firsthand that high concurrency in Go is easy, but managing the downstream bottlenecks—like API rate limits, Redis locking deadlocks, and PostgreSQL connection pool starvation—is what truly separates a toy project from enterprise architecture. 

If you're reading this code, I hope you find the implementations of the Circuit Breaker, the Cache Stampede locks, and the Semantic Vector Caching as fascinating to read as they were to build!

---

### Quick Start

```bash
# Clone the repo
git clone https://github.com/rauni-rainy/ai-gateway-GOLANG.git

# Set up your environment (NOTE: .env is excluded from git!)
cp .env.example .env

# Start your database and redis
docker-compose up -d

# Run migrations
make migrate

# Test the API
curl -X POST http://localhost:8080/v1/complete \
     -H "Authorization: Bearer my-admin-secret" \
     -H "Content-Type: application/json" \
     -d '{"provider":"groq", "model":"llama-3.1-8b-instant", "max_tokens":100, "messages":[{"role":"user", "content":"hello"}]}'
```
