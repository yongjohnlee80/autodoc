VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# The dev checkout nests worktrees inside the bare repo, and Go's nested-VCS
# rule then points buildvcs at the bare dir and fails; the version comes from
# LDFLAGS instead. CI clones normally and is unaffected.
export GOFLAGS := -buildvcs=false

.PHONY: build test race vet fmt cover clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/autodoc ./cmd/autodoc

test:
	go test ./...

# What CI runs: the race detector, the suite twice.
race:
	go test -race -count=2 -timeout 20m ./...

vet:
	go vet ./...

fmt:
	@unformatted=$$(gofmt -l .); if [ -n "$$unformatted" ]; then echo "gofmt would rewrite:"; echo "$$unformatted"; exit 1; fi

# The coverage profile CI's policy compares, and its per-function summary.
cover:
	go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

clean:
	rm -rf bin coverage.out
