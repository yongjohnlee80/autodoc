#!/usr/bin/env bash
# The single entry point for AutoDoc's Lua suites: builds the binary from this checkout, gives the
# suites a sandboxed XDG tree, and runs every tests/*_spec.lua under `nvim --headless -u NONE -l`.
# A suite passes only by printing its summary line with 0 failed; a suite that dies without one
# fails the run. auto-core.nvim comes from AUTODOC_TEST_AUTOCORE, else the sibling checkout.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/autodoc-lua.XXXXXX")"
trap 'rm -rf "$work"' EXIT

if [ -z "${AUTODOC_TEST_AUTOCORE:-}" ]; then
  for c in "$root/../../auto-core.nvim/main" "$root/../auto-core.nvim"; do
    if [ -d "$c/lua/auto-core" ]; then AUTODOC_TEST_AUTOCORE="$(cd "$c" && pwd)"; break; fi
  done
fi
if [ -z "${AUTODOC_TEST_AUTOCORE:-}" ] || [ ! -d "$AUTODOC_TEST_AUTOCORE/lua/auto-core" ]; then
  echo "run-all: auto-core.nvim not found: set AUTODOC_TEST_AUTOCORE" >&2
  exit 1
fi
export AUTODOC_TEST_AUTOCORE

echo "==> building autodoc"
if ! (cd "$root" && CGO_ENABLED=0 GOFLAGS=-buildvcs=false go build -o "$work/bin/autodoc" ./cmd/autodoc); then
  echo "run-all: the build failed" >&2
  exit 1
fi
export AUTODOC_TEST_BIN="$work/bin/autodoc"
export LUA_PATH="$here/?.lua;;"

rc=0
for spec in "$here"/*_spec.lua; do
  name="$(basename "$spec")"
  sandbox="$work/$name"
  mkdir -p "$sandbox"/{config,data,state,cache,run}
  chmod 700 "$sandbox/run"
  echo "==> $name"
  out="$(cd "$root" && XDG_CONFIG_HOME="$sandbox/config" XDG_DATA_HOME="$sandbox/data" XDG_STATE_HOME="$sandbox/state" \
    XDG_CACHE_HOME="$sandbox/cache" XDG_RUNTIME_DIR="$sandbox/run" AUTODOC_TEST_TMP="$sandbox" \
    nvim --headless -u NONE -l "$spec" 2>&1)"
  echo "$out"
  if ! printf '%s\n' "$out" | grep -qE '^SUMMARY: [0-9]+ passed, 0 failed$'; then
    echo "run-all: $name FAILED"
    rc=1
  fi
done
if [ "$rc" -ne 0 ]; then
  echo "run-all: one or more suites failed"
  exit 1
fi
echo "run-all: all suites passed"
