package tui

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/autodoc/internal/client"
)

func (h *Host) showMismatch(mismatch *MismatchError) {
	canRestart := mismatch.Server < mismatch.Client && h.session.CanSpawn()
	var message string
	old := h.session.Stale()
	switch {
	case canRestart && h.restartAccepted && old != nil:
		// an older backend answered again after a restart this TUI asked for: something else starts
		// it (a supervisor's binary, say); offered again, the restart would loop
		canRestart = false
		message = h.trf("autodoc.text.restart.this_tui_speaks_protocol_autodoc_speaks_protocol_again",
			mismatch.Client, mismatch.Version, mismatch.Server, old.PID)
	case canRestart:
		message = h.trf("autodoc.text.restart.this_tui_speaks_protocol_autodoc_speaks_protocol_restart",
			mismatch.Client, mismatch.Version, mismatch.Server)
	default:
		message = h.trf("autodoc.text.restart.this_tui_speaks_protocol_autodoc_speaks_protocol_cannot",
			mismatch.Client, mismatch.Version, mismatch.Server)
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
const restartWait = client.RestartWait

// startRestart asks first, saying which version runs and which one a restart starts.
func (h *Host) startRestart() {
	if !h.session.CanSpawn() {
		h.notify(h.tr("autodoc.notice.restart.restart_this_tui_starts_no_daemon_so_nothing_wou"))
		return
	}
	running := h.session.Version()
	if old := h.session.Stale(); old != nil && !h.connected {
		running = old.Version // older than this TUI, refusing its protocol
	} else if h.session.PID() == 0 || !h.connected {
		h.notify(h.tr("autodoc.notice.restart.restart_not_connected_to_a_backend"))
		return
	}
	do(h, func(context.Context) answerOf[string] {
		if h.installed == nil {
			return answered("", nil)
		}
		v, err := h.installed()
		if err != nil {
			return answered("?", nil)
		}
		return answered(v, nil)
	}, func(a answerOf[string]) {
		installed := a.v
		if a.err != nil {
			installed = "?" // reading it failed (it panicked): not known, as when it errs
		}
		var q string
		switch {
		case installed == "" || installed == "?":
			q = h.trf("autodoc.text.restart.restart_the_backend_autodoc_it_stops_and_starts", running)
		case installed == running:
			q = h.trf("autodoc.text.restart.restart_the_backend_autodoc_it_stops_and_starts_same", running, installed)
		default:
			q = h.trf("autodoc.text.restart.restart_the_backend_autodoc_it_stops_and_starts_as", running, installed)
		}
		h.ask(Question{Title: h.tr("autodoc.question.restart.restart_the_backend"), Text: q, OnYes: h.restartConfirmed})
	})
}

// restartConfirmed stops the daemon; the connection's end brings the reconnect (watch), which
// waits for the old process to go and then starts the installed one. An older daemon, which
// refused this TUI's protocol, is stopped over a connection of its own protocol; there is no
// connection to end, so the wait and the start follow here.
func (h *Host) restartConfirmed() {
	h.restartAccepted = true
	// until this TUI's daemon answers, another client only dials, so the build that serves next is
	// this one (handoff.go); a handoff that cannot be written fails open, as before
	if err := h.session.WriteHandoff(); err != nil {
		h.failed(h.tr("autodoc.notice.restart.restart_the_handoff_to_the_other_clients_was_not"), err)
	}
	if old := h.session.Stale(); old != nil && !h.connected {
		h.notify(h.trf("autodoc.notice.restart.asking_the_older_backend_autodoc_to_stop", old.Version))
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
				h.session.RemoveHandoff()
				h.failed(h.tr("autodoc.notice.restart.restart"), err)
				if h.mismatchRecovery {
					h.set("App.mismatchQuestion", h.trf("autodoc.text.restart.restart_failed_retry_or_quit", err.Error()))
					h.open("mismatch")
					h.mismatchOpen = true
				}
				return
			}
			h.restartFrom = old.Version
			h.session.ForgetStale() // it has stopped: a retry starts the installed one, not stops this again
			h.start()
		})
		return
	}
	pid, from, gen := h.session.PID(), h.session.Version(), h.session.Gen()
	h.restartPID, h.restartFrom = pid, from
	h.notify(h.tr("autodoc.notice.restart.asking_the_backend_to_stop"))
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "sys.shutdown")
		return err
	}, func(err error) {
		if err != nil && gen == h.session.Gen() {
			h.session.RemoveHandoff()
			h.restartPID, h.restartFrom = 0, ""
			h.failed(h.tr("autodoc.notice.restart.restart_refused"), err)
		}
	})
}

// awaitExit is the Host's wait for a stopped daemon: until its process has gone, restartWait at most.
func awaitExit(ctx context.Context, pid int64) bool { return client.WaitGone(ctx, pid, restartWait) }
