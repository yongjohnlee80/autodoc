package crew

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// CodexWaker wakes a Codex session through its app-server's persistent queue
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): `codex queue`, run as argv, never through
// a shell. An idle thread runs the notice at once, a busy one after its turn, an unloaded one at
// its next resume; a TUI attached to the app-server shows it with its draft left alone. Before
// every wake the server is bound to the daemon's user: a socket (not a link to one) in Codex's
// socket folder for that user, owned by it, in a folder only it can enter. Each value is passed as
// --flag=value, so a thread id can never be read as a flag.
type CodexWaker struct {
	bin   string   // the codex CLI
	uid   int      // the daemon's user, who must own the server and its folder
	roots []string // the socket folders a locator must lie in
}

// NewCodexWaker is the Codex waker for the daemon's user uid, running the codex CLI at bin.
func NewCodexWaker(uid int, bin string) *CodexWaker {
	return &CodexWaker{bin: bin, uid: uid, roots: CodexSocketDirs(uid)}
}

func (w *CodexWaker) Wake(ctx context.Context, ep Endpoint, notice string) error {
	var l codexLocator
	if err := strictJSON(ep.Locator, &l); err != nil {
		return fmt.Errorf("%w: codex locator: %v", ErrBadEndpoint, err)
	}
	if !under(l.Server, w.roots...) || !threadOK(l.Thread) {
		return fmt.Errorf("%w: codex locator %s", ErrBadEndpoint, ep.Locator)
	}
	if err := w.bound(l.Server); err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, w.bin, "queue", "--remote=unix://"+l.Server, "--thread="+l.Thread, "--message="+notice)
	cmd.Stderr = &stderr
	// a child codex leaves behind (holding stderr) must not keep the call past its deadline: once
	// the process is killed, Run returns this soon after, whatever still holds the pipe
	cmd.WaitDelay = codexWaitDelay
	err := cmd.Run()
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		// killed at the deadline: the queue may have taken the notice already
		return fmt.Errorf("%w: codex queue: %v", ErrWakeUnknown, ctx.Err())
	}
	msg := strings.TrimSpace(stderr.String())
	if len(msg) > 512 {
		msg = msg[len(msg)-512:]
	}
	return fmt.Errorf("crew: codex queue: %v: %s", err, msg)
}

// bound checks that server is the daemon's user's app-server socket: a socket itself, owned by the
// user, in a folder of the user's that nobody else can enter.
func (w *CodexWaker) bound(server string) error {
	fi, err := os.Lstat(server)
	if err != nil {
		return fmt.Errorf("crew: codex's app-server socket: %w", err)
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("crew: codex's app-server %s is not a socket (%s)", server, fi.Mode().Type())
	}
	if err := w.ownedBy(fi); err != nil {
		return fmt.Errorf("crew: codex's app-server %s: %w", server, err)
	}
	dir := filepath.Dir(server)
	di, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !di.IsDir() || di.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("crew: codex's socket folder %s is not a folder only its user can enter (%s)", dir, di.Mode())
	}
	if err := w.ownedBy(di); err != nil {
		return fmt.Errorf("crew: codex's socket folder %s: %w", dir, err)
	}
	return nil
}

func (w *CodexWaker) ownedBy(fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("no owner on this platform")
	}
	if int(st.Uid) != w.uid {
		return fmt.Errorf("owned by uid %d, not the daemon's %d", st.Uid, w.uid)
	}
	return nil
}

// codexWaitDelay is how long Run waits for codex's output pipe after the process has gone.
const codexWaitDelay = time.Second

var _ Waker = (*CodexWaker)(nil)
