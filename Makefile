# All targets resolve modules from the local cache; drop GOPROXY=off once the
# machine has network access to proxy.golang.org.
GO      ?= go
GOFLAGS ?= -mod=mod
export GOFLAGS

.PHONY: help run build migrate migrate-status backfill-images seed-images seed-assets seed seed-fresh test test-race cover lint vet fmt tidy clean

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

run: ## Start the API
	$(GO) run ./cmd/api

build: ## Compile the API into bin/api
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o bin/api ./cmd/api

migrate: ## Apply pending SQL migrations
	$(GO) run ./cmd/migrate

migrate-status: ## Show applied and pending migrations, change nothing
	$(GO) run ./cmd/migrate -status

backfill-images: ## Recover storage handles for rows written before they were stored (dry run)
	$(GO) run ./cmd/backfill-images

seed-images: ## Render the fixture's placeholder images into images/
	python3 tools/generate-seed-images.py

seed-assets: seed-images ## Render and upload the fixture's images to the storage provider
	$(GO) run ./cmd/seed-assets

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
