.PHONY: build check clean deps dist fmt install-sdk test test-py vet

GO       = go
PYTHON  ?= python3
BIN_DIR := bin
DIST_DIR := dist
DAEMON  := $(BIN_DIR)/gonnxd
CTL     := $(BIN_DIR)/gonnxctl
VERSION := $(shell printf '%s-dev' "$$(git describe --tags --always --dirty 2>/dev/null || echo unknown)")
LDFLAGS := -s -w -X main.version=$(VERSION)
OS      := linux
ARCH    ?= amd64
# Default state dir for dev installs; override with: make install-sdk STATE_DIR=...
STATE_DIR ?= $(HOME)/.local/share/gonnx

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

# Copy sdk/python into STATE_DIR/sdk/python so gonnxd can find it.
# Usage (dev):    make install-sdk
# Usage (system): sudo make install-sdk STATE_DIR=/var/lib/gonnx
install-sdk:
	@echo "==> installing SDK into $(STATE_DIR)/sdk/python"
	@mkdir -p "$(STATE_DIR)/sdk/python"
	@cp -r sdk/python/. "$(STATE_DIR)/sdk/python/"
	@echo "    done"

# Build a release tarball locally (mirrors what release.yml does in CI).
# Usage:  make dist ARCH=amd64   or   make dist ARCH=arm64
dist:
	@mkdir -p $(DIST_DIR)
	@echo "==> building binaries ($(OS)/$(ARCH))"
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) \
		$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/gonnxd   ./cmd/gonnxd
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) \
		$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/gonnxctl ./cmd/gonnxctl
	@echo "==> building Python SDK wheel"
	$(PYTHON) -m pip install -q build
	$(PYTHON) -m build --wheel --outdir $(DIST_DIR)/ sdk/python
	@echo "==> generating checksums"
	(cd $(DIST_DIR) && sha256sum gonnxd gonnxctl *.whl > checksums.sha256)
	@echo "==> packing tarball"
	tar -czf $(DIST_DIR)/gonnx-$(OS)-$(ARCH)-$(VERSION).tar.gz \
		-C $(DIST_DIR) gonnxd gonnxctl checksums.sha256 $$(ls $(DIST_DIR)/*.whl | xargs -n1 basename)
	@echo "done: $(DIST_DIR)/gonnx-$(OS)-$(ARCH)-$(VERSION).tar.gz"

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)
