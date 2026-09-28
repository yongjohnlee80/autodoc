package tui

import (
	"context"
	"errors"
	"fmt"
)

// CONNECTING — once when the program starts, and again whenever the connection ends.

// start connects, then lists the workspaces. It runs on the loop, once the program runs.
func (h *Host) start() {
	h.setWhere("autodoc [connecting]")
	h.setStatus("connecting to " + h.session.addr + "…")
	do(h, h.session.Connect, func(err error) {
		if err != nil {
			h.setWhere("autodoc [disconnected]")
			// the status line has room for the reason; About has room for the paths
			var ce *ConnectError
			if errors.As(err, &ce) {
				h.setStatus(fmt.Sprintf("connect failed: no daemon answered in %s (Help › About)", ce.Window))
			} else {
				h.setStatus("connect failed (Help › About)")
			}
			h.set("App.aboutText", h.aboutText()+"\n\nThe last connect failed:\n"+err.Error())
			return
		}
		h.setStatus("connected — autodoc " + h.session.Version())
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
		h.setStatus("disconnected — reconnecting…")
		h.start()
	})
}

// call calls the daemon from a worker, with the connection there is now.
func (h *Host) call(ctx context.Context, method string, params ...any) (any, error) {
	return h.session.Call(ctx, method, params...)
}

// failed shows a failed call on the status line.
func (h *Host) failed(what string, err error) {
	h.setStatus(fmt.Sprintf("%s: %s", what, wireMessage(err)))
}
