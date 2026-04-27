.PHONY: build check clean deps fmt test test-py vet

GO       = go
PYTHON  ?= python3
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
	$(GO) test -v -race ./...

# Install the Python SDK in editable mode (once) and run pytest.
# Override the interpreter with:  make test-py PYTHON=python3.11
test-py:
	$(PYTHON) -m pip install -q -e "sdk/python[dev]"
	$(PYTHON) -m pytest -v sdk/python

check: fmt vet test test-py

clean:
	rm -rf $(BIN_DIR)
