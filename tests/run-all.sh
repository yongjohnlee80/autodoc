#!/usr/bin/env bash
# The single entry point for AutoDoc's Lua (Neovim) suites. It builds the binary from this checkout
# (the suites that need a daemon start it from that build), then runs every tests/*_spec.lua as
# `nvim --headless -u NONE -l <suite>` (the family runner contract), each in its own throwaway
# XDG and TMPDIR root. The run FAILS when any suite
#   - printed no "<P> passed, <F> failed" summary (an abort mid-run),
#   - reported a non-zero failed count, or
#   - exited non-zero (a crash before or after its summary).
# A new suite is picked up by its *_spec.lua name; nothing else needs editing.
#
#   tests/run-all.sh            every suite
#   tests/run-all.sh preview    one suite (preview_spec.lua)
#
# auto-core.nvim comes from AUTODOC_TEST_AUTOCORE, else the sibling checkout.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"
cd "$root" || exit 1

command -v nvim >/dev/null 2>&1 || { echo "run-all: nvim not found on PATH"; exit 127; }

work="$(mktemp -d "${TMPDIR:-/tmp}/autodoc-lua.XXXXXX")" || { echo "run-all: no work dir"; exit 1; }
trap 'rm -rf -- "$work"' EXIT

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
export AUTO_CORE="$AUTODOC_TEST_AUTOCORE" # the name the KB tooling's suite reads

echo "==> building autodoc"
if ! CGO_ENABLED=0 GOFLAGS=-buildvcs=false go build -o "$work/bin/autodoc" ./cmd/autodoc; then
  echo "run-all: the build failed" >&2
  exit 1
fi
export AUTODOC_TEST_BIN="$work/bin/autodoc"
export LUA_PATH="$here/?.lua;;"

only="${1:-}"
overall=0
ran=0

run_suite() {
  local file="$1" name sandbox out rc summary failed
  name="$(basename "$file" .lua)"
  echo "==> $name"
  sandbox="$(mktemp -d "$work/$name.XXXXXX")" || { echo "run-all: no sandbox for $name"; overall=1; return; }
  mkdir -p "$sandbox"/{config,data,state,cache,run,tmp}
  chmod 700 "$sandbox/run"
  out="$(XDG_CONFIG_HOME="$sandbox/config" XDG_DATA_HOME="$sandbox/data" XDG_STATE_HOME="$sandbox/state" \
    XDG_CACHE_HOME="$sandbox/cache" XDG_RUNTIME_DIR="$sandbox/run" TMPDIR="$sandbox/tmp" \
    AUTODOC_TEST_TMP="$sandbox" nvim --headless -u NONE -l "$file" 2>&1)"
  rc=$?
  printf '%s\n' "$out" | grep -E '^ *FAIL' | head -20
  summary="$(printf '%s\n' "$out" | grep -oE '[0-9]+ passed, [0-9]+ failed' | tail -1 || true)"
  if [ -z "$summary" ]; then
    echo "   x $name: NO SUMMARY LINE (aborted mid-run)"
    printf '%s\n' "$out" | tail -20 | sed 's/^/     /'
    overall=1
    return
  fi
  echo "   $name: $summary (exit=$rc)"
  failed="$(printf '%s' "$summary" | grep -oE '[0-9]+' | tail -1)"
  if [ "${failed:-0}" -gt 0 ]; then
    echo "   x $name: $failed failed"
    overall=1
  elif [ "$rc" -ne 0 ]; then
    echo "   x $name: exited $rc after its summary"
    overall=1
  fi
}

for f in "$here"/*_spec.lua; do
  [ -e "$f" ] || continue
  if [ -n "$only" ] && [ "$(basename "$f" .lua)" != "${only}_spec" ]; then continue; fi
  run_suite "$f"
  ran=$((ran + 1))
done

if [ "$ran" -eq 0 ]; then
  echo "run-all: no suite matched ${only:-*}"
  exit 1
fi
if [ "$overall" -ne 0 ]; then
  echo "run-all: one or more suites failed"
  exit 1
fi
echo "run-all: all $ran suite(s) passed"
