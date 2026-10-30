package crew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/yongjohnlee80/autodoc/internal/peercred"
)

// ClaudeWaker wakes a Claude Code session through its inbox socket (§4.7, rationale §R15): the
// documented cross-session messaging channel, one JSON line per message. An idle session starts a
// turn; a busy one reads it at its next tool round, its draft left alone. Before every wake the
// socket is bound to the session's own process: its path lies in Claude's socket folders for the
// daemon's user, and the process at the other end (the kernel's record, not the path) is the
// registered pid, still the same process (its reference). A wrong or vanished holder fails the wake,
// and the session's next SessionStart registers its new socket.
type ClaudeWaker struct {
	roots []string // the socket folders a locator must lie in
}

// NewClaudeWaker is the Claude waker for the daemon's user uid.
func NewClaudeWaker(uid int) *ClaudeWaker { return &ClaudeWaker{roots: ClaudeSocketDirs(uid)} }

// claudeLine is the one line a wake writes: a user message with the notice, from autodoc.
type claudeLine struct {
	Type    string `json:"type"`
	From    string `json:"from"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
}

func (w *ClaudeWaker) Wake(ctx context.Context, ep Endpoint, notice string) error {
	var l claudeLocator
	if err := strictJSON(ep.Locator, &l); err != nil {
		return fmt.Errorf("%w: claude locator: %v", ErrBadEndpoint, err)
	}
	if !under(l.Socket, w.roots...) {
		return fmt.Errorf("%w: claude socket %s lies outside Claude's socket folders", ErrBadEndpoint, l.Socket)
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", l.Socket)
	if err != nil {
		// %v, not %w: nothing was sent, so even a deadline here is a failure, never an unknown
		return fmt.Errorf("crew: dialing claude's inbox socket: %v", err)
	}
	defer c.Close()
	if err := w.bound(c, l); err != nil {
		return err
	}
	line := claudeLine{Type: "user", From: "autodoc"}
	line.Message.Role, line.Message.Content = "user", notice
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetWriteDeadline(dl)
	}
	if _, err := c.Write(append(b, '\n')); err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return fmt.Errorf("%w: %v", ErrWakeUnknown, err) // part of the line may have gone
		}
		return fmt.Errorf("crew: writing to claude's inbox socket: %v", err)
	}
	return nil
}

// bound checks that c's other end is the session's process: the daemon's user, the registered pid,
// and still the process that pid named at registration.
func (w *ClaudeWaker) bound(c net.Conn, l claudeLocator) error {
	if err := peercred.Check(c); err != nil {
		return err
	}
	pid, err := peercred.PeerPID(c)
	if err != nil {
		return err
	}
	if pid != l.PID {
		return fmt.Errorf("crew: claude's inbox socket is held by pid %d, not the session's %d", pid, l.PID)
	}
	ref, err := ProcRef(pid)
	if err != nil {
		return err
	}
	if ref != l.Start {
		return fmt.Errorf("crew: pid %d is now %s, not the session's process %s", pid, ref, l.Start)
	}
	return nil
}

var _ Waker = (*ClaudeWaker)(nil)
