.PHONY: build check clean

BIN_DIR := bin
DAEMON  := $(BIN_DIR)/gonnxd
CTL     := $(BIN_DIR)/gonnxctl

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(DAEMON) ./cmd/gonnxd
	go build -o $(CTL)    ./cmd/gonnxctl

check:
	go vet ./...
	go test ./...

clean:
	rm -rf $(BIN_DIR)
