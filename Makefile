.DEFAULT_GOAL := help

VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo "dev")
IMAGE ?= cnpg-i-pganalyze:dev
CONTAINER_TOOL ?= docker

.PHONY: help
help: ## Show this help message
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: test
test: ## Run the tests
	go test -race -cover ./...

.PHONY: build
build: ## Build the plugin binary into bin/
	CGO_ENABLED=0 go build -ldflags "-X github.com/pganalyze/cnpg-i-pganalyze/pkg/metadata.Version=$(VERSION)" -o bin/cnpg-i-pganalyze ./cmd/plugin

.PHONY: image
image: ## Build the plugin image
	$(CONTAINER_TOOL) build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

.PHONY: manifest
manifest: ## Render the install manifest to manifest.yaml
	kubectl kustomize kubernetes > manifest.yaml
