# Reelyard build helpers. Windows users without make can run the same
# commands by hand (see README).

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X github.com/reelyard/server/internal/version.Version=$(VERSION) -X github.com/reelyard/server/internal/version.Commit=$(COMMIT) -X github.com/reelyard/server/internal/version.Date=$(DATE)
# Pure Go everywhere: no C toolchain needed, and the torrent library then
# uses bbolt instead of a C SQLite.
export CGO_ENABLED = 0

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: all web build test vet run cross clean ci ci-checks smoke hooks

all: web build

web:
	cd web && npm ci && npm run build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/reelyard$(if $(filter windows,$(shell go env GOOS)),.exe,) ./cmd/reelyard

test:
	go test ./...

vet:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

# The CI workflow's checks, locally: web build once, then gofmt/vet, tests
# and the smoke test in parallel.
ci: web
	@$(MAKE) --no-print-directory -j3 -O ci-checks
	@echo "ci: all checks passed"

ci-checks: vet test smoke

smoke:
	CGO_ENABLED=0 go build -o bin/reelyard-smoke ./cmd/reelyard
	./scripts/smoke.sh bin/reelyard-smoke

# Run `make ci` before every git push (skip once with --no-verify).
hooks:
	git config core.hooksPath .githooks

run:
	go run ./cmd/reelyard serve

cross:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/reelyard-$$os-$$arch$$ext ./cmd/reelyard || exit 1; \
	done

clean:
	go clean
	rm -r -f ./bin ./dist
