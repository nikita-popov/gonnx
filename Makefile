.PHONY: build check clean

GO       = go
BIN_DIR := bin
DAEMON  := $(BIN_DIR)/gonnxd
CTL     := $(BIN_DIR)/gonnxctl
VERSION := $(shell printf '%s-dev' "$$(git describe --tags --always --dirty 2>/dev/null || echo unknown)")
LDFLAGS := -s -w -X main.version=$(VERSION)

all: deps build

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(DAEMON) ./cmd/gonnxd
	$(GO) build -o $(CTL)    ./cmd/gonnxctl

deps:
	$(GO) mod tidy
	$(GO) mod download

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

check: fmt vet test

clean:
	rm -rf $(BIN_DIR)
