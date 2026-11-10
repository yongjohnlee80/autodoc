# Platform decisions

How AutoDoc keeps what only one operating system can do in one place, so that a port to another OS
is a list of packages to implement, not a search through the code.

## Each OS concern is one package

Decided 2026-11-09, for every OS AutoDoc runs on now or later: each OS concern is its own small package with one OS-neutral API, and the
rest of the code uses only that API.
- **Per-OS code lives only inside its package**, in `*_unix.go`, `*_linux.go`, `*_darwin.go` or
  `*_windows.go` files behind build tags. What the OSes share is written once, in the package's
  neutral file.
- **Each package has one contract**, a conformance cell written once and run on every OS.
- **A missing implementation fails the build.** Each package has an `unsupported.go`, built only
  when no implementation matches, that names the package in a compile error. There is no stub that
  compiles and does nothing.
- **Nothing outside those packages** imports `golang.org/x/sys/unix` or `golang.org/x/sys/windows`,
  uses `syscall`'s names (`SysProcAttr`, `Kill`, `Flock`, `Statfs`, `Stat_t`, signals other than
  `os.Interrupt` and `os.Kill`), dials or listens on `"unix"`, reads `runtime.GOOS`, or is a file of
  one OS.

The packages, the planned seams (their design is pending):

| Concern | Package |
|---|---|
| The interprocess connection and its same-user check | `internal/ipc` |
| Process trees: start, signal, stop | `internal/proctree` |
| An exclusive file lock | `internal/filelock` |
| Free disk space | `internal/diskspace` |
| The user's folders: home, cache, temporary | `internal/userdirs` |
| The default shell | `internal/usershell` |
| The environment, and the programs on its PATH | `internal/procenv` |
| What this process uses: CPU, resident memory | `core/procstat` |
| What this machine offers: RAM, GPU | `core/hostprobe` |

`internal/diskspace`, `internal/filelock`, `internal/userdirs` and `internal/procenv` exist; the others are made as their code moves behind
them.

A guard reads every non-test Go file of the module, whatever its build tags, and lists each such use
outside the seam packages in a ledger. The ledger is exact in both directions: a new use fails until
it moves behind its seam, and a use that is gone fails until its row is removed. So the list only
shrinks, and empty is the destination. A test file may use them (a skip on one OS, say).

In the code: `internal/osaudit/os_calls_test.go`, `internal/osaudit/testdata/os_calls.txt`,
`internal/procenv/procenv.go`, `internal/userdirs/userdirs.go`
