package tui

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

func (h *Host) showMismatch(mismatch *MismatchError) {
	canRestart := mismatch.Server < mismatch.Client && h.session.CanSpawn()
	message := fmt.Sprintf("This TUI speaks protocol %d; autodoc %s speaks protocol %d.", mismatch.Client, mismatch.Version, mismatch.Server)
	if canRestart {
		message += " Restart Now stops the older backend and starts the installed one."
	} else {
		message += " This TUI cannot safely restart that backend; quit and start a compatible autodoc."
	}
	h.set("App.mismatchQuestion", message)
	h.set("App.canRestartMismatch", canRestart)
	if !h.mismatchOpen {
		h.open("mismatch")
		h.mismatchOpen = true
	}
}

func (h *Host) closeMismatch() {
	if h.mismatchOpen {
		h.closeDialog("mismatch")
		h.mismatchOpen = false
	}
}

func (h *Host) restartMismatch() {
	if !h.session.CanSpawn() {
		return
	}
	if h.session.Stale() == nil && !h.connected {
		// the older backend is gone and the installed one did not start: start it again
		h.closeMismatch()
		h.start()
		return
	}
	if h.session.Stale() == nil {
		return
	}
	h.closeMismatch()
	h.mismatchRecovery = true
	h.restartConfirmed()
}

func (h *Host) quitMismatch() {
	h.mismatchOpen = false
	h.quit()
}

// RESTART — System › Restart backend…: the daemon stops, and the TUI's reconnect starts the autodoc
// installed now, so an update takes effect without quitting (AutoDB's SPC X). Indexing and
// embedding pick up where they stopped: their work is kept in the store.

// restartWait bounds waiting for the old daemon to exit before the new one is started.
const restartWait = 15 * time.Second

// startRestart asks first, saying which version runs and which one a restart starts.
func (h *Host) startRestart() {
	if !h.session.CanSpawn() {
		h.notify("restart: this TUI starts no daemon, so nothing would bring one back")
		return
	}
	running := h.session.Version()
	if old := h.session.Stale(); old != nil && !h.connected {
		running = old.Version // older than this TUI, refusing its protocol
	} else if h.session.PID() == 0 || !h.connected {
		h.notify("restart: not connected to a backend")
		return
	}
	do(h, func(context.Context) string {
		if h.installed == nil {
			return ""
		}
		v, err := h.installed()
		if err != nil {
			return "?"
		}
		return v
	}, func(installed string) {
		q := fmt.Sprintf("Restart the backend (autodoc %s)? It stops, and starts again", running)
		switch {
		case installed == "" || installed == "?":
			q += "."
		case installed == running:
			q += " at the same version, " + installed + ": no newer one is installed."
		default:
			q += " as autodoc " + installed + ", the one installed."
		}
		q += " Indexing and embedding carry on where they stopped; every client reconnects."
		h.set("App.restartQuestion", q)
		h.open("restartBackend")
	})
}

// restartConfirmed stops the daemon; the connection's end brings the reconnect (watch), which
// waits for the old process to go and then starts the installed one. An older daemon, which
// refused this TUI's protocol, is stopped over a connection of its own protocol; there is no
// connection to end, so the wait and the start follow here.
func (h *Host) restartConfirmed() {
	if old := h.session.Stale(); old != nil && !h.connected {
		h.notify(fmt.Sprintf("asking the older backend (autodoc %s) to stop…", old.Version))
		do(h, func(ctx context.Context) error {
			if err := h.session.StopStale(ctx); err != nil {
				return err
			}
			if !h.awaitExit(ctx, old.PID) {
				return fmt.Errorf("the older backend (pid %d) has not stopped after %s", old.PID, restartWait)
			}
			return nil
		}, func(err error) {
			if err != nil {
				h.failed("restart", err)
				if h.mismatchRecovery {
					h.set("App.mismatchQuestion", "Restart failed: "+err.Error()+". Retry or quit.")
					h.open("mismatch")
					h.mismatchOpen = true
				}
				return
			}
			h.restartFrom = old.Version
			h.session.forgetStale() // it has stopped: a retry starts the installed one, not stops this again
			h.start()
		})
		return
	}
	pid, from, gen := h.session.PID(), h.session.Version(), h.session.Gen()
	h.restartPID, h.restartFrom = pid, from
	h.notify("asking the backend to stop…")
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "sys.shutdown")
		return err
	}, func(err error) {
		if err != nil && gen == h.session.Gen() {
			h.restartPID, h.restartFrom = 0, ""
			h.failed("restart refused", err)
		}
	})
}

// awaitExit is the Host's wait for a stopped daemon: until its process has gone, restartWait at most.
func awaitExit(ctx context.Context, pid int64) bool { return waitGone(ctx, pid, restartWait) }

// waitGone waits until process pid has exited, or d has passed (false).
func waitGone(ctx context.Context, pid int64, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if !alive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// alive is whether process pid exists: signal 0 checks without sending.
func alive(pid int64) bool {
	p, err := os.FindProcess(int(pid))
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
