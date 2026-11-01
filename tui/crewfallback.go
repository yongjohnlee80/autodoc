package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// The window's part in the crew's fallback (docs/decisions/crew.md#the-fallback-exactly-once): mail
// its agents didn't receive in time comes back to it as jobs (mail.fallbacks), which it types into
// the agent's terminal through the base, once, and reports (mail.fallback_done). Asking for jobs is
// what makes the window take them; a daemon before protocol 42 has none, and the window's sends
// are typed by the send itself, as before.

const (
	// fallbackWait is how long one mail.fallbacks waits for a job (crew.MaxWait).
	fallbackWait = crew.MaxWait
	// NOTE: can't announce: a failed poll (a lost connection, a store failure) says nothing about
	// when the daemon answers again, so the window asks again after a fixed gap.
	fallbackRetry = 5 * time.Second
)

// fallbackJob is one job as mail.fallbacks gives it: what the window types, to whom, and how it
// answers.
type fallbackJob struct {
	ID, Lease, Codename, From, Text string
	Tries                           int64 // leases of the job before this one: a retry knows it is one
}

// The window's record of a job, <agentBase>/.crew-fallbacks/<job id>, written before it types: the
// window never types a job it has a record of. A record holds "typing" until the outcome is known,
// then the outcome, and goes once the daemon took the report.
const fallbackTyping = "typing"

// pollFallbacks asks for the scope's jobs while claim gen is the current one, delivering each, and
// asks again as soon as one answer is handled.
func (h *crewHost) pollFallbacks(gen uint64) {
	if gen != h.gen || h.scope == "" || h.deliverJob == nil {
		return
	}
	scope, epoch := h.scope, h.epoch
	h.c.Go(func(ctx context.Context) any {
		v, err := h.c.Call(ctx, "mail.fallbacks", scope, h.run, epoch, fallbackWait.Milliseconds())
		if err != nil {
			return err
		}
		return v
	}, func(v any) {
		if gen != h.gen {
			return
		}
		if err, _ := v.(error); err != nil {
			switch code(err) {
			case golibrpc.CodeMethodNotFound: // a daemon before protocol 42: no jobs, ever, from it
				return
			case crew.CodeStaleEpoch: // the scope changed hands: the renewal claims again, and that claim polls
				return
			}
			h.c.After(fallbackRetry, func() { h.pollFallbacks(gen) })
			return
		}
		for _, j := range fallbackJobsOf(v) {
			h.takeJob(j)
		}
		h.c.After(0, func() { h.pollFallbacks(gen) }) // off this answer's stack: the next poll waits on the daemon
	})
}

// fallbackJobsOf is mail.fallbacks' jobs, as the wire decodes them.
func fallbackJobsOf(v any) []fallbackJob {
	var out []fallbackJob
	for _, e := range asList(asMap(v)["jobs"]) {
		m := asMap(e)
		out = append(out, fallbackJob{ID: str(m, "id"), Lease: str(m, "lease"), Codename: str(m, "codename"),
			From: str(m, "from"), Text: str(m, "text"), Tries: num(m, "tries")})
	}
	return out
}

// takeJob delivers j once: a job the window has a record of was typed (or begun) before, so it is
// reported from the record and never typed again; one offered again with no record went to another
// run of the window, which may have typed it, so it is reported unknown and the user given the
// text; otherwise the record is written, then the text typed.
func (h *crewHost) takeJob(j fallbackJob) {
	if h.taken == nil {
		h.taken = map[string]bool{}
	}
	h.taken[j.ID] = true
	if rec, ok := h.fallbackRecord(j.ID); ok {
		outcome := rec
		if outcome == fallbackTyping { // begun before a restart: the text reached the terminal or the user sees it
			outcome = string(mail.FallbackDelivered)
		}
		h.reportJob(j, outcome)
		return
	}
	if j.Tries > 0 || h.writeFallbackRecord(j.ID, fallbackTyping) != nil {
		h.c.Notify(h.c.Trf("autodoc.notice.crewfallback.unknown", j.Codename, j.Text))
		h.reportJob(j, string(mail.FallbackUnknown))
		return
	}
	h.deliverJob(j.Codename, j.Text, func(err error) {
		// the user is told of every fallback: typed and sent here, typed but left for their Enter (the
		// base's notice says why), or not typed at all
		outcome := string(mail.FallbackDelivered)
		switch {
		case err == nil:
			h.c.Notify(h.c.Trf("autodoc.notice.crewfallback.delivered", j.Codename, j.From))
		case errors.Is(err, ErrAgentTypedNotSent):
			outcome = string(mail.FallbackTypedNotSent)
		case err != nil: // nothing typed: the user has the text instead
			outcome = string(mail.FallbackUnknown)
			h.c.Notify(h.c.Trf("autodoc.notice.crewfallback.unknown", j.Codename, j.Text))
		}
		_ = h.writeFallbackRecord(j.ID, outcome) // a failed write leaves "typing": a re-offer still reports, never types
		h.reportJob(j, outcome)
	})
}

// reportJob tells the daemon what came of j, and drops the record once it is told. A report the
// daemon didn't take keeps the record: the job is offered again when its lease lapses, and answered
// from it.
func (h *crewHost) reportJob(j fallbackJob, outcome string) {
	scope, epoch, ok := h.held()
	if !ok {
		return
	}
	h.c.Go(func(ctx context.Context) any {
		_, err := h.c.Call(ctx, "mail.fallback_done", scope, h.run, epoch, j.ID, j.Lease, outcome)
		return err
	}, func(v any) {
		if err, _ := v.(error); err == nil {
			_ = os.Remove(h.fallbackPath(j.ID))
		}
	})
}

func (h *crewHost) fallbackPath(id string) string {
	return filepath.Join(filepath.Dir(h.dir), ".crew-fallbacks", filepath.Base(id))
}

// fallbackRecord is the window's record of job id, and whether it has one.
func (h *crewHost) fallbackRecord(id string) (string, bool) {
	b, err := os.ReadFile(h.fallbackPath(id))
	return string(b), err == nil
}

// writeFallbackRecord records job id as state, durably, before the window acts on it.
func (h *crewHost) writeFallbackRecord(id, state string) error {
	p := h.fallbackPath(id)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return writeAtomic(p, []byte(state), 0o600)
}

// tookJob is whether this run took job, and so told the user of its outcome itself.
func (h *crewHost) tookJob(job string) bool { return h.taken[job] }
