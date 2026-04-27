GO           ?= go
GOFLAGS      ?=
GOLANGCI_LINT ?= golangci-lint
BINARY       ?= mesh-operator
BIN_DIR      ?= bin
MAIN_PKG     ?= ./cmd/mesh-operator

.PHONY: all build test vet lint tidy fmt check clean

all: check build

build:
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/$(BINARY) $(MAIN_PKG)

test:
	$(GO) test $(GOFLAGS) ./...

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

tidy:
	$(GO) mod tidy

fmt:
	$(GO) fmt ./...

check: fmt vet test

clean:
	rm -rf $(BIN_DIR)
