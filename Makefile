GOFLAGS ?= -mod=readonly
export GOFLAGS
export CGO_ENABLED ?= 0

BIN := lokiotel
PKG := ./cmd/lokiotel
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(DATE)

.PHONY: build test lint run release

build:
	go build -trimpath -ldflags "$(LDFLAGS)" $(PKG)

test:
	go test ./...

lint:
	golangci-lint run

run:
	go run -ldflags "$(LDFLAGS)" $(PKG) start

release:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='$(LDFLAGS) -s -w' -o dist/$(BIN)-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='$(LDFLAGS) -s -w' -o dist/$(BIN)-linux-arm64 $(PKG)
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags='$(LDFLAGS) -s -w' -o dist/$(BIN)-darwin-arm64 $(PKG)
