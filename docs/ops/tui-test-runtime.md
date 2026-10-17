# TUI test runtime

The TUI tests mount the real QML program and many run a real RPC daemon, SQLite
store, filesystem watcher, browser renderer or plugin subprocess. They include
integration tests as well as state-only unit tests. Keep those contracts covered
when changing the fixtures.

## Run the CI race tests

CI runs four independent TUI jobs:

```sh
bash tests/run-tui-shard.sh 0 4 > tui-0.jsonl
bash tests/run-tui-shard.sh 1 4 > tui-1.jsonl
bash tests/run-tui-shard.sh 2 4 > tui-2.jsonl
bash tests/run-tui-shard.sh 3 4 > tui-3.jsonl
```

Run all four to reproduce the complete TUI race gate. Shard indices start at zero.
The script discovers tests from the race build with `go test -list`, assigns
successive top-level tests modulo the shard count, and anchors the resulting
`-run` expression. Every discovered test belongs to exactly one shard; its
subtests run with it. New tests join automatically. Each shard retains `-race`
and `-count=2`, and has a 15-minute package deadline. Invalid arguments and empty
selections fail.

Separate processes preserve tests that change environment variables or package
globals. Adding `t.Parallel` to those cases would change their isolation.
The main Go job runs every other package with the same race/count policy.
GUI and head/base coverage jobs still execute the complete TUI suite under their
respective build and coverage settings. Each race job uploads its JSONL timing
output even on failure; download the `tui-race-*` artifacts for per-case results.

## Database fixture

`tui/store_test.go` creates an empty store using the production `store.Open`,
applies the current migration history once per test process, closes every
connection, and only then reads the checkpointed database bytes. Every daemon
gets those bytes in a different temporary file and opens it normally, with its
own writer, readers, lease and WAL. Preferences and workspaces start empty.
The shared bytes are immutable; no live store or connection is shared.

`TestDaemonStoresKeepTheirOwnDataAndLease` checks that one fixture's preferences
and workspaces cannot contaminate another, writes stay independent, and opening
the same file twice still returns `store.ErrBusy`. The deployment and store
package tests continue to exercise fresh creation, upgrades and recovery.
The image is generated from the current checkout, not a checked-in database
that could drift from the migrations.

## Profile a slow case

```sh
go test -race -count=1 -json -timeout 15m \
  -cpuprofile /tmp/tui.cpu -memprofile /tmp/tui.mem \
  -o /tmp/autodoc-tui.test ./tui > /tmp/tui.jsonl
go tool pprof -top -cum /tmp/autodoc-tui.test /tmp/tui.cpu
go tool pprof -alloc_space -top /tmp/autodoc-tui.test /tmp/tui.mem
```

Compare like commands on the same machine without overlapping CPU-heavy work.
Report package runtime separately from compilation and job setup. Parent test
elapsed time includes its subtests: do not add both when estimating a shard.
Keep failure paths and negative assertions intact. Debounce, stale-result and
watcher tests deliberately observe a quiet interval; shortening their sleeps
without a replacement synchronization proof weakens those tests.
