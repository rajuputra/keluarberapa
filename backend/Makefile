# KeluarBerapa backend - developer tasks.
#
# Everything here is a thin wrapper over the go tool, so nothing is required
# beyond a Go toolchain and a PostgreSQL instance.

BINARY_DIR := bin

.DEFAULT_GOAL := help
.PHONY: help setup run build migrate migrate-status fmt vet test test-db test-all check clean

help: ## Show the available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

setup: ## Create .env from the template and download dependencies
	@test -f .env || cp .env.example .env
	go mod download

run: ## Start the API (reads .env)
	go run ./cmd/api

build: ## Compile both binaries into bin/
	go build -o $(BINARY_DIR)/api ./cmd/api
	go build -o $(BINARY_DIR)/migrate ./cmd/migrate

migrate: ## Apply pending database migrations
	go run ./cmd/migrate -action up

migrate-status: ## Show which migrations have been applied
	go run ./cmd/migrate -action status

fmt: ## Format every Go file
	gofmt -w .

vet: ## Run go vet
	go vet ./...

test: ## Run the unit tests (database tests are skipped)
	go test ./...

test-db: ## Run the database integration tests (needs TEST_DATABASE_URL)
	@test -n "$(TEST_DATABASE_URL)" || \
		{ echo "TEST_DATABASE_URL is not set; see README 'Running the tests'"; exit 1; }
	go test ./internal/database/... -count=1 -v

test-all: ## Run every test, including the database integration tests
	go test ./... -count=1

check: fmt vet test ## Format, vet and test - run this before committing
	@gofmt -l . | tee /dev/stderr | (! read)

clean: ## Remove build output
	rm -rf $(BINARY_DIR)
	go clean -testcache
