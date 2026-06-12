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
	for f in migrations/*.sql; do psql $$DATABASE_URL -f "$$f"; done

lint:
	golangci-lint run
