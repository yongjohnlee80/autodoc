package tui

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// REGISTRATIONS — a build's chunkers and formats (ADR 0216). Builds share one store and one socket,
// so the daemon this TUI finds may be another build's. Kinds are always the daemon's: it is the one
// indexing. A TUI whose binary registers more offers to restart the daemon as its own build, once a
// session and only forward: never when the daemon's registrations are not a strict subset of its
// own, so a community TUI never downgrades a Pro daemon, and two builds never flip it back and
// forth. Tables that are not comparable (a shared extension at another version) get a word on the
// status line; System › Restart backend… is how a newer build takes over then.

// checkRegistrations asks the daemon just connected for its registrations, and takes its kinds. The
// answer is the connection's, whichever workspace is entered meanwhile: a reconnect drops it.
func (h *Host) checkRegistrations() {
	gen := h.session.Gen()
	do(h, func(ctx context.Context) (answer struct {
		t   registrations.Tables
		err error
	}) {
		caps, err := h.call(ctx, "sys.capabilities")
		if err != nil {
			answer.err = err
			return
		}
		answer.t, answer.err = rpc.RegistrationsOf(caps)
		return
	}, func(a struct {
		t   registrations.Tables
		err error
	}) {
		if gen != h.session.Gen() {
			return
		}
		if a.err != nil {
			h.daemonTables, h.kinds = registrations.Tables{}, kind.Registrations{}
			h.notify("the backend's registrations: " + wireMessage(a.err))
			return
		}
		h.daemonTables = a.t
		h.kinds = kind.Registrations{Chunked: slices.Sorted(maps.Keys(a.t.Chunkers))}
		h.compareRegistrations()
	})
}

// compareRegistrations decides what this TUI says of the daemon's registrations against its own.
func (h *Host) compareRegistrations() {
	own, theirs := h.ownTables, h.daemonTables
	if own.Fingerprint() == theirs.Fingerprint() {
		return
	}
	lacks := strings.Join(theirs.Lacks(own), ", ")
	switch {
	case theirs.StrictSubsetOf(own) && h.registrationRestart:
		// a restart this TUI asked for, answered by a daemon that still lacks them: another build
		// serves this store (a supervisor's, or one started by hand); say which, and ask no more
		h.notifyOngoing(toastConnection, fmt.Sprintf("the backend that answered after the restart, autodoc %s (pid %d), still lacks %s: another build serves this store",
			h.session.Version(), h.session.PID(), lacks))
	case theirs.StrictSubsetOf(own) && !h.registrationOffered:
		h.registrationOffered = true
		h.set("App.registrationQuestion", fmt.Sprintf("This backend, autodoc %s, lacks %s, which this build reads. Restart it as this build? It stops, and this build starts in its place; indexing and embedding carry on where they stopped, and every client reconnects.",
			h.session.Version(), lacks))
		h.set("App.canRestartRegistrations", h.session.CanSpawn())
		h.open("registrations")
	case theirs.StrictSubsetOf(own), own.StrictSubsetOf(theirs):
		// offered once already, or the daemon reads more than this build: its own say
	case !h.registrationNoted:
		h.registrationNoted = true
		h.notify(fmt.Sprintf("this backend reads %s otherwise than this build: it keeps its own; System › Restart backend… restarts it as this build", lacks))
	}
}

// restartForRegistrations is the registration dialog's Restart: the daemon stops over this
// connection, and the reconnect starts this build in its place.
func (h *Host) restartForRegistrations() {
	h.closeDialog("registrations")
	h.registrationRestart = true
	h.restartConfirmed()
}

// holdLabel is how the hit list marks a hit of a document the daemon holds (ADR 0216 §1.4): one a
// chunker or a format it lacks made, searchable, never reinterpreted.
func holdLabel(hold string) string {
	switch hold {
	case "":
		return ""
	case "stale":
		return "held · changed on disk"
	case "unchecked":
		return "held · not yet checked"
	}
	return "held"
}
