# everest-mcp — OpenEverest MCP Gateway
VERSION ?= dev
IMAGE   ?= ghcr.io/namansh70747/everest-mcp:$(VERSION)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build
build: ## Build the gateway binary
	go build -trimpath -ldflags "$(LDFLAGS)" -o everest-mcp ./cmd/everest-mcp

.PHONY: test
test: ## Run unit + in-memory MCP tests
	go test ./...

.PHONY: e2e
e2e: ## Run all tests including the end-to-end stdio test
	go test -tags e2e -race ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format the code
	gofmt -w .

.PHONY: check
check: fmt vet e2e ## Format, vet, and run the full test suite

.PHONY: demo
demo: build ## Run the scripted live demo (set EVEREST_URL, EVEREST_TOKEN, NS)
	go run ./cmd/everest-mcp-demo --everest-url "$(EVEREST_URL)" --token "$(EVEREST_TOKEN)" \
	  --cluster "$(or $(CLUSTER),local)" --namespace "$(NS)"

.PHONY: docker
docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
