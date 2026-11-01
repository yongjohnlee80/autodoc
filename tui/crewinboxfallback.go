package tui

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/mail"
)

// The fallback in the human's feed (docs/decisions/crew.md#the-fallback-exactly-once): a job whose
// host is away shows, in every window, as waiting for it until a later change says its host took
// it; one whose outcome is unknown gives the user its text, in every window but the one whose
// crew host took it and told the user itself.

// fallbackToast is the toast id of job's wait for its host.
func fallbackToast(job string) string { return "crew-fallback:" + job }

// readFallback reads job (inbox.fallback), named by a feed change of kind, while gen is the
// connection's, and shows what it says.
func (x *crewInbox) readFallback(gen uint64, job, kind string) {
	if job == "" {
		return
	}
	x.c.Go(func(ctx context.Context) any {
		v, err := x.c.Call(ctx, "inbox.fallback", job)
		if err != nil {
			return err
		}
		return v
	}, func(v any) {
		if gen != x.gen {
			return
		}
		if _, failed := v.(error); failed { // an older daemon, a job gone, or a failure: the next change asks again
			return
		}
		x.showFallback(job, kind, asMap(v))
	})
}

// showFallback shows job's view m: waiting for its absent host while it is open or leased, else
// the wait ends; an unknown outcome's text to the user, unless this window's host took the job.
func (x *crewInbox) showFallback(job, kind string, m map[string]any) {
	id := fallbackToast(job)
	state, present := str(m, "state"), m["host_present"] == true
	if (state == mail.JobOpen || state == mail.JobLeased) && !present {
		text := x.c.Trf("autodoc.crew.fallback_waits", str(m, "from"), str(m, "recipient"), str(m, "scope"))
		x.fallbacks[job] = text
		if o, shown := x.open[id]; !shown || o.text != text {
			x.c.NotifyOngoingClick(id, text, nil)
			x.open[id] = crewOpen{text: text, done: x.c.Trf("autodoc.crew.fallback_went", str(m, "recipient"))}
		}
		return
	}
	if _, waited := x.fallbacks[job]; waited {
		delete(x.fallbacks, job)
		if o, shown := x.open[id]; shown {
			x.c.NotifyDone(id, o.done)
			delete(x.open, id)
		}
	}
	if state == mail.JobUnknown && kind == mail.ChangeFallbackUnknown && (x.took == nil || !x.took(job)) {
		x.c.Notify(x.c.Trf("autodoc.notice.crewfallback.unknown", str(m, "recipient"), str(m, "text")))
	}
}

// recheckFallbacks reads again every job shown as waiting: a reconnected feed, or a scope that
// changed hands, may have left them stale.
func (x *crewInbox) recheckFallbacks() {
	for job := range x.fallbacks {
		x.readFallback(x.gen, job, mail.ChangeFallback)
	}
}
