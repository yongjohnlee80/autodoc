VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DATE := $(shell date -u +%F)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.buildDate=$(BUILD_DATE)

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
GUI_LIBS := egl wayland-egl wayland-client wayland-cursor x11 xkbcommon xkbcommon-x11 x11-xcb xcursor xfixes

# The Vulkan backend is optional, and not in GUI_LIBS for a reason: gio draws
# through EGL/OpenGL by default -- Wayland tries EGL first unless the app asks
# for app.PreferVulkan, and X11 keeps Vulkan off entirely (gio's vulkanBuggy,
# gioui.org/issue/347) -- so a build without it is a complete GUI. What it costs
# is a compile-time header, vulkan/vulkan.h, which ships separately from the
# loader that installs vulkan.pc (Arch: vulkan-headers, Debian/Ubuntu:
# libvulkan-dev). `pkg-config --exists vulkan` is therefore satisfied on a host
# that cannot compile it, which is why build-gui probes the header itself.
#
#   VULKAN=auto  (default) with Vulkan when the header is there, without it when not
#   VULKAN=1     require it: fail naming the package when the header is missing
#   VULKAN=0     omit it: EGL/OpenGL only, no header needed
VULKAN ?= auto

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
		echo "Debian/Ubuntu: libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libxcursor-dev libxfixes-dev"; \
		echo "Arch: wayland libx11 libxkbcommon libxkbcommon-x11 libxcursor libxfixes mesa"; \
		echo "Or make build: the TUI alone, static."; \
		exit 1; \
	fi
	@tags=gui; \
	if printf '%s\n' '#include <vulkan/vulkan.h>' | $(CC) -E -x c - $$(pkg-config --cflags vulkan 2>/dev/null) >/dev/null 2>&1; \
		then have=yes; else have=no; fi; \
	case "$(VULKAN)" in \
	1) if [ $$have = no ]; then \
	     echo "make build-gui VULKAN=1: vulkan/vulkan.h not found (Arch: vulkan-headers, Debian/Ubuntu: libvulkan-dev)"; \
	     exit 1; \
	   fi ;; \
	0) tags=gui,novulkan ;; \
	*) if [ $$have = no ]; then \
	     tags=gui,novulkan; \
	     echo "make build-gui: no vulkan/vulkan.h -- building EGL/OpenGL only (VULKAN=1 to require Vulkan)"; \
	   fi ;; \
	esac; \
	set -x; CGO_ENABLED=1 go build -tags "$$tags" -trimpath -ldflags "$(LDFLAGS)" -o bin/autodoc ./cmd/autodoc

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
