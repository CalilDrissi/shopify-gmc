.PHONY: help build run worker scheduler seed migrate test fmt vet lint tidy migrate-up migrate-down migrate-version migrate-cycle dev-css build-css node_modules docker-up docker-down clean

GO        := go
BIN_DIR   := bin
SERVER    := $(BIN_DIR)/server
WORKER    := $(BIN_DIR)/worker
SCHEDULER := $(BIN_DIR)/scheduler
SEED      := $(BIN_DIR)/seed
MIGRATE   := $(BIN_DIR)/migrate

-include .env
export

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'

build: ## Build all binaries into ./bin
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(SERVER) ./cmd/server
	$(GO) build -o $(WORKER) ./cmd/worker
	$(GO) build -o $(SCHEDULER) ./cmd/scheduler
	$(GO) build -o $(SEED) ./cmd/seed
	$(GO) build -o $(MIGRATE) ./cmd/migrate

run: ## Run the HTTP server
	$(GO) run ./cmd/server

worker: ## Run the background worker
	$(GO) run ./cmd/worker

scheduler: ## Run the periodic scheduler
	$(GO) run ./cmd/scheduler

seed: ## Seed the database with sample data
	$(GO) run ./cmd/seed

test: ## Run all tests
	$(GO) test ./...

fmt: ## Format Go code
	$(GO) fmt ./...

vet: ## Run go vet
	$(GO) vet ./...

lint: ## Run staticcheck (requires github.com/golangci/golangci-lint)
	golangci-lint run ./...

tidy: ## Run go mod tidy
	$(GO) mod tidy

migrate: ## Run the migrate CLI (e.g. make migrate ARGS="version")
	$(GO) run ./cmd/migrate $(ARGS)

migrate-up: ## Apply all pending migrations
	$(GO) run ./cmd/migrate up

migrate-down: ## Roll back the last migration
	$(GO) run ./cmd/migrate down 1

migrate-version: ## Show the current schema version
	$(GO) run ./cmd/migrate version

migrate-cycle: ## Verify migrations: up, then down to zero, then up again
	$(GO) run ./cmd/migrate up
	$(GO) run ./cmd/migrate down
	$(GO) run ./cmd/migrate up

node_modules: package.json
	npm install --no-audit --no-fund
	@touch node_modules

dev-css: node_modules ## Watch Sass and recompile to static/css/main.css
	npx sass --watch styles/main.scss:static/css/main.css

build-css: node_modules ## Compile compressed Sass to static/css/main.css (consumed by go:embed)
	npx sass --no-source-map --style=compressed styles/main.scss:static/css/main.css

vendor-js: node_modules ## Vendor Alpine.js + HTMX into static/js/
	cp node_modules/alpinejs/dist/cdn.min.js static/js/alpine.js
	cp node_modules/htmx.org/dist/htmx.min.js static/js/htmx.js

docker-up: ## Start Postgres and Mailhog
	docker compose up -d

docker-down: ## Stop docker services
	docker compose down

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) static/css/main.css static/css/main.css.map node_modules
