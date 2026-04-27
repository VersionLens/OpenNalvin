GO_TAGS := sqlite_fts5
GOFLAGS := -tags=$(GO_TAGS)

export GOFLAGS

.PHONY: build build-api build-web dev dev-api dev-web test test-go test-web typecheck-web tidy clean cli check-naming

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

test: test-go test-web check-naming

# check-naming fails the build if any tracked source file leaks the name of
# the upstream project this codebase was branched off. Keep the exclude list
# in sync with the directories actually shipped to users (no node_modules,
# git, vendored source, or built web bundle).
check-naming:
	@token=$$(printf 'llm''ctl'); \
	matches=$$(grep -rIn -e "$$token" -e "$$(printf '%s' "$$token" | tr a-z A-Z)" . \
	  --include='*.go' --include='*.yaml' --include='*.yml' --include='*.md' \
	  --include='*.sql' --include='*.json' --include='*.mod' --include='*.sum' \
	  --include=Dockerfile \
	  --exclude-dir=node_modules --exclude-dir=.git \
	  --exclude-dir=third_party --exclude-dir=web/dist \
	  --exclude-dir=.claude 2>/dev/null); \
	if [ -n "$$matches" ]; then \
	  echo "check-naming: forbidden token found"; \
	  echo "$$matches"; \
	  exit 1; \
	fi

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
