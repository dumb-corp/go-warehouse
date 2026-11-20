.PHONY: run up down test build

run:
	@set -a; . ./dev.env; set +a; go run ./cmd/server

up:
	docker compose up -d

down:
	docker compose down -v

test:
	go test ./...

build:
	go build -o bin/server ./cmd/server

run-build:
	@set -a; . ./dev.env; set +a; ./bin/server