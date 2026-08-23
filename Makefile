ENV_FILE := .env

ifneq (,$(wildcard $(ENV_FILE)))
include $(ENV_FILE)
export
endif

.PHONY: help run build test vet fmt fmt-check lint ci clean

help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*## "}; {printf "%-12s %s\n", $$1, $$2}'

run: ## Run the proxy locally, config sourced from .env (see .env.example)
ifeq (,$(wildcard $(ENV_FILE)))
	$(error $(ENV_FILE) not found; copy .env.example to .env and fill in your Immich URL/API key)
endif
	go run .

build: ## Build the binary into dist/
	go build -o dist/immich-dlna-proxy .

test: ## Run the test suite (matches CI)
	go test ./... -v -race

vet: ## Run go vet
	go vet ./...

fmt: ## Reformat source with gofmt
	gofmt -w .

fmt-check: ## Fail if gofmt would change anything (matches CI)
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

lint: ## Run golangci-lint
	golangci-lint run

ci: vet fmt-check test ## Run the same checks as CI

clean: ## Remove build output
	rm -rf dist
