package plugin

import (
	"errors"
	"fmt"
)

// PROTOCOL 7 (docs/decisions/plugins.md#plugin-sessions): a plugin's session slot, the reason it is closed, and a command's
// arguments.
//
//	host → plugin   plugin.open {…, session}       the slot as this plugin left it in this run of AutoDoc
//	                plugin.close {reason}          quit, off, update, remove, failed or closed
//	                plugin.command {id, args}      a command with its arguments
//	plugin → host   host.session {values}          the slot replaced
//
// The host keeps a plugin's slot in its memory, never on disk, until AutoDoc quits (or the plugin is
// removed), and hands it back at each start: a plugin signed in to a service keeps its token there,
// so a restart of the plugin (turned off and on, updated) does not ask again.

// The bounds of a session slot: past any, host.session is refused and the slot kept as it was.
const (
	MaxSessionKeys  = 32
	MaxSessionValue = 8 << 10  // bytes, each value
	MaxSessionBytes = 64 << 10 // keys and values together
)

// The reasons a plugin.close gives (protocol 7). A plugin reads one with Peer.CloseReason inside
// Close. It is best-effort: a plugin that never finished opening, blocks, or has died is stopped
// without one.
const (
	CloseQuit    = "quit"   // AutoDoc is quitting: the slot goes with it
	CloseOff     = "off"    // the user turned the plugin off; the slot stays for its next start
	CloseUpdate  = "update" // its new build is about to start; the slot stays
	CloseRemove  = "remove" // it is being removed: the slot is dropped
	CloseFailure = "failed" // the host stopped it for a fault (no answer, a wrong message)
	CloseClosed  = "closed" // closed as a dialog closes: Esc on a modal one, or its own host.close
)

// ErrSessionBounds is a slot over MaxSessionKeys, MaxSessionValue or MaxSessionBytes.
var ErrSessionBounds = errors.New("plugin: a session slot over its bounds")

// CheckSession is nil when values fit a slot's bounds.
func CheckSession(values map[string]string) error {
	if len(values) > MaxSessionKeys {
		return fmt.Errorf("%w: %d keys, at most %d", ErrSessionBounds, len(values), MaxSessionKeys)
	}
	total := 0
	for k, v := range values {
		if len(v) > MaxSessionValue {
			return fmt.Errorf("%w: a value of %d bytes, at most %d", ErrSessionBounds, len(v), MaxSessionValue)
		}
		total += len(k) + len(v)
	}
	if total > MaxSessionBytes {
		return fmt.Errorf("%w: %d bytes, at most %d", ErrSessionBounds, total, MaxSessionBytes)
	}
	return nil
}

func sessionWire(values map[string]string) map[string]any {
	m := make(map[string]any, len(values))
	for k, v := range values {
		m[k] = v
	}
	return m
}

// SessionParams are host.session's.
func SessionParams(values map[string]string) []any {
	return one(map[string]any{"values": sessionWire(values)})
}

// ReadSession reads host.session: a fresh map, checked against the bounds. Its error never holds a
// value: a slot holds secrets.
func ReadSession(params []any) (map[string]string, error) {
	m, err := arg(params)
	if err != nil {
		return nil, err
	}
	return readSessionMap(m["values"], true)
}

// readSessionMap reads a slot's map; required says an absent one is an error (host.session) rather
// than none (plugin.open).
func readSessionMap(v any, required bool) (map[string]string, error) {
	if v == nil {
		if required {
			return nil, fmt.Errorf("%w: host.session wants values", ErrParams)
		}
		return nil, nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: a session's values are a map", ErrParams)
	}
	out := make(map[string]string, len(raw))
	for k, x := range raw {
		s, ok := x.(string)
		if !ok {
			return nil, fmt.Errorf("%w: a session's values are strings", ErrParams)
		}
		out[k] = s
	}
	if err := CheckSession(out); err != nil {
		return nil, err
	}
	return out, nil
}

// CloseParams are plugin.close's: its reason from protocol 7, none before.
func CloseParams(reason string) []any {
	if reason == "" {
		return EmptyParams()
	}
	return one(map[string]any{"reason": reason})
}

// ReadClose reads plugin.close's reason: "" when it has none.
func ReadClose(params []any) string {
	m, err := arg(params)
	if err != nil {
		return ""
	}
	return str(m, "reason")
}

// CommandArgsParams are plugin.command's with arguments (protocol 7).
func CommandArgsParams(id string, args []string) []any {
	m := map[string]any{"id": id}
	if len(args) > 0 {
		m["args"] = stringsWire(args)
	}
	return one(m)
}

// WithArgs is plugin.command's params, built by CommandParams or FitCommand, with args added
// (protocol 7); params as they are when there are none.
func WithArgs(params []any, args []string) []any {
	if len(args) == 0 || len(params) != 1 {
		return params
	}
	m, ok := params[0].(map[string]any)
	if !ok {
		return params
	}
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out["args"] = stringsWire(args)
	return one(out)
}

// ReadCommandArgs reads plugin.command's arguments: nil when it has none.
func ReadCommandArgs(params []any) []string {
	m, err := arg(params)
	if err != nil {
		return nil
	}
	raw, _ := m["args"].([]any)
	var out []string
	for _, x := range raw {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
