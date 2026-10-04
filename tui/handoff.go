package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// HANDOFF — a restart's successor is the requester's build (ADR 0216 §1.4). When the daemon stops,
// every client whose connection ends would start one at once, the requester only after the old
// process is gone, so another client's binary would usually win. Every spawner was the installed
// binary until builds differed in their registrations. Before it stops the daemon, the requester
// writes a handoff beside the daemon's log; Session.Connect, which every spawner in this repository
// goes through (--ui and --call), only dials while another live process's handoff for its socket
// stands. The socket's bind stays the lock: two daemons never serve; the handoff decides which
// build does. A daemon started by hand or by a supervisor does not read it, and the bind settles it.

// handoffSpan is how long a handoff stands: the old process's exit, then the new daemon's start.
// Every build shares restartWait, so a handoff another build wrote is never longer; a deadline
// further away than this is invalid, and ignored.
const handoffSpan = 2 * restartWait

// handoff is a restart under way, as its requester wrote it.
type handoff struct {
	PID      int64  `json:"pid"`
	Socket   string `json:"socket"`   // socketID of the socket it restarts
	Deadline int64  `json:"deadline"` // unix nanoseconds
}

// socketID is the socket's path made one spelling: the directory resolved (the socket itself is
// gone while a restart is under way), then its name. A directory that cannot be resolved (removed
// mid-window, or unreadable) falls back to the cleaned absolute path on both sides; a mismatch fails
// open, since a reader with another id never finds the file and spawns as before.
func socketID(sock string) string {
	abs, err := filepath.Abs(sock)
	if err != nil {
		return filepath.Clean(sock)
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(dir, filepath.Base(abs))
	}
	return filepath.Clean(abs)
}

// handoffPath is where the handoff for sock is kept: one per socket, since server.socket may differ
// while the state directory is shared.
func handoffPath(stateDir, sock string) string {
	sum := sha256.Sum256([]byte(socketID(sock)))
	return filepath.Join(stateDir, "restart-handoff-"+hex.EncodeToString(sum[:6]))
}

// errHandoffBusy is the handoff's lock held by another, asked for without waiting.
var errHandoffBusy = errors.New("tui: the restart handoff is being changed")

// beforeHandoffRemove, when set (tests only), runs inside a removal, between its read and its
// unlink: the seam a test replaces the handoff through.
var beforeHandoffRemove func()

// writeHandoff records that process pid is restarting the daemon on sock until now+handoffSpan,
// atomically, under the handoff's lock (lockHandoff): a requester's write never lands inside
// another's removal.
func writeHandoff(stateDir, sock string, pid int64, now time.Time) error {
	return writeHandoffLocked(stateDir, sock, pid, now, true)
}

func writeHandoffLocked(stateDir, sock string, pid int64, now time.Time, wait bool) error {
	b, err := json.Marshal(handoff{PID: pid, Socket: socketID(sock), Deadline: now.Add(handoffSpan).UnixNano()})
	if err != nil {
		return err
	}
	path := handoffPath(stateDir, sock)
	release, err := lockHandoff(path, wait)
	if err != nil {
		return err
	}
	defer release()
	tmp, err := os.CreateTemp(stateDir, ".restart-handoff-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// WriteHandoff announces that process pid is restarting the daemon on sock, as a TUI does before it
// stops one: until the handoff's deadline, every other client of sock only dials. A launcher other
// than the TUI restarting the daemon announces it so.
func WriteHandoff(stateDir, sock string, pid int64) error {
	return writeHandoff(stateDir, sock, pid, time.Now())
}

// removeHandoff removes the handoff for sock if pid wrote it: another requester's is left alone.
// The read, the check and the unlink run whole under the handoff's lock, so another requester's
// handoff renamed into place meanwhile is never the one removed.
func removeHandoff(stateDir, sock string, pid int64) {
	path := handoffPath(stateDir, sock)
	release, err := lockHandoff(path, true)
	if err != nil {
		return // the deadline retires it: a reader ignores a lapsed handoff
	}
	defer release()
	h, ok := readHandoff(stateDir, sock)
	if beforeHandoffRemove != nil {
		beforeHandoffRemove()
	}
	if ok && h.PID == pid {
		_ = os.Remove(path)
	}
}

func readHandoff(stateDir, sock string) (handoff, bool) {
	b, err := os.ReadFile(handoffPath(stateDir, sock))
	if err != nil {
		return handoff{}, false
	}
	var h handoff
	if json.Unmarshal(b, &h) != nil {
		return handoff{}, false
	}
	return h, true
}

// holdsBack reports whether h stands for a reader, process self, connecting to sock now: another
// process's, still alive, for this socket, with a deadline still ahead and no further than
// handoffSpan. A dead process, a past or invalid deadline, or another socket's costs nothing.
func (h handoff) holdsBack(self int64, sock string, now time.Time) bool {
	if h.PID == self || h.Socket != socketID(sock) {
		return false
	}
	deadline := time.Unix(0, h.Deadline)
	if !now.Before(deadline) || deadline.Sub(now) > handoffSpan {
		return false
	}
	return alive(h.PID)
}
