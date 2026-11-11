# Harbor build helpers. Windows users without make can run the same
# commands by hand (see README).

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X github.com/inluxc/harbor/internal/version.Version=$(VERSION) -X github.com/inluxc/harbor/internal/version.Commit=$(COMMIT) -X github.com/inluxc/harbor/internal/version.Date=$(DATE)
# Pure Go everywhere: no C toolchain needed, and the torrent library then
# uses bbolt instead of a C SQLite.
export CGO_ENABLED = 0

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: all web build test vet run cross clean

all: web build

web:
	cd web && npm ci && npm run build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/harbor$(if $(filter windows,$(shell go env GOOS)),.exe,) ./cmd/harbor

test:
	go test ./...

vet:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

run:
	go run ./cmd/harbor serve

cross:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/harbor-$$os-$$arch$$ext ./cmd/harbor || exit 1; \
	done

clean:
	go clean
	rm -r -f ./bin ./dist
