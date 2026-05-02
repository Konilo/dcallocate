.PHONY: build test lint tidy run release clean help

BIN := dcallocate
PKG := ./cmd/dcallocate
DIST := dist

GOOS_LIST := windows linux darwin
ifeq ($(OS),Windows_NT)
EXT_windows := .exe
else
EXT_windows := .exe
endif

# `make` with no target prints help.
help:
	@echo "Targets:"
	@echo "  build      build the local binary into ./bin/$(BIN)"
	@echo "  test       run all tests with verbose output"
	@echo "  lint       run go vet and golangci-lint"
	@echo "  tidy       go mod tidy"
	@echo "  run        go run ./cmd/dcallocate -- pass arguments via ARGS=..."
	@echo "  release    cross-compile for windows / linux / darwin into ./$(DIST)/"
	@echo "  clean      remove ./bin and ./$(DIST)"

build:
	mkdir -p bin
	go build -o bin/$(BIN) $(PKG)

test:
	go test ./... -v

lint:
	go vet ./...
	golangci-lint run

tidy:
	go mod tidy

run:
	go run $(PKG) $(ARGS)

release: clean
	mkdir -p $(DIST)
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(BIN)-windows-amd64.exe $(PKG)
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(BIN)-linux-amd64       $(PKG)
	GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(BIN)-linux-arm64       $(PKG)
	GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(BIN)-darwin-arm64      $(PKG)
	GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(BIN)-darwin-amd64      $(PKG)
	@ls -lh $(DIST)/

clean:
	rm -rf bin $(DIST)
