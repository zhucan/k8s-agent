GOOS    ?= linux
GOARCH  ?= amd64
BIN_DIR := bin

GO_BUILD := CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags "-s -w"

.PHONY: all build bot cli test clean

all: build

build: bot cli

bot:
	$(GO_BUILD) -o $(BIN_DIR)/bot ./cmd/bot

cli:
	$(GO_BUILD) -o $(BIN_DIR)/cli ./cmd/cli

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR)
