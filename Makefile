VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  = -s -w \
	-X github.com/windlass-dev/windlass/internal/version.Version=$(VERSION) \
	-X github.com/windlass-dev/windlass/internal/version.Commit=$(COMMIT)

.PHONY: dev build build-web test test-integration lint clean

## dev: run the backend; pair with `npm run dev` in web/ for the frontend
dev:
	go run ./cmd/windlass

## build-web: compile the frontend into web/dist
build-web:
	cd web && npm ci && npm run build

## build: single production binary with the frontend embedded
build: build-web
	go build -trimpath -tags embedweb -ldflags '$(LDFLAGS)' -o bin/windlass ./cmd/windlass

## build-cross: linux binaries for release
build-cross: build-web
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -tags embedweb -ldflags '$(LDFLAGS)' -o bin/windlass-linux-amd64 ./cmd/windlass
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -tags embedweb -ldflags '$(LDFLAGS)' -o bin/windlass-linux-arm64 ./cmd/windlass

## test: unit tests only — no Docker, no Node required
test:
	go test ./...

## test-integration: requires Docker + Caddy (runs in CI on Linux)
test-integration:
	go test -tags integration -count=1 ./...

lint:
	golangci-lint run

clean:
	rm -rf bin web/dist
