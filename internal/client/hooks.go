package client

import "time"

// The setters below are for tests and diagnostics: nothing in a running program calls them. Each
// is set before the session's first Connect, and none is guarded by the session's mutex.

// SetBeforeCall runs f before each call, on the goroutine making it: the seam that holds one answer
// back while a later one lands.
func (s *Session) SetBeforeCall(f func(method string, params []any)) { s.beforeCall = f }

// SetAfterCall runs f after each call, before its answer is handed back: the seam that holds an
// answer already read while the caller moves on.
func (s *Session) SetAfterCall(f func(method string)) { s.afterCall = f }

// SetProbeWindow is how long Connect keeps dialing after the first refusal (spawnProbeWindow).
func (s *Session) SetProbeWindow(d time.Duration) { s.window = d }

// SetProcess is the process this session's handoffs are written as: another pid makes two sessions
// of one process behave as two processes' would.
func (s *Session) SetProcess(pid int64) { s.self = pid }

// HasHandoff reports whether a restart handoff for sock is kept in stateDir, standing or not.
func HasHandoff(stateDir, sock string) bool {
	_, ok := readHandoff(stateDir, sock)
	return ok
}
