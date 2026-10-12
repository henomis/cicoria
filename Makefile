# cicoria — common tasks. Run "make help" for the list.

BIN := cicoria
PKG := .

# Tests start cicoria themselves; inside a cicoria session these two would
# point them at the one that is running.
GOTEST := env -u CICORIA_SOCK -u CICORIA_SESSION go test

.DEFAULT_GOAL := build
.PHONY: help build install run test race test-real fmt vet lint check site clean

help: ## List the targets
	@awk -F ':.*## ' '/^[a-z-]+:.*## /{printf "  %-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build ./cicoria
	go build -o $(BIN) $(PKG)

install: ## Install cicoria into GOBIN
	go install $(PKG)

run: build ## Build and open cicoria on this directory
	./$(BIN)

test: ## Run the tests
	$(GOTEST) ./...

race: ## Run the tests with the race detector
	$(GOTEST) -race ./...

test-real: ## Run the test that embeds the real Claude Code
	CICORIA_E2E_REAL=$(CURDIR) $(GOTEST) -run TestRealAgent -v $(PKG)

fmt: ## Format the code
	gofmt -w .

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint, as the CI does
	golangci-lint run ./...

check: vet test ## Check formatting, vet and test: what to run before a commit
	@test -z "$$(gofmt -l .)" || { echo "not formatted:"; gofmt -l .; exit 1; }

site: ## Serve the website in web/ on http://localhost:8000
	python3 -m http.server 8000 --directory web

clean: ## Remove the built binary
	rm -f $(BIN)
