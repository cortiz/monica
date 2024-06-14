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
	./$(BiNARY_NAME)

# Tests the project.
.PHONY: test
test: generate
	$(GOTEST) -v ./...

# Cleans the project.
.PHONY: clean
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)

# Installs the dependencies.
.PHONY: deps
deps:
	$(GOGET) -u ./...

# Format the code.
.PHONY: fmt
fmt:
	$(GOCMD) fmt ./...

# Runs static analysis on the code.
.PHONY: lint
lint:
	golint ./...

.PHONY: release
release: build-deps pr lint
	goreleaser release --rm-dist

.PHONY: pr
pr: generate test fmt
	echo "All checks passed"

.PHONY: generate
generate:
	$(GOCMD) generate ./...

# Prints help information.
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all           - Runs the 'test' and 'build' targets."
	@echo "  build         - Compiles the project and generates an executable named '$(BINARY_NAME)'."
	@echo "  run           - Executes the compiled binary '$(BINARY_NAME)'."
	@echo "  test          - Runs all tests in the project."
	@echo "  clean         - Cleans up the project by removing the compiled binaries and any temporary files."
	@echo "  deps          - Installs or updates the project dependencies."
	@echo "  fmt           - Formats the Go code according to the Go style guidelines."
	@echo "  lint          - Runs static analysis on the code to identify potential issues."
	@echo "	build-deps     - Installs the dependencies needed to build the project."
	@echo " release			   - Creates a ignition Release."
