package crew

import (
	"fmt"
	"path/filepath"
)

// SOCKET WAKES (docs/decisions/crew.md#wakes-an-os-cant-offer). Claude Code, Codex's app-server and
// AutoDoc's OpenCode plugin are woken through a Unix socket each binds under the user's runtime
// folders (ClaudeSocketDirs, CodexSocketDirs, OpenCodeSocketDirs). An OS whose paths those folders
// are not can't offer that wake: a locator there names no socket, so none is registered, the
// session is listed with no wake (WhyNoWakeHere), and its mail waits until the agent reads it, as
// for any session a wake can't reach. agy's wake, over loopback, is every OS's.

// SocketWakes reports whether this OS can wake an agent through the socket its CLI binds (a Claude,
// Codex or OpenCode session): the socket folders are paths here, by the standard library's own
// reading of a path (on Windows "/tmp" is not absolute), so crew keeps no OS rule of its own. A
// Manager takes it as its default (WithSocketWakes).
func SocketWakes() bool { return filepath.IsAbs("/tmp") }

// WhyNoWakeHere is why a live session of a kind woken by its socket can't be woken on an OS that
// can't offer that wake (SocketWakes): crew.list's why, in no_endpoint's place.
const WhyNoWakeHere = "no_wake_on_this_os"

// socketWoken reports whether kind's sessions are woken through a socket their CLI binds.
func socketWoken(kind string) bool {
	return kind == KindClaude || kind == KindCodex || kind == KindOpenCode
}

// wakesOn reports whether a session of kind can be woken here.
func (m *Manager) wakesOn(kind string) bool { return m.socketWakes || !socketWoken(kind) }

// errNoWakeHere is a socket locator's refusal on an OS that can't offer the wake.
func errNoWakeHere(kind string) error {
	return fmt.Errorf("%w: %s locator: wakes through a %s socket are not available on this OS yet: the agent's mail waits until it reads it",
		ErrBadEndpoint, kind, kind)
}
