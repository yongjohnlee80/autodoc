VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# The dev checkout nests worktrees inside the bare repo, and Go's nested-VCS
# rule then points buildvcs at the bare dir and fails; the version comes from
# LDFLAGS instead. CI clones normally and is unaffected.
export GOFLAGS := -buildvcs=false

.PHONY: build build-gui test race vet fmt cover test-lua clean

# `make build` is the TUI. On Linux it is static, with no cgo: modernc SQLite needs none.
# `make build-gui` adds the GUI (--gui, golib/gui on Gio), the only thing that needs cgo, and on
# Linux the window system's development libraries (GUI_LIBS, as pkg-config names them).
# macOS builds with cgo either way, since golib watches the workspace roots with FSEvents there
# (the Command Line Tools), so there `make build` is `make build-gui`: the GUI costs nothing more.
GUI_LIBS := egl wayland-egl wayland-client wayland-cursor x11 xkbcommon xkbcommon-x11 x11-xcb xcursor xfixes vulkan

ifeq ($(shell uname -s),Darwin)
build: build-gui
else
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/autodoc ./cmd/autodoc
endif

build-gui:
	@if [ "$$(uname -s)" = Linux ] && ! pkg-config --exists $(GUI_LIBS); then \
		echo "make build-gui: missing the window system's development libraries:"; \
		for l in $(GUI_LIBS); do pkg-config --exists $$l || echo "  $$l"; done; \
		echo "Debian/Ubuntu: libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libxcursor-dev libxfixes-dev libvulkan-dev"; \
		echo "Arch: wayland libx11 libxkbcommon libxkbcommon-x11 libxcursor libxfixes mesa vulkan-headers"; \
		echo "Or make build: the TUI alone, static."; \
		exit 1; \
	fi
	CGO_ENABLED=1 go build -tags gui -trimpath -ldflags "$(LDFLAGS)" -o bin/autodoc ./cmd/autodoc

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

# The Lua side's suites (tests/run-all.sh): a daemon built from this checkout, in headless Neovim.
test-lua:
	./tests/run-all.sh

clean:
	rm -rf bin coverage.out
