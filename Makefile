# All targets resolve modules from the local cache; drop GOPROXY=off once the
# machine has network access to proxy.golang.org.
GO      ?= go
GOFLAGS ?= -mod=mod
export GOFLAGS

.PHONY: help run build seed seed-fresh test test-race cover lint vet fmt tidy clean

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

run: ## Start the API
	$(GO) run ./cmd/api

build: ## Compile the API into bin/api
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o bin/api ./cmd/api

seed: ## Populate the database with demo data (idempotent)
	$(GO) run ./cmd/seed -migrate

seed-fresh: ## Clear the seeded tables, then re-seed. Destructive.
	$(GO) run ./cmd/seed -migrate -truncate -yes

test: ## Run the test suite
	$(GO) test ./...

test-race: ## Run the test suite with the race detector
	$(GO) test -race ./...

cover: ## Report coverage per package
	$(GO) test -race -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format all Go files
	gofmt -w .

lint: fmt vet ## Format then vet

tidy: ## Sync go.mod/go.sum
	$(GO) mod tidy

clean: ## Remove build artefacts
	rm -rf bin coverage.out
