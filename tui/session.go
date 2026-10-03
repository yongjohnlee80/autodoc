package tui

import (
	"context"
	"errors"
	"fmt"
)

// CONNECTING — once when the program starts, and again whenever the connection ends.

// start connects, then lists the workspaces. It runs on the loop, once the program runs.
func (h *Host) start() {
	h.setConnected(false)
	h.setWhere("autodoc [connecting]")
	h.notifyOngoing(toastConnection, "connecting to "+h.session.addr+"…")
	do(h, h.session.Connect, func(err error) {
		if err != nil {
			h.setWhere("autodoc [disconnected]")
			// the status line has room for the reason; About has room for the paths
			var ce *ConnectError
			var me *MismatchError
			switch {
			case errors.As(err, &me) && me.Server < me.Client:
				h.setWhere("autodoc [older backend]")
				h.notifyOngoing(toastConnection, fmt.Sprintf("the backend is autodoc %s (protocol %d), older than this TUI (%d): System › Restart backend… starts the installed one",
					me.Version, me.Server, me.Client))
				h.showMismatch(me)
			case errors.As(err, &me):
				h.setWhere("autodoc [newer backend]")
				h.notifyOngoing(toastConnection, fmt.Sprintf("this TUI (protocol %d) is older than the backend, autodoc %s (%d): quit and start the installed autodoc",
					me.Client, me.Version, me.Server))
				h.showMismatch(me)
			case errors.As(err, &ce):
				h.notifyOngoing(toastConnection, fmt.Sprintf("connect failed: no daemon answered in %s (Help › About)", ce.Window))
			default:
				h.notifyOngoing(toastConnection, "connect failed (Help › About)")
			}
			h.set("App.aboutText", h.aboutText()+"\n\nThe last connect failed:\n"+err.Error())
			return
		}
		h.closeMismatch()
		h.mismatchRecovery = false
		if h.restartFrom != "" {
			h.notifyDone(toastConnection, fmt.Sprintf("backend restarted: autodoc %s → %s", h.restartFrom, h.session.Version()))
			h.restartFrom, h.restartPID = "", 0
		} else {
			h.notifyDone(toastConnection, "connected — autodoc "+h.session.Version())
		}
		h.setConnected(true)
		h.entered = false // a new connection enters its workspace again, as the first did
		h.loadPrefs()
		h.loadWorkspaces()
		h.watch()
	})
}

// watch waits for the connection to end, then connects again. The epoch moves, so what was asked
// of the old connection is dropped when it answers.
func (h *Host) watch() {
	done, gen := h.session.Done(), h.session.Gen()
	do(h, func(ctx context.Context) bool {
		select {
		case <-done:
			return true
		case <-ctx.Done():
			return false
		}
	}, func(ended bool) {
		if !ended || h.session.Gen() != gen {
			return
		}
		h.epoch++
		h.setWhere("autodoc [disconnected]")
		if pid := h.restartPID; pid != 0 {
			// a restart: the old daemon lets go of its workspaces after its socket, so the new one
			// starts once the old process is gone, or it would find them busy
			h.notifyOngoing(toastConnection, "restarting the backend: waiting for the old one to stop…")
			do(h, func(ctx context.Context) bool { return h.awaitExit(ctx, pid) }, func(gone bool) {
				if !gone {
					h.notify(fmt.Sprintf("the old backend (pid %d) has not stopped after %s: reconnecting anyway", pid, restartWait))
				}
				h.start()
			})
			return
		}
		h.notifyOngoing(toastConnection, "disconnected — reconnecting…")
		h.start()
	})
}

// call calls the daemon from a worker, with the connection there is now.
func (h *Host) call(ctx context.Context, method string, params ...any) (any, error) {
	return h.session.Call(ctx, method, params...)
}

// failed shows a failed call on the status line.
func (h *Host) failed(what string, err error) {
	h.notify(fmt.Sprintf("%s: %s", what, wireMessage(err)))
}
