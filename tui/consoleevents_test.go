package tui

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

// watchConsoleEvents makes a console event say so before it ends the test binary on Windows. Go
// delivers a console's Ctrl+C and Ctrl+Break as os.Interrupt and its Close, logoff and shutdown as
// SIGTERM; with nothing notified, the default handler ends the process with no word, and go test
// prints no exit status for a binary that wrote anything, so the job's log showed only FAIL (a ./tui
// run that ended at 28 s, 2026-11-20). Notified here, the event is printed with every goroutine's
// stack, and the binary exits 3: the next such end names its cause, and an end with no word again
// was something else (TerminateProcess, or a fail-fast in a system DLL). Only the test process
// itself watches: the helper modes this binary also runs as (agents, shells, plugins) have exited
// before TestMain calls this.
func watchConsoleEvents() {
	if runtime.GOOS != "windows" {
		return
	}
	events := make(chan os.Signal, 1)
	signal.Notify(events, os.Interrupt, syscall.SIGTERM)
	go func() {
		ev := <-events
		stacks := make([]byte, 64<<20)
		stacks = stacks[:runtime.Stack(stacks, true)]
		fmt.Fprintf(os.Stderr, "the test binary got %v (a console's Ctrl+C or Break is interrupt; its Close, logoff or shutdown is terminated); every goroutine:\n%s\n", ev, stacks)
		os.Exit(3)
	}()
}
