package crew

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
)

// OpenCodeWaker wakes an OpenCode session through AutoDoc's plugin in its opencode process (§4.7,
// rationale §R15): the plugin listens on a unix socket and, asked to wake its session, starts a
// turn with client.session.promptAsync, which a busy session takes in at its next step. Nothing is
// typed into the TUI. Before every wake the socket is bound to the session's own process, as for
// Claude (boundTo): a socket a dead opencode left, or one at a pid a later process holds, is never
// trusted. The plugin wakes with its own fixed notice, never text it is sent, and refuses a session
// that is not its own top-level one, so a stray connection can do no more than a spurious wake.
type OpenCodeWaker struct {
	roots []string
}

// NewOpenCodeWaker is the OpenCode waker for the daemon's user uid.
func NewOpenCodeWaker(uid int) *OpenCodeWaker { return &OpenCodeWaker{roots: OpenCodeSocketDirs(uid)} }

// opencodeMaxAnswer bounds the plugin's answer: one short JSON line.
const opencodeMaxAnswer = 4 << 10

// opencodeAnswer is the plugin's one line: ok, or why not. Refused is a wake it did not act on (not
// its session, a request it could not read); Error is promptAsync failing, after it was asked.
type opencodeAnswer struct {
	OK      bool   `json:"ok"`
	Refused string `json:"refused"`
	Error   string `json:"error"`
}

// Wake asks the plugin holding ep's session to wake it. Its outcomes:
//
//	the socket outside the plugin's folders, its dial, or its binding fails  failed: nothing sent
//	the request's write fails                                                 failed: the plugin acts only on a whole line
//	the plugin's answer refuses (not its session, an unreadable request)      failed: it did nothing
//	the plugin's answer is ok                                                 accepted: promptAsync returned
//	promptAsync failed, or no answer, one cut short, malformed or too long    ErrWakeUnknown: the turn may be queued
//
// notice is the scheduler's (WakeNotice); the plugin wakes with its own copy of it, never text it
// is sent, so the request names the session alone.
func (w *OpenCodeWaker) Wake(ctx context.Context, ep Endpoint, _ string) error {
	var l opencodeLocator
	if err := strictJSON(ep.Locator, &l); err != nil {
		return fmt.Errorf("%w: opencode locator: %v", ErrBadEndpoint, err)
	}
	if !under(l.Socket, w.roots...) {
		return fmt.Errorf("%w: opencode socket %s lies outside the plugin's socket folders", ErrBadEndpoint, l.Socket)
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", l.Socket)
	if err != nil {
		// %v, not %w: nothing was sent, so even a deadline here is a failure, never an unknown
		return fmt.Errorf("crew: dialing opencode's plugin socket: %v", err)
	}
	defer c.Close()
	if err := boundTo(c, l.Start, "opencode's plugin socket"); err != nil {
		return err
	}
	b, err := json.Marshal(map[string]string{"session": l.Session})
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	if _, err := c.Write(append(b, '\n')); err != nil {
		// the plugin acts on a whole line alone: a request cut short is one it never acts on
		return fmt.Errorf("crew: writing to opencode's plugin socket: %v", err)
	}
	line, err := bufio.NewReaderSize(c, opencodeMaxAnswer).ReadSlice('\n')
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, bufio.ErrBufferFull) {
			return fmt.Errorf("%w: opencode's plugin did not answer in time or at length: %v", ErrWakeUnknown, err)
		}
		return fmt.Errorf("%w: opencode's plugin answered no whole line: %v", ErrWakeUnknown, err)
	}
	var a opencodeAnswer
	if err := json.Unmarshal(line, &a); err != nil {
		return fmt.Errorf("%w: opencode's plugin answered %q", ErrWakeUnknown, truncate(line, 80))
	}
	switch {
	case a.OK:
		return nil
	case a.Refused != "":
		return fmt.Errorf("crew: opencode's plugin refused the wake: %s", truncate([]byte(a.Refused), 200))
	}
	return fmt.Errorf("%w: opencode's plugin could not start the turn: %s", ErrWakeUnknown, truncate([]byte(a.Error), 200))
}

// truncate is b, cut to at most n bytes for an error's text.
func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}

var _ Waker = (*OpenCodeWaker)(nil)
