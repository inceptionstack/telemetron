GOFLAGS ?= -mod=readonly
export GOFLAGS
export CGO_ENABLED ?= 0

BIN := lokiotel
PKG := ./cmd/lokiotel

.PHONY: build test lint run release

build:
	go build -trimpath $(PKG)

test:
	go test ./...

lint:
	golangci-lint run

run:
	go run $(PKG) start

release:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/$(BIN)-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o dist/$(BIN)-linux-arm64 $(PKG)
