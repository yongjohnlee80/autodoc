package tui

import (
	"context"
	"errors"
)

// CONNECTING — once when the program starts, and again whenever the connection ends.

// start connects, then lists the workspaces. It runs on the loop, once the program runs.
func (h *Host) start() {
	h.setConnected(false)
	h.setWhere("autodoc [connecting]")
	h.notifyOngoing(toastConnection, h.trf("autodoc.notice.session.connecting_to", h.session.Address()))
	do(h, h.session.Connect, func(err error) {
		if err != nil {
			h.setWhere("autodoc [disconnected]")
			// the status line has room for the reason; About has room for the paths
			var ce *ConnectError
			var me *MismatchError
			switch {
			case errors.As(err, &me) && me.Server < me.Client:
				h.setWhere("autodoc [older backend]")
				h.notifyOngoing(toastConnection, h.trf("autodoc.notice.session.the_backend_is_autodoc_protocol_older_than_this", me.Version, me.Server, me.Client))
				h.showMismatch(me)
			case errors.As(err, &me):
				h.setWhere("autodoc [newer backend]")
				h.notifyOngoing(toastConnection, h.trf("autodoc.notice.session.this_tui_protocol_is_older_than_the_backend_auto", me.Client, me.Version, me.Server))
				h.showMismatch(me)
			case errors.As(err, &ce):
				h.notifyOngoing(toastConnection, h.trf("autodoc.notice.session.connect_failed_no_daemon_answered_in_help_about", ce.Window))
			default:
				h.notifyOngoing(toastConnection, h.tr("autodoc.notice.session.connect_failed_help_about"))
			}
			h.set("App.aboutText", h.trf("autodoc.text.session.the_last_connect_failed", h.aboutText(), err.Error()))
			h.set("App.aboutError", h.trf("autodoc.text.session.the_last_connect_failed_2", err.Error()))
			if h.mismatchRecovery && !errors.As(err, &me) {
				// the older backend stopped, and the installed one did not come up: the recovery stays
				// in its dialog, with the reason, and Restart Now tries the start again (ADR 0212 §8)
				h.set("App.mismatchQuestion", h.trf("autodoc.text.session.the_installed_backend_did_not_start_restart_now", wireMessage(err)))
				h.set("App.canRestartMismatch", h.session.CanSpawn())
				if !h.mismatchOpen {
					h.open("mismatch")
					h.mismatchOpen = true
				}
			}
			return
		}
		h.closeMismatch()
		h.mismatchRecovery = false
		if h.restartFrom != "" {
			h.notifyDone(toastConnection, h.trf("autodoc.notice.session.backend_restarted_autodoc", h.restartFrom, h.session.Version()))
			h.restartFrom, h.restartPID = "", 0
		} else {
			h.notifyDone(toastConnection, h.trf("autodoc.notice.session.connected_autodoc", h.session.Version()))
		}
		h.session.RemoveHandoff() // a restart this TUI asked for has its daemon: the others may spawn again
		h.setConnected(true)
		h.checkRegistrations()
		h.entered = false // a new connection enters its workspace again, as the first did
		h.loadPrefs()
		h.loadStageRanker(false)
		h.loadWorkspaces()
		h.followEvents()
		h.watch()
		h.p.Post(h.offerRecovery) // once a start: unsaved work kept when AutoDoc last ended
		h.reg.connected()
	})
}

// watch waits for the connection to end, then connects again. The epoch moves, so what was asked
// of the old connection is dropped when it answers.
func (h *Host) watch() {
	done, gen := h.session.Done(), h.session.Gen()
	do(h, func(ctx context.Context) answerOf[bool] {
		select {
		case <-done:
			return answered(true, nil)
		case <-ctx.Done():
			return answered(false, nil)
		}
	}, func(a answerOf[bool]) {
		// the watch failing (it panicked) is the connection's watch gone: connected again, as when
		// the connection ends, rather than never watched again
		ended := a.v || a.err != nil
		if !ended || h.session.Gen() != gen {
			return
		}
		h.epoch++
		h.setWhere("autodoc [disconnected]")
		if pid := h.restartPID; pid != 0 {
			// a restart: the old daemon lets go of its workspaces after its socket, so the new one
			// starts once the old process is gone, or it would find them busy
			h.notifyOngoing(toastConnection, h.tr("autodoc.notice.session.restarting_the_backend_waiting_for_the_old_one_t"))
			do(h, func(ctx context.Context) answerOf[bool] { return answered(h.awaitExit(ctx, pid), nil) }, func(a answerOf[bool]) {
				if !a.v { // not seen to stop, or the wait failed: reconnected anyway
					h.notify(h.trf("autodoc.notice.session.the_old_backend_pid_has_not_stopped_after_reconn", pid, restartWait))
				}
				h.start()
			})
			return
		}
		h.notifyOngoing(toastConnection, h.tr("autodoc.notice.session.disconnected_reconnecting"))
		h.start()
	})
}

// call calls the daemon from a worker, with the connection there is now.
func (h *Host) call(ctx context.Context, method string, params ...any) (any, error) {
	return h.session.Call(ctx, method, params...)
}

// failed shows a failed call on the status line.
func (h *Host) failed(what string, err error) {
	h.notify(h.trf("autodoc.notice.session.text", what, wireMessage(err)))
}
