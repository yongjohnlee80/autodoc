#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 || ! $1 =~ ^[0-9]+$ || ! $2 =~ ^[0-9]+$ ]]; then
  echo "usage: bash tests/run-tui-shard.sh <zero-based shard> <shard count>" >&2
  exit 2
fi
shard=$((10#$1))
count=$((10#$2))
if (( count < 1 || shard >= count )); then
  echo "shard must be less than a positive shard count" >&2
  exit 2
fi

listing=$(mktemp "${TMPDIR:-/tmp}/autodoc-tui-tests.XXXXXX")
trap 'rm -f "$listing"' EXIT
go test -mod=readonly -race -list '^Test' ./tui > "$listing"
selection=$(awk -v shard="$shard" -v count="$count" '
  /^Test/ {
    if (seen++ % count == shard) {
      printf "%s%s", separator, $0
      separator = "|"
      selected++
    }
  }
  END { if (!selected) exit 1 }
' "$listing")
echo "TUI race shard $shard/$count: $(tr '|' '\n' <<< "$selection" | wc -l) tests, each twice" >&2
go test -mod=readonly -race -count=2 -timeout 15m -json -run "^($selection)$" ./tui
