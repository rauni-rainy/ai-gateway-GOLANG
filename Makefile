.PHONY: db-up db-down dev test bench build migrate lint

db-up:
	docker-compose up -d postgres redis

db-down:
	docker-compose down

dev: db-up
	go run ./cmd/server

test:
	go test ./... -race -count=1 -v

bench:
	go test ./... -bench=. -benchmem

build:
	CGO_ENABLED=0 go build -o bin/gateway ./cmd/server

migrate:
	psql $$DATABASE_URL -f migrations/001_schema.sql

lint:
	golangci-lint run
