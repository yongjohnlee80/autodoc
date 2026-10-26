package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/rpc"
	"github.com/yongjohnlee80/golib/search/embed"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

const setupModel = "qwen3-embedding:0.6b"

// setupDaemon scripts the setup's verbs: a machine with an NVIDIA GPU, two cards (the second one
// recommended, installed when installed is), and workspaces a (on), b (its own provider p), c (on)
// and d (off), and e (not served). The estimate is exact once the model is installed.
func setupDaemon(installed bool) *scriptedCore {
	c := newScripted("a")
	c.answers["host.probe"] = func([]any) (any, error) {
		return map[string]any{"gpu": map[string]any{"vendor": "nvidia", "name": "RTX", "vram": int64(8) << 30},
			"ram": int64(32) << 30, "free": int64(100) << 30, "ollama": "0.40.11"}, nil
	}
	c.answers["embedding.catalog"] = func([]any) (any, error) {
		return []any{
			map[string]any{"name": "qwen3-embedding:8b", "fit": "none", "download": int64(4) << 30, "context": int64(40960)},
			map[string]any{"name": setupModel, "fit": "gpu", "download": int64(600) << 20, "context": int64(32768),
				"recommended": true, "installed": installed},
		}, nil
	}
	c.answers["workspace.list"] = func([]any) (any, error) {
		return []any{
			map[string]any{"name": "a", "state": "ready", "embedding_policy": store.EmbeddingAlways},
			map[string]any{"name": "b", "state": "ready", "embedding_policy": store.EmbeddingAlways, "provider": "p"},
			map[string]any{"name": "c", "state": "ready", "embedding_policy": store.EmbeddingAlways},
			map[string]any{"name": "d", "state": "ready", "embedding_policy": store.EmbeddingNever},
			map[string]any{"name": "e", "state": "error"},
		}, nil
	}
	c.answers["embedding.estimate"] = func(p []any) (any, error) {
		var ws []any
		for _, n := range []string{"a", "b", "c", "d"} {
			w := map[string]any{"workspace": n, "texts": int64(100), "logical_bytes": int64(1) << 20}
			if installed && p[0] == setupModel {
				w["seconds"] = 12.0
			}
			ws = append(ws, w)
		}
		basis := "provisional"
		if installed && p[0] == setupModel { // only an installed model's estimate is exact
			basis = "installed"
		}
		return map[string]any{"basis": basis, "workspaces": ws}, nil
	}
	return c
}

func shown(c *scriptedCore, name string) bool {
	b, _ := c.set["App.embedSetup"+name+"Shown"].(bool)
	return b
}

func setupRows(s *embedSetup) map[string]rowOf {
	out := map[string]rowOf{}
	for i := range s.rows.Len() {
		r := s.rows.At(i)
		out[r["key"].(string)] = r
	}
	return out
}

// Open checks the default Ollama: the machine as one line, the cards with the recommended one
// picked, and every served workspace ticked unless it has its own provider or is off. The model
// not installed, the estimate is the download's, and only Download is offered.
func TestTheSetupChecksTheMachineAndTicksTheWorkspaces(t *testing.T) {
	c := setupDaemon(false)
	s := newEmbedSetup(c)
	s.open()
	if !slices.Contains(c.opened, "embeddingSetup") {
		t.Fatal("the dialog did not open")
	}
	if host := c.set["App.embedSetupHost"].(string); !strings.Contains(host, "RTX") || !strings.Contains(host, "0.40.11") {
		t.Fatalf("host line %q", host)
	}
	if c.set["App.embedSetupModelIndex"] != 1 || s.model() != setupModel {
		t.Fatalf("picked %v (%q); want the recommended one", c.set["App.embedSetupModelIndex"], s.model())
	}
	if !c.called("embedding.estimate[" + setupModel) {
		t.Fatalf("the recommended model was not estimated: %v", c.calls)
	}
	rows := setupRows(s)
	if _, ok := rows["e"]; ok || len(rows) != 4 {
		t.Fatalf("rows %v; want a to d, not the unserved e", rows)
	}
	for name, mark := range map[string]string{"a": "[x]", "b": "[ ]", "c": "[x]", "d": "[ ]"} {
		if rows[name]["mark"] != mark {
			t.Errorf("%s marked %v; want %s", name, rows[name]["mark"], mark)
		}
	}
	if rows["a"]["time"] != "—" {
		t.Errorf("a provisional estimate has no time: %v", rows["a"]["time"])
	}
	if !c.set["App.embedSetupReady"].(bool) || !shown(c, "Pull") || shown(c, "Use") || shown(c, "Stop") || shown(c, "Retry") {
		t.Fatalf("buttons: pull %v use %v stop %v retry %v", shown(c, "Pull"), shown(c, "Use"), shown(c, "Stop"), shown(c, "Retry"))
	}
	if st := c.set["App.embedSetupStatus"]; st != c.Trf("autodoc.text.embedsetup.to_download", bytesText(600<<20)) {
		t.Fatalf("status %q", st)
	}
}

// No Ollama at the URL says so, and offers nothing but Check.
func TestTheSetupSaysWhenNoOllamaAnswers(t *testing.T) {
	c := setupDaemon(false)
	c.answers["host.probe"] = func([]any) (any, error) { return map[string]any{"ram": int64(1) << 30}, nil }
	s := newEmbedSetup(c)
	s.check(" http://elsewhere:11434 ")
	if got := c.set["App.embedSetupHost"]; got != c.Trf("autodoc.text.embedsetup.no_ollama", "http://elsewhere:11434") {
		t.Fatalf("host line %q", got)
	}
	if c.set["App.embedSetupReady"].(bool) || shown(c, "Pull") || shown(c, "Use") {
		t.Fatal("offered more than Check with no Ollama")
	}
	if c.called("embedding.estimate") {
		t.Fatal("estimated with no Ollama")
	}
}

// An answer to an earlier check is dropped: the later Ollama's is the one shown.
func TestAnEarlierChecksAnswerIsDropped(t *testing.T) {
	c := setupDaemon(false)
	c.deferred = true
	s := newEmbedSetup(c)
	s.check("http://first")
	c.answers["host.probe"] = func([]any) (any, error) { return map[string]any{}, nil }
	s.check("http://second")
	c.deliver(1, 0)
	if got := c.set["App.embedSetupHost"]; got != c.Trf("autodoc.text.embedsetup.no_ollama", "http://second") {
		t.Fatalf("host line %q; want the second check's", got)
	}
}

// What Use makes of each workspace, ADR §4.4's table.
func TestTheSetupsTargetFollowsTheTable(t *testing.T) {
	for _, c := range []struct {
		row         setupRow
		setProvider bool
		policy      string
		setPolicy   bool
		will        string
	}{
		{setupRow{ticked: true, provider: "p", policy: store.EmbeddingAlways}, true, store.EmbeddingAlways, false, "switches_from"},
		{setupRow{ticked: true, provider: "p", policy: store.EmbeddingNever}, true, store.EmbeddingAlways, true, "switches_from"},
		{setupRow{ticked: true, policy: store.EmbeddingNever}, false, store.EmbeddingAlways, true, "turned_on"},
		{setupRow{ticked: true, policy: store.EmbeddingAlways}, false, store.EmbeddingAlways, false, "embeds"},
		{setupRow{policy: store.EmbeddingAlways}, false, store.EmbeddingNever, true, "turned_off"},
		{setupRow{provider: "p", policy: store.EmbeddingAlways}, false, "", false, "keeps_its_own"},
		{setupRow{policy: store.EmbeddingNever}, false, "", false, "stays_off"},
	} {
		_, setProvider, policy, setPolicy := c.row.target()
		if setProvider != c.setProvider || policy != c.policy || setPolicy != c.setPolicy {
			t.Errorf("%+v: provider %v, policy %q %v; want %v, %q %v", c.row, setProvider, policy, setPolicy, c.setProvider, c.policy, c.setPolicy)
		}
		s := newEmbedSetup(newScripted("a"))
		if got, want := s.willText(c.row), englishText("autodoc.text.embedsetup."+c.will); !strings.HasPrefix(got, strings.TrimSuffix(want, "%1")) {
			t.Errorf("%+v: will %q; want %q", c.row, got, want)
		}
	}
}

// installedSetup is a checked setup with the model installed, a provider of another context named
// as the setup would name its own, and the plan's calls recorded from here.
func installedSetup(t *testing.T) (*scriptedCore, *embedSetup, string) {
	t.Helper()
	c := setupDaemon(true)
	name := providerName(setupModel, int64(store.DefaultContext))
	c.answers["embedding.providers"] = func([]any) (any, error) {
		return map[string]any{"providers": []any{
			map[string]any{"name": name, "kind": store.KindOllama, "base_url": "http://localhost:11434", "model": setupModel, "context": int64(512)},
		}, "active": name}, nil
	}
	s := newEmbedSetup(c)
	s.open()
	if !shown(c, "Use") {
		t.Fatalf("Use not offered with the model installed: status %q", c.set["App.embedSetupStatus"])
	}
	c.calls = nil
	return c, s, name
}

// Use runs the plan in order: the workspace unticked turned off, the provider added (another
// context's keeps its name, so this one is -2), used, then each ticked workspace's target.
func TestUseRunsThePlanInOrder(t *testing.T) {
	c, s, name := installedSetup(t)
	added := false
	c.answers["embedding.add"] = func([]any) (any, error) { added = true; return nil, nil }
	s.toggle(slices.IndexFunc(s.ws, func(r setupRow) bool { return r.name == "b" }))
	s.toggle(slices.IndexFunc(s.ws, func(r setupRow) bool { return r.name == "c" }))
	s.toggle(slices.IndexFunc(s.ws, func(r setupRow) bool { return r.name == "d" }))
	s.use()
	tokens := int64(store.DefaultContext)
	want := []string{
		"workspace.list[]",
		"workspace.embedding_policy[c never]",
		"embedding.providers[]",
		fmt.Sprint("embedding.add[", map[string]any{"name": name + "-2", "kind": store.KindOllama, "base_url": "http://localhost:11434", "model": setupModel, "context": tokens}, "]"),
		"embedding.providers[]",
		"embedding.use[" + name + "-2]",
		"workspace.set_provider[b ]",
		"workspace.embedding_policy[d always]",
		"workspace.list[]", // the workspaces as the plan left them
	}
	if !slices.Equal(c.calls, want) {
		t.Fatalf("calls\n%s\nwant\n%s", strings.Join(c.calls, "\n"), strings.Join(want, "\n"))
	}
	if !added || c.set["App.embedSetupStatus"] != c.Trf("autodoc.text.embedsetup.done", setupModel) || shown(c, "Use") || shown(c, "Retry") {
		t.Fatalf("after: status %q, use %v, retry %v", c.set["App.embedSetupStatus"], shown(c, "Use"), shown(c, "Retry"))
	}
	if !c.saw("search.changed ranker=false") {
		t.Fatal("the search was not told its models changed")
	}
}

// An equal provider already in use wins over an equal one first by name, and is neither added nor
// used again.
func TestUseReusesTheEqualProviderInUse(t *testing.T) {
	c, s, name := installedSetup(t)
	tokens := int64(store.DefaultContext)
	c.answers["embedding.providers"] = func([]any) (any, error) {
		return map[string]any{"providers": []any{
			map[string]any{"name": "aa", "kind": store.KindOllama, "base_url": "http://localhost:11434", "model": setupModel, "context": tokens},
			map[string]any{"name": "zz", "kind": store.KindOllama, "base_url": "http://localhost:11434/", "model": setupModel, "context": tokens},
			map[string]any{"name": "keyed", "kind": store.KindOllama, "base_url": "http://localhost:11434", "model": setupModel, "context": tokens, "has_key": true},
			map[string]any{"name": name, "kind": store.KindOllama, "base_url": "http://localhost:11434", "model": setupModel, "context": int64(512)},
		}, "active": "zz"}, nil
	}
	s.use()
	if c.called("embedding.add") || c.called("embedding.use") {
		t.Fatalf("the equal provider in use was added or used again: %v", c.calls)
	}
	if c.set["App.embedSetupStatus"] != c.Trf("autodoc.text.embedsetup.done", setupModel) {
		t.Fatalf("status %q", c.set["App.embedSetupStatus"])
	}
}

// A step that fails stops the plan there, saying which step and why, and offers Retry; Retry runs
// the plan again from the start.
func TestAFailedStepStopsThePlanAndRetryRunsItAgain(t *testing.T) {
	c, s, _ := installedSetup(t)
	failing := true
	c.answers["embedding.use"] = func([]any) (any, error) {
		if failing {
			return nil, &golibrpc.Error{Code: rpc.CodeProviderRefused, Message: "the provider refused"} // as the wire answers it
		}
		return nil, nil
	}
	s.toggle(slices.IndexFunc(s.ws, func(r setupRow) bool { return r.name == "b" })) // a step after the one that fails
	s.use()
	want := c.Trf("autodoc.text.embedsetup.stopped_at", c.Tr("autodoc.text.embedsetup.step_using_it"), "the provider refused")
	if got := c.set["App.embedSetupStatus"]; got != want {
		t.Fatalf("status %q; want %q", got, want)
	}
	if !shown(c, "Retry") || shown(c, "Use") {
		t.Fatalf("retry %v, use %v", shown(c, "Retry"), shown(c, "Use"))
	}
	if c.called("workspace.set_provider") {
		t.Fatalf("a step after the failed one ran: %v", c.calls)
	}
	failing = false
	s.Commands()["App.embedSetupRetry"].Run(nil)
	if !c.called("workspace.set_provider[b ]") || shown(c, "Retry") {
		t.Fatalf("Retry did not finish the plan: %v", c.calls)
	}
}

// A download is polled while it runs (its progress shown), and once done the model is installed and
// estimated again; Stop cancels the job, and the poll says it stopped.
func TestTheDownloadIsPolledAndStops(t *testing.T) {
	for _, end := range []string{"done", "cancelled"} {
		t.Run(end, func(t *testing.T) {
			c := setupDaemon(false)
			s := newEmbedSetup(c)
			var later []func()
			s.after = func(d time.Duration, fn func()) {
				if d != pollPullEvery {
					t.Errorf("polled after %v", d)
				}
				later = append(later, fn)
			}
			s.open()
			state := "running"
			c.answers["embedding.pull"] = func([]any) (any, error) { return map[string]any{"job": "pull-1"}, nil }
			c.answers["embedding.pull_status"] = func([]any) (any, error) {
				return map[string]any{"state": state, "completed": int64(10) << 20, "total": int64(600) << 20}, nil
			}
			s.pull()
			if !shown(c, "Stop") || shown(c, "Pull") || len(later) != 1 {
				t.Fatalf("while running: stop %v, pull %v, polls queued %d", shown(c, "Stop"), shown(c, "Pull"), len(later))
			}
			if got := c.set["App.embedSetupStatus"]; got != c.Trf("autodoc.text.embedsetup.downloading", bytesText(10<<20), bytesText(600<<20)) {
				t.Fatalf("progress %q", got)
			}
			if end == "cancelled" {
				s.stop()
				if !c.called("embedding.pull_cancel[pull-1]") {
					t.Fatalf("Stop did not cancel the job: %v", c.calls)
				}
			}
			state = end
			c.calls = nil
			later[0]()
			if shown(c, "Stop") || len(later) != 1 {
				t.Fatalf("after %s: stop %v, polls queued %d", end, shown(c, "Stop"), len(later))
			}
			if end == "done" {
				if !s.installed() || !c.called("embedding.estimate["+setupModel) {
					t.Fatalf("done: installed %v, calls %v", s.installed(), c.calls)
				}
				return
			}
			if got := c.set["App.embedSetupStatus"]; got != c.Tr("autodoc.text.embedsetup.stopped") || !shown(c, "Pull") {
				t.Fatalf("stopped: %q, pull %v", got, shown(c, "Pull"))
			}
		})
	}
}

// A closed dialog's download poll stops: the session it belongs to has ended.
func TestClosingTheSetupEndsItsPolls(t *testing.T) {
	c := setupDaemon(false)
	s := newEmbedSetup(c)
	var later []func()
	s.after = func(_ time.Duration, fn func()) { later = append(later, fn) }
	s.open()
	c.answers["embedding.pull"] = func([]any) (any, error) { return map[string]any{"job": "pull-1"}, nil }
	c.answers["embedding.pull_status"] = func([]any) (any, error) { return map[string]any{"state": "running"}, nil }
	s.pull()
	s.Commands()["App.embedSetupClosed"].Run(nil)
	c.calls = nil
	later[0]()
	if len(later) != 1 {
		t.Fatalf("a closed dialog polled on: %d polls queued, calls %v", len(later), c.calls)
	}
}

func TestProviderNameAndDurations(t *testing.T) {
	if got := providerName("Qwen3-Embedding:0.6B", 8192); got != "qwen3-embedding-0.6b-8192" {
		t.Errorf("providerName %q", got)
	}
	for sec, want := range map[float64]string{12: "12 s", 89: "89 s", 600: "10 min", 7200: "2.0 h"} {
		if got := durationText(sec); got != want {
			t.Errorf("durationText(%v) %q; want %q", sec, got, want)
		}
	}
}

// heldCore is a scripted core whose Go holds the worker itself, not only its answer, until run: a
// worker started earlier runs after what happened since, as a slow daemon makes it.
type heldCore struct {
	*scriptedCore
	held []func()
}

func (h *heldCore) Go(fn func(context.Context) any, then func(any)) {
	h.held = append(h.held, func() { then(fn(context.Background())) })
}

// run runs the held worker i, and its answer.
func (h *heldCore) run(i int) {
	f := h.held[i]
	h.held[i] = func() {}
	f()
}

// Each worker asks the Ollama its operation was started on: a Check, an estimate and a download
// started on one URL ask it, not the URL a later Check set before they ran.
func TestEachOperationKeepsItsOwnOllama(t *testing.T) {
	asked := func(h *heldCore) {
		t.Helper()
		if slices.ContainsFunc(h.calls, func(c string) bool { return strings.Contains(c, "http://second") }) {
			t.Fatalf("a worker started on the first Ollama asked the second: %v", h.calls)
		}
	}
	// a Check's worker, run after a second Check
	h := &heldCore{scriptedCore: setupDaemon(false)}
	s := newEmbedSetup(h)
	s.check("http://first")
	s.check("http://second")
	h.run(0)
	if !h.called("host.probe[http://first]") || !h.called("embedding.catalog[http://first]") {
		t.Fatalf("the first Check's worker: %v", h.calls)
	}
	asked(h)

	// an estimate's and a download's, run after a second Check
	h = &heldCore{scriptedCore: setupDaemon(false)}
	s = newEmbedSetup(h)
	s.check("http://first")
	h.run(0) // the check's answer starts the estimate (1)
	s.pull() // 2
	s.check("http://second")
	h.run(1)
	h.run(2)
	for _, want := range []string{"embedding.estimate[" + setupModel + " [] http://first]", "embedding.pull[" + setupModel + " http://first]"} {
		if !h.called(want) {
			t.Errorf("no %s in %v", want, h.calls)
		}
	}
	asked(h)
}

// A download installs the model it pulled: picking another one while it runs leaves that one not
// installed, and the download's end does not time a model it did not pull.
func TestADownloadInstallsItsOwnModel(t *testing.T) {
	c := setupDaemon(false)
	s := newEmbedSetup(c)
	var later []func()
	s.after = func(_ time.Duration, fn func()) { later = append(later, fn) }
	s.open()
	state := "running"
	c.answers["embedding.pull"] = func([]any) (any, error) { return map[string]any{"job": "pull-1"}, nil }
	c.answers["embedding.pull_status"] = func([]any) (any, error) { return map[string]any{"state": state}, nil }
	s.pull()
	s.picked(0) // the other card, while the download runs
	state = "done"
	c.calls = nil
	later[0]()
	if s.cards[1]["installed"] != true || s.cards[0]["installed"] == true {
		t.Fatalf("installed: %s %v, %s %v; want the downloaded %s only", s.cards[0]["name"], s.cards[0]["installed"], s.cards[1]["name"], s.cards[1]["installed"], setupModel)
	}
	if c.called("embedding.estimate") || shown(c, "Use") {
		t.Fatalf("the download's end estimated or offered the model picked since: %v, use %v", c.calls, shown(c, "Use"))
	}
}

// Picking another model hides Use until that model's own estimate is exact, and Use sent anyway
// changes nothing.
func TestPickingAnotherModelHidesUseUntilItsEstimate(t *testing.T) {
	h := &heldCore{scriptedCore: setupDaemon(true)}
	s := newEmbedSetup(h)
	s.open()
	h.run(0)
	h.run(1)
	if !shown(h.scriptedCore, "Use") {
		t.Fatalf("Use not offered for the installed model: %q", h.set["App.embedSetupStatus"])
	}
	s.picked(0) // not installed; its estimate (2) is held
	if shown(h.scriptedCore, "Use") {
		t.Fatal("Use still offered after another model was picked")
	}
	h.calls = nil
	s.use()
	if len(h.calls) != 0 || len(h.held) != 3 || s.running {
		t.Fatalf("Use, sent with no exact estimate for the model picked, ran: %v", h.calls)
	}
	h.run(2)
	if shown(h.scriptedCore, "Use") || s.exactFor != "" {
		t.Fatalf("Use offered on another model's provisional estimate (exact for %q)", s.exactFor)
	}
}

// Retry runs the plan that stopped, and skips what already took: a workspace whose provider was
// cleared is not cleared again (the daemon restarts it each time), and the step that failed runs.
// Ticking after a stop is another plan: Use, not Retry.
func TestRetrySkipsWhatAlreadyTook(t *testing.T) {
	c, s, _ := installedSetup(t)
	ws := map[string]map[string]any{
		"a": {"name": "a", "state": "ready", "embedding_policy": store.EmbeddingAlways},
		"b": {"name": "b", "state": "ready", "embedding_policy": store.EmbeddingAlways, "provider": "p"},
		"c": {"name": "c", "state": "ready", "embedding_policy": store.EmbeddingAlways},
		"d": {"name": "d", "state": "ready", "embedding_policy": store.EmbeddingNever},
	}
	c.answers["workspace.list"] = func([]any) (any, error) {
		var out []any
		for _, n := range []string{"a", "b", "c", "d"} {
			out = append(out, maps.Clone(ws[n]))
		}
		return out, nil
	}
	c.answers["workspace.set_provider"] = func(p []any) (any, error) { ws[p[0].(string)]["provider"] = p[1]; return nil, nil }
	failing := true
	c.answers["workspace.embedding_policy"] = func(p []any) (any, error) {
		if failing && p[0] == "d" {
			return nil, &golibrpc.Error{Code: golibrpc.CodeInvalidParams, Message: "not now"}
		}
		ws[p[0].(string)]["embedding_policy"] = p[1]
		return nil, nil
	}
	for _, n := range []string{"b", "d"} {
		s.toggle(slices.IndexFunc(s.ws, func(r setupRow) bool { return r.name == n }))
	}
	s.use()
	if !shown(c, "Retry") || shown(c, "Use") {
		t.Fatalf("after the failed step: retry %v, use %v, status %q", shown(c, "Retry"), shown(c, "Use"), c.set["App.embedSetupStatus"])
	}
	s.use() // Use is not Retry: a stopped plan waits for Retry
	failing = false
	s.retry()
	cleared := 0
	for _, call := range c.calls {
		if call == "workspace.set_provider[b ]" {
			cleared++
		}
	}
	if cleared != 1 || ws["d"]["embedding_policy"] != store.EmbeddingAlways {
		t.Fatalf("b cleared %d times (want 1), d's policy %v: %v", cleared, ws["d"]["embedding_policy"], c.calls)
	}
	if c.set["App.embedSetupStatus"] != c.Trf("autodoc.text.embedsetup.done", setupModel) || shown(c, "Retry") {
		t.Fatalf("after Retry: %q, retry %v", c.set["App.embedSetupStatus"], shown(c, "Retry"))
	}
	if rows := setupRows(s); rows["b"]["now"] != c.Tr("autodoc.text.embedsetup.on") || rows["d"]["now"] != c.Tr("autodoc.text.embedsetup.on") {
		t.Fatalf("the rows after the plan: b %v, d %v; want both on", rows["b"]["now"], rows["d"]["now"])
	}

	// a stop, then a tick: Use for the new plan, Retry gone
	failing = true
	s.toggle(slices.IndexFunc(s.ws, func(r setupRow) bool { return r.name == "a" }))
	ws["a"]["embedding_policy"] = store.EmbeddingAlways
	s.toggle(slices.IndexFunc(s.ws, func(r setupRow) bool { return r.name == "a" }))
	if !shown(c, "Use") || shown(c, "Retry") {
		t.Fatalf("after ticking: use %v, retry %v", shown(c, "Use"), shown(c, "Retry"))
	}
}

// A plan whose dialog closed before it ended does not leave the setup believing it still runs.
func TestAPlanEndingAfterCloseLeavesNothingRunning(t *testing.T) {
	h := &heldCore{scriptedCore: setupDaemon(true)}
	h.answers["embedding.providers"] = func([]any) (any, error) { return map[string]any{"providers": []any{}}, nil }
	s := newEmbedSetup(h)
	s.open()
	h.run(0)
	h.run(1)
	s.use()
	s.Commands()["App.embedSetupClosed"].Run(nil)
	h.run(2)
	if s.running {
		t.Fatal("the plan ended after its dialog closed, and the setup still says one runs")
	}
}

// Switching to another model and back asks again: the first estimate of that model, answering
// late, is not the one shown, so Use waits for the latest.
func TestALateEstimateOfAModelPickedAgainIsDropped(t *testing.T) {
	h := &heldCore{scriptedCore: setupDaemon(true)}
	s := newEmbedSetup(h)
	s.open()
	h.run(0)    // the check's answer asks the first estimate (1), held
	s.picked(0) // 2
	s.picked(1) // 3: the first model again
	h.run(1)
	if shown(h.scriptedCore, "Use") || s.exactFor != "" {
		t.Fatalf("a late estimate asked before two picks was shown (exact for %q)", s.exactFor)
	}
	h.run(2)
	h.run(3)
	if !shown(h.scriptedCore, "Use") || s.exactFor != setupModel {
		t.Fatalf("the latest estimate did not offer Use (exact for %q)", s.exactFor)
	}
}

// Switching to another model and straight back, before either estimate answers, hides Use until
// the new estimate does: the one exact before the switch is not that answer.
func TestSwitchingAwayAndBackWaitsForTheNewEstimate(t *testing.T) {
	h := &heldCore{scriptedCore: setupDaemon(true)}
	s := newEmbedSetup(h)
	s.open()
	h.run(0)
	h.run(1)
	if !shown(h.scriptedCore, "Use") {
		t.Fatalf("Use not offered for the installed model: %q", h.set["App.embedSetupStatus"])
	}
	s.picked(0) // 2
	s.picked(1) // 3: back, both estimates held
	if shown(h.scriptedCore, "Use") {
		t.Fatal("Use offered on the estimate from before the switch")
	}
	h.run(3)
	if !shown(h.scriptedCore, "Use") {
		t.Fatal("the new estimate did not offer Use")
	}
}

// statefulWorkspaces makes c's workspace verbs act on ws: the list reads it, the setters write it,
// and policy for the workspace named fails until ok is set; it answers the calls' count of each.
func statefulWorkspaces(c *scriptedCore, failsFor string, ok *bool) map[string]map[string]any {
	ws := map[string]map[string]any{
		"a": {"name": "a", "state": "ready", "embedding_policy": store.EmbeddingAlways},
		"b": {"name": "b", "state": "ready", "embedding_policy": store.EmbeddingAlways, "provider": "p"},
		"c": {"name": "c", "state": "ready", "embedding_policy": store.EmbeddingAlways},
		"d": {"name": "d", "state": "ready", "embedding_policy": store.EmbeddingNever},
	}
	c.answers["workspace.list"] = func([]any) (any, error) {
		var out []any
		for _, n := range []string{"a", "b", "c", "d"} {
			out = append(out, maps.Clone(ws[n]))
		}
		return out, nil
	}
	c.answers["workspace.set_provider"] = func(p []any) (any, error) { ws[p[0].(string)]["provider"] = p[1]; return nil, nil }
	c.answers["workspace.embedding_policy"] = func(p []any) (any, error) {
		if !*ok && p[0] == failsFor {
			return nil, &golibrpc.Error{Code: golibrpc.CodeInvalidParams, Message: "not now"}
		}
		ws[p[0].(string)]["embedding_policy"] = p[1]
		return nil, nil
	}
	return ws
}

func tick(s *embedSetup, name string) {
	s.toggle(slices.IndexFunc(s.ws, func(r setupRow) bool { return r.name == name }))
}

// A plan made after a stop starts from what the stop left: b's own provider was cleared before d's
// step failed, so b, unticked now, embeds with the daemon's provider and is turned off, as its row
// says, not kept "with its own".
func TestANewPlanAfterAStopStartsFromWhatTheStopLeft(t *testing.T) {
	c, s, _ := installedSetup(t)
	ok := false
	ws := statefulWorkspaces(c, "d", &ok)
	tick(s, "b")
	tick(s, "d")
	s.use()
	if rows := setupRows(s); rows["b"]["now"] != c.Tr("autodoc.text.embedsetup.on") {
		t.Fatalf("b's row after the stop says %v; want on, as the daemon has it", rows["b"]["now"])
	}
	tick(s, "b") // unticked: another plan
	ok = true
	s.use()
	if ws["b"]["embedding_policy"] != store.EmbeddingNever || ws["d"]["embedding_policy"] != store.EmbeddingAlways {
		t.Fatalf("after the new plan: b %v, d %v; want b off, d on: %v", ws["b"]["embedding_policy"], ws["d"]["embedding_policy"], c.calls)
	}
}

// A run whose workspaces cannot be read again leaves Use waiting for a Check: the rows could be
// from before it.
func TestAPlanWhoseEndCannotBeReadWaitsForACheck(t *testing.T) {
	c, s, _ := installedSetup(t)
	ok := false
	statefulWorkspaces(c, "d", &ok)
	list := c.answers["workspace.list"]
	reads := 0
	c.answers["workspace.list"] = func(p []any) (any, error) {
		if reads++; reads == 2 { // the read after the run
			return nil, errors.New("gone")
		}
		return list(p)
	}
	tick(s, "d")
	s.use()
	tick(s, "a") // another plan: no Retry
	if shown(c, "Use") {
		t.Fatal("Use offered on rows the stopped run may have changed")
	}
	s.check(s.base)
	if !shown(c, "Use") {
		t.Fatal("Use not offered after a Check read the workspaces again")
	}
}

// A Check during a download keeps following it: the download is the daemon's, not the view's. Its
// end installs the model only on the Ollama it was pulled from.
func TestACheckDuringADownloadKeepsFollowingIt(t *testing.T) {
	for _, recheck := range []string{embed.DefaultOllamaURL, "http://elsewhere:11434"} {
		t.Run(recheck, func(t *testing.T) {
			h := &heldCore{scriptedCore: setupDaemon(false)}
			s := newEmbedSetup(h)
			var later []func()
			s.after = func(_ time.Duration, fn func()) { later = append(later, fn) }
			state := "running"
			h.answers["embedding.pull"] = func([]any) (any, error) { return map[string]any{"job": "pull-1"}, nil }
			h.answers["embedding.pull_status"] = func([]any) (any, error) { return map[string]any{"state": state}, nil }
			s.open()
			h.run(0) // the check; its estimate (1)
			h.run(1)
			s.pull()         // 2
			s.check(recheck) // 3
			h.run(2)         // the download's answer, under the new check; its poll (4)
			if s.job != "pull-1" || !shown(h.scriptedCore, "Stop") {
				t.Fatalf("the download answered after a Check was dropped: job %q, stop %v", s.job, shown(h.scriptedCore, "Stop"))
			}
			s.check(recheck) // 5: another Check, the poll chain running
			h.run(4)
			if len(later) != 1 {
				t.Fatalf("the poll did not go on after the Check: %d queued", len(later))
			}
			h.run(3) // the older check's answer, dropped
			h.run(5) // the latest check's; its estimate (6)
			state = "done"
			later[0]()
			h.run(len(h.held) - 1)
			if s.job != "" || shown(h.scriptedCore, "Stop") {
				t.Fatalf("the download's end not followed: job %q, stop %v", s.job, shown(h.scriptedCore, "Stop"))
			}
			if got, want := s.cards[1]["installed"] == true, recheck == embed.DefaultOllamaURL; got != want {
				t.Fatalf("%s marked installed %v on %s; want %v (pulled on %s)", setupModel, got, recheck, want, embed.DefaultOllamaURL)
			}
		})
	}
}

// Reopening the dialog follows a download still running from before it closed.
func TestReopeningFollowsARunningDownload(t *testing.T) {
	c := setupDaemon(false)
	s := newEmbedSetup(c)
	var later []func()
	s.after = func(_ time.Duration, fn func()) { later = append(later, fn) }
	c.answers["embedding.pull"] = func([]any) (any, error) { return map[string]any{"job": "pull-1"}, nil }
	c.answers["embedding.pull_status"] = func([]any) (any, error) {
		return map[string]any{"state": "running", "completed": int64(1) << 20, "total": int64(600) << 20}, nil
	}
	s.open()
	s.pull()
	s.closed()
	later[0]() // the closed view's chain ends here
	if len(later) != 1 {
		t.Fatalf("a closed dialog polled on: %d queued", len(later))
	}
	c.set["App.embedSetupStatus"] = ""
	s.open()
	if len(later) != 2 || !shown(c, "Stop") || c.set["App.embedSetupStatus"] == "" {
		t.Fatalf("reopened: %d polls queued, stop %v, status %q; want the download followed", len(later), shown(c, "Stop"), c.set["App.embedSetupStatus"])
	}
}

// A plan ending after the dialog was closed and opened again leaves nothing running, and the new
// view, which read the workspaces before the plan ended, offers Use again after a Check.
func TestAPlanEndingAfterReopenRefreshesTheControls(t *testing.T) {
	h := &heldCore{scriptedCore: setupDaemon(true)}
	h.answers["embedding.providers"] = func([]any) (any, error) { return map[string]any{"providers": []any{}}, nil }
	s := newEmbedSetup(h)
	s.open()
	h.run(0)
	h.run(1)
	s.use() // 2, held
	s.closed()
	s.open() // 3
	h.run(3) // its estimate (4)
	h.run(4)
	if shown(h.scriptedCore, "Use") {
		t.Fatal("Use offered while the old plan still runs")
	}
	h.run(2)
	// the old plan ran after this view read the workspaces: it ended, and the view checks again first
	if s.running || shown(h.scriptedCore, "Use") || h.set["App.embedSetupStatus"] != h.Tr("autodoc.text.embedsetup.workspaces_changed_check_again") {
		t.Fatalf("after the old plan ended: running %v, use %v, status %q", s.running, shown(h.scriptedCore, "Use"), h.set["App.embedSetupStatus"])
	}
	s.check(s.base) // 5
	h.run(5)
	h.run(6)
	if !shown(h.scriptedCore, "Use") {
		t.Fatal("Use not offered after the Check")
	}
}

// A plan from an earlier view that ends after a new view read the workspaces leaves that view's rows
// behind the daemon's: Use waits for a Check that reads them again, and says so.
func TestAnOldPlanEndingMakesTheNewViewCheckAgain(t *testing.T) {
	h := &heldCore{scriptedCore: setupDaemon(true)}
	h.answers["embedding.providers"] = func([]any) (any, error) { return map[string]any{"providers": []any{}}, nil }
	ok := true
	ws := statefulWorkspaces(h.scriptedCore, "", &ok)
	s := newEmbedSetup(h)
	s.open()
	h.run(0)
	h.run(1)
	tick(s, "b")
	s.use() // 2, held before it changes anything
	s.closed()
	s.open() // 3: reads b with its own provider
	h.run(3)
	h.run(4)
	h.run(2) // the old plan clears b's provider
	if ws["b"]["provider"] != "" {
		t.Fatalf("the old plan did not run: b's provider %v", ws["b"]["provider"])
	}
	if s.canUse() || shown(h.scriptedCore, "Use") || h.set["App.embedSetupStatus"] != h.Tr("autodoc.text.embedsetup.workspaces_changed_check_again") {
		t.Fatalf("the new view kept its rows from before the old plan: canUse %v, use %v, status %q", s.canUse(), shown(h.scriptedCore, "Use"), h.set["App.embedSetupStatus"])
	}
	s.check(s.base) // 5
	h.run(5)
	h.run(6)
	if !shown(h.scriptedCore, "Use") {
		t.Fatal("Use not offered after a Check read the workspaces again")
	}
	if rows := setupRows(s); rows["b"]["now"] != h.Tr("autodoc.text.embedsetup.on") {
		t.Fatalf("b's row after the Check: %v; want on", rows["b"]["now"])
	}
}

// A download's end outlives a Check that began before it: that Check's catalog may have been read
// before the download ended, so its answer does not mark the model not installed again.
func TestADownloadsEndOutlivesALateCheck(t *testing.T) {
	h := &heldCore{scriptedCore: setupDaemon(false)}
	s := newEmbedSetup(h)
	s.after = func(time.Duration, func()) {}
	h.answers["embedding.pull"] = func([]any) (any, error) { return map[string]any{"job": "pull-1"}, nil }
	h.answers["embedding.pull_status"] = func([]any) (any, error) { return map[string]any{"state": "done"}, nil }
	s.open()
	h.run(0)
	h.run(1)
	s.pull()        // 2
	h.run(2)        // the job; its poll (3)
	s.check(s.base) // 4: begins before the download ends
	h.run(3)        // done; its estimate (5)
	if s.cards[1]["installed"] != true {
		t.Fatal("the download's end did not mark the model installed")
	}
	h.answers["embedding.estimate"] = setupDaemon(true).answers["embedding.estimate"]
	h.run(4) // the Check's answer, its catalog saying not installed; its estimate (6)
	if s.cards[1]["installed"] != true {
		t.Fatal("a Check begun before the download ended marked the model not installed again")
	}
	h.run(5) // the estimate asked before the Check answered: dropped
	h.run(6)
	if !shown(h.scriptedCore, "Use") || shown(h.scriptedCore, "Pull") {
		t.Fatalf("after the Check's estimate: use %v, pull %v", shown(h.scriptedCore, "Use"), shown(h.scriptedCore, "Pull"))
	}
}

// A Check begun after a download ended reads a catalog newer than it: a model removed since is not
// installed, and Download is offered again, as after a close and reopen.
func TestAFreshCheckSeesAModelRemovedAfterItsDownload(t *testing.T) {
	c := setupDaemon(false)
	s := newEmbedSetup(c)
	c.answers["embedding.pull"] = func([]any) (any, error) { return map[string]any{"job": "pull-1"}, nil }
	c.answers["embedding.pull_status"] = func([]any) (any, error) { return map[string]any{"state": "done"}, nil }
	s.open()
	if s.installed() || !shown(c, "Pull") {
		t.Fatalf("before the download: installed %v, pull %v", s.installed(), shown(c, "Pull"))
	}
	c.answers["embedding.estimate"] = setupDaemon(true).answers["embedding.estimate"]
	s.pull() // done at once; the estimate after it says installed
	if !s.installed() || !shown(c, "Use") {
		t.Fatalf("after the download: installed %v, use %v", s.installed(), shown(c, "Use"))
	}
	// removed on the same Ollama: the catalog and the estimate say so
	c.answers["embedding.estimate"] = setupDaemon(false).answers["embedding.estimate"]
	for _, again := range []func(){func() { s.check(s.base) }, func() { s.closed(); s.open() }} {
		again()
		if s.installed() || !shown(c, "Pull") || shown(c, "Use") {
			t.Fatalf("after the removal: installed %v, pull %v, use %v; want it not installed, Download offered", s.installed(), shown(c, "Pull"), shown(c, "Use"))
		}
	}
}

// An estimate the daemon makes against the installed model marks the model installed, whatever the
// catalog read earlier said: the daemon's answer is the newer one.
func TestAnInstalledEstimateMarksTheModelInstalled(t *testing.T) {
	c := setupDaemon(false)
	c.answers["embedding.estimate"] = setupDaemon(true).answers["embedding.estimate"]
	s := newEmbedSetup(c)
	s.open()
	if !s.installed() || !shown(c, "Use") || shown(c, "Pull") {
		t.Fatalf("installed %v, use %v, pull %v; want the estimate's word for it", s.installed(), shown(c, "Use"), shown(c, "Pull"))
	}
}
