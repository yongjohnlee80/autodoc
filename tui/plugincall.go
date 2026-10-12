package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// PLUGIN CALLS (ADR 1791762474 §4.5) — protocol 3's host.call: a plugin asks AutoDoc to run a
// command of its catalog (catalog.go), one its manifest's calls = [...] declares, and is answered
// plugin.result, both notifications matched by the plugin's call number. Every call accepted is
// answered exactly once:
//
//	ok                  the entry ran
//	failed <why>        the entry's own error
//	unknown <id>        no entry has the id
//	refused <why>       the id is not declared, is a plugin's, the call is malformed, or more than
//	                    plugin.MaxPendingCalls are waiting ("refused busy")
//
// A call reusing a number still waiting is a protocol violation: logged, run never, answered never,
// so the waiting call's answer is the only one with its number. A call from a plugin opened at a
// protocol before 3, or before host.ready, or with no readable number, is logged and dropped.
//
// The link's goroutine reads the call and keeps its number; the loop runs it, as a key does, and
// answers it.

// hostCall is a host.call, on the link's goroutine: it must not block.
func (r *pluginRun) hostCall(params []any) {
	if r.e.m.Protocol < 3 || !r.readyIn.Load() {
		r.log.note(fmt.Sprintf("host.call dropped: the plugin speaks protocol %d, or has not answered plugin.open", r.e.m.Protocol))
		return
	}
	c, err := plugin.ReadCall(params)
	if c.Num == 0 {
		r.log.note("host.call dropped: " + err.Error())
		return
	}
	r.callMu.Lock()
	switch {
	case r.calls[c.Num]:
		r.dupes++
		r.callMu.Unlock()
		r.log.note(fmt.Sprintf("host.call %d dropped, unanswered: the number is a call still waiting", c.Num))
		return
	case len(r.calls) >= plugin.MaxPendingCalls:
		r.busy++
		r.callMu.Unlock()
		r.h.p.Post(func() { r.answer(c.Num, plugin.CallRefused+" busy", false) })
		return
	}
	if r.calls == nil {
		r.calls = map[int64]bool{}
	}
	r.calls[c.Num] = true
	r.callMu.Unlock()
	var refusal string
	switch {
	case err != nil:
		refusal = "malformed: " + err.Error()
	case strings.HasPrefix(c.ID, "plugin."):
		refusal = c.ID + " is a plugin's command, never another plugin's to run"
	case !slices.Contains(r.e.m.Calls, c.ID):
		refusal = c.ID + " is not in the manifest's calls"
	}
	r.h.p.Post(func() {
		if refusal != "" {
			r.answer(c.Num, plugin.CallRefused+" "+refusal, true)
			return
		}
		r.answer(c.Num, callOutcome(c.ID, r.h.cat.run(c.ID, c.Args)), true)
	})
}

// callOutcome is a run's error as plugin.result says it: "" for none.
func callOutcome(id string, err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errUnknownCommand):
		return plugin.CallUnknown + " " + id
	}
	return plugin.CallFailed + " " + err.Error()
}

// answer sends plugin.result for call n, on the loop: ok when why is "". A call that was waiting
// waits no longer.
func (r *pluginRun) answer(n int64, why string, waiting bool) {
	if waiting {
		r.callMu.Lock()
		delete(r.calls, n)
		r.callMu.Unlock()
	}
	r.send(plugin.MethodResult, plugin.ResultParams(plugin.Result{Num: n, OK: why == "", Error: why}))
}

// callStats are how many calls wait, were dropped as a number reused, and were answered busy.
func (r *pluginRun) callStats() (waiting, dupes, busy int) {
	r.callMu.Lock()
	defer r.callMu.Unlock()
	return len(r.calls), r.dupes, r.busy
}
