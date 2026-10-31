# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
BINARY_NAME=monica
GOINST = $(GOCMD) install

# Default target executed when `make` is called without arguments.
.PHONY: all
all: help

# Installs the dependencies.
.PHONY: build-deps
build-deps:
	$(GOINST) "github.com/goreleaser/goreleaser/v2@latest"

# Builds the project.
.PHONY: build
build: generate
	$(GOBUILD) -o $(BINARY_NAME) -v

# Runs the project.
.PHONY: run
run:
	./$(BINARY_NAME)

# Tests the project.
.PHONY: test
test: generate
	$(GOTEST) -v ./... -cover

# Cleans the project.
.PHONY: clean
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)

# Installs the dependencies.
.PHONY: deps
deps:
	$(GOGET) -u ./...

# Format the code.
.PHONY: fmt
fmt:
	$(GOCMD) fmt ./...

# Fails if any file is not gofmt-formatted (used by CI; does not modify files).
.PHONY: fmt-check
fmt-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "Not gofmt-formatted:"; echo "$$unformatted"; exit 1; fi

# Runs static analysis on the code.
.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: release
release: build-deps pr lint
	goreleaser release --clean

.PHONY: pr
pr: generate test fmt-check
	@echo "All checks passed"

.PHONY: generate
generate:
	$(GOCMD) generate ./...

# Prints help information.
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build       - Compiles the project into '$(BINARY_NAME)'."
	@echo "  run         - Executes the compiled binary '$(BINARY_NAME)'."
	@echo "  test        - Runs all tests (offline-safe) with coverage."
	@echo "  clean       - Removes the compiled binary."
	@echo "  deps        - Updates the project dependencies."
	@echo "  fmt         - Formats the Go code."
	@echo "  fmt-check   - Fails if any Go file is not gofmt-formatted."
	@echo "  lint        - Runs golangci-lint."
	@echo "  pr          - PR gate: generate + test + fmt-check."
	@echo "  build-deps  - Installs the tools needed to release."
	@echo "  release     - Creates a release with goreleaser."
