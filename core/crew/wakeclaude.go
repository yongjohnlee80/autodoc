package crew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
)

// ClaudeWaker wakes a Claude Code session through its inbox socket
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): the documented cross-session messaging
// channel, one JSON line per message. An idle session starts a turn; a busy one reads it at its
// next tool round, its draft left alone. Before every wake the socket is bound to the session's own
// process: its path lies in Claude's socket folders for the daemon's user, and the process at the
// other end (the kernel's record, not the path) is the registered pid, still the same process (its
// reference). A wrong or vanished holder fails the wake as gone (ErrEndpointGone), and the
// session's next SessionStart registers its new socket.
type ClaudeWaker struct {
	roots []string // the socket folders a locator must lie in
	peer  PeerProc // the process at a socket's other end
}

// NewClaudeWaker is the Claude waker for the daemon's user uid, binding sockets with peer.
func NewClaudeWaker(uid int, peer PeerProc) *ClaudeWaker {
	return &ClaudeWaker{roots: ClaudeSocketDirs(uid), peer: peer}
}

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
		// nothing was sent, so even a deadline here is a failure, never an unknown; no socket, or
		// nothing listening on it, is the session's process gone
		return dialFailure("claude's inbox socket", err)
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

// bound checks that c's other end is the session's process (boundTo).
func (w *ClaudeWaker) bound(c net.Conn, l claudeLocator) error {
	return boundTo(c, w.peer, l.Start, "claude's inbox socket")
}

// boundTo checks that the other end of c, a unix socket, is the process start names: the daemon's
// user, and the pid start names, still the process it named at registration. A socket a dead
// process left, or one its pid's later holder binds, is refused alike.
func boundTo(c net.Conn, peer PeerProc, start, what string) error {
	if peer == nil {
		return fmt.Errorf("crew: no way to check the process at %s", what) // never trust an unchecked socket
	}
	ref, err := peer(c)
	if err != nil {
		return err
	}
	// the reference names the pid too (registration made start the pid's): one comparison refuses
	// another process and a later one at the same pid alike, and the session's is not the one there
	if ref != start {
		return fmt.Errorf("%w: %s is held by process %s, not the session's %s", ErrEndpointGone, what, ref, start)
	}
	return nil
}

var _ Waker = (*ClaudeWaker)(nil)
