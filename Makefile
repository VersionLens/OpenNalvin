GO_TAGS := sqlite_fts5
GOFLAGS := -tags=$(GO_TAGS)

export GOFLAGS

.PHONY: build build-api build-web dev dev-api dev-web test test-go test-web typecheck-web tidy clean cli

## Build

build: build-web build-api

build-api:
	go build -o nalvin .

build-web:
	bun run --cwd web build

## Dev

dev:
	concurrently -k -n api,web -c cyan,magenta "$(MAKE) dev-api" "$(MAKE) dev-web"

dev-api:
	air

dev-web:
	bun run --cwd web dev

## Test

test: test-go test-web

test-go:
	go test ./...

test-web:
	bun run --cwd web test

typecheck-web:
	bun run --cwd web typecheck

## Misc

tidy:
	go mod tidy

cli:
	go run . $(ARGS)

clean:
	rm -f nalvin
	rm -rf web/dist
