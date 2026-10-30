package tui

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/golib/search/embed"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// SETTING UP SEMANTIC SEARCH — Search models › Embedding Models › Set up…
// (docs/decisions/search.md#the-setup-dialog): what this machine runs (host.probe), the models that
// fit it (embedding.catalog), the room and time each workspace takes (embedding.estimate), the
// download (embedding.pull, polled once a second), and Use: a plan of existing setters, each
// workspace's target computed apart (its provider, its policy), applied in order and retried from
// the start, every step a no-op once done.

// pollPullEvery is how often a running download's state is read.
const pollPullEvery = time.Second

// setupRow is one workspace in the setup: as it is now, whether it is ticked, and the estimate.
type setupRow struct {
	name, policy, provider string // its embedding policy ("" is always) and own provider ("" none)
	ticked                 bool
	texts, room            int64
	seconds                *float64
}

// target is what Use makes of r: the provider it will have ("" clears an own one) and its policy,
// each with whether it changes (ADR §4.4's table).
func (r setupRow) target() (provider string, setProvider bool, policy string, setPolicy bool) {
	off := r.policy == store.EmbeddingNever
	switch {
	case r.ticked && r.provider != "":
		return "", true, store.EmbeddingAlways, off
	case r.ticked:
		return "", false, store.EmbeddingAlways, off
	case r.provider == "" && !off:
		return "", false, store.EmbeddingNever, true
	}
	return "", false, "", false
}

// embedSetup is the dialog: the Ollama it asks, the cards, the workspaces, a running download, and
// the plan's last failure.
type embedSetup struct {
	c        Core
	after    func(time.Duration, func())
	rows     *tuidecl.ListModel // the workspaces, as the table shows them
	models   *tuidecl.ListModel // the cards, as the dropdown shows them
	base     string
	cards    []map[string]any
	pick     int
	ws       []setupRow
	exactFor string // the model the estimate is exact for (installed, timed); "" for none
	ready    bool   // a check has answered: the controls show what can be done
	stale    bool   // a plan ran and its workspaces could not be read again: Use waits for a Check
	shown    bool   // the dialog is open
	session  uint64 // each open and each Check is a new one: an answer to an older one is dropped
	asked    uint64 // each estimate is a new one: only the latest's answer is shown
	// The operations outlive a view's session: a Check or a close and reopen neither strands nor
	// forgets them.
	job, jobModel, jobBase string // the running download, its model and its Ollama; job "" for none
	// pulled is the downloads finished, by Ollama and model, each with its number in finished: a
	// Check started before one finished may have read the catalog before it, so its answer does
	// not undo that one; a Check started after reads a catalog newer than it, removals included
	pulled   map[string]map[string]uint64
	finished uint64
	polls    uint64     // each poll chain is a new one: an older chain stops
	plan     *setupPlan // the plan that stopped at a step, for Retry; nil for none
	running  bool       // a plan runs
}

// setupPlan is what Use applies, fixed when it starts: Retry runs the same one, whatever was picked
// or ticked since.
type setupPlan struct {
	model, base string
	tokens      int64
	rows        []setupRow
}

func newEmbedSetup(c Core) *embedSetup {
	s := &embedSetup{c: c, rows: tuidecl.NewListModel("key", "mark", "workspace", "now", "will", "texts", "room", "time"),
		models: tuidecl.NewListModel("key", "label")}
	if a, ok := c.(interface{ After(time.Duration, func()) }); ok {
		s.after = a.After
	}
	return s
}

func (s *embedSetup) State() map[string]any {
	return map[string]any{
		"App.embedSetupBase": embed.DefaultOllamaURL, "App.embedSetupHost": "", "App.embedSetupStatus": "",
		"App.embedSetupModels": s.models, "App.embedSetupModelIndex": 0, "App.embedSetupRows": s.rows,
		"App.embedSetupReady": false, "App.embedSetupPullShown": false, "App.embedSetupStopShown": false,
		"App.embedSetupUseShown": false, "App.embedSetupRetryShown": false,
	}
}

func (s *embedSetup) Commands() map[string]Command {
	return map[string]Command{
		"App.openEmbeddingSetup":    none(s.open),
		"App.embedSetupCheck":       oneString("the Ollama's URL", s.check),
		"App.embedSetupModelPicked": oneNumber("a model", s.picked),
		"App.embedSetupToggle":      oneNumber("a workspace", s.toggle),
		"App.embedSetupPull":        none(s.pull),
		"App.embedSetupStop":        none(s.stop),
		"App.embedSetupUse":         none(s.use),
		"App.embedSetupRetry":       none(s.retry),
		"App.embedSetupClosed":      none(s.closed),
	}
}

// open checks the default Ollama, and follows a download still running from before.
func (s *embedSetup) open() {
	s.shown = true
	s.c.Set("App.embedSetupBase", embed.DefaultOllamaURL)
	s.c.Open("embeddingSetup")
	s.check(embed.DefaultOllamaURL)
	if s.job != "" {
		s.follow()
	}
}

// closed ends the view's session and its polls; the operations themselves run on.
func (s *embedSetup) closed() {
	s.shown = false
	s.session++
	s.polls++
}

// buttons shows what can be done now.
func (s *embedSetup) buttons(ready bool) {
	s.ready = ready
	installed := ready && s.installed()
	s.c.Set("App.embedSetupReady", ready)
	s.c.Set("App.embedSetupPullShown", ready && !installed && s.job == "")
	s.c.Set("App.embedSetupStopShown", s.job != "")
	s.c.Set("App.embedSetupUseShown", ready && s.canUse())
	s.c.Set("App.embedSetupRetryShown", s.plan != nil && !s.running)
}

// canUse is Use's precondition: the model picked is installed, the estimate is exact for it, and no
// download or plan runs, nor a stopped plan waits for Retry.
func (s *embedSetup) canUse() bool {
	return s.installed() && s.exactFor == s.model() && s.job == "" && !s.running && s.plan == nil && !s.stale
}

func (s *embedSetup) card() map[string]any {
	if s.pick < 0 || s.pick >= len(s.cards) {
		return nil
	}
	return s.cards[s.pick]
}

func (s *embedSetup) installed() bool { c := s.card(); return c != nil && c["installed"] == true }

func (s *embedSetup) model() string { return str(s.card(), "name") }

// check reads the machine, the cards and the workspaces from the Ollama at base, and estimates the
// recommended model.
func (s *embedSetup) check(base string) {
	s.session++
	session, since := s.session, s.finished
	base = strings.TrimSpace(base) // the worker asks this Ollama, whatever a later Check sets
	s.base, s.exactFor, s.plan, s.stale = base, "", nil, false
	s.c.Set("App.embedSetupStatus", "")
	s.buttons(false)
	s.c.Go(func(ctx context.Context) any {
		host, err := s.c.Call(ctx, "host.probe", base)
		if err != nil {
			return err
		}
		cards, err := s.c.Call(ctx, "embedding.catalog", base)
		if err != nil {
			return err
		}
		ws, err := s.c.Call(ctx, "workspace.list")
		if err != nil {
			return err
		}
		return []any{host, cards, ws}
	}, func(v any) {
		if session != s.session {
			return
		}
		if err, ok := v.(error); ok {
			s.c.Set("App.embedSetupHost", s.c.Trf("autodoc.text.embedsetup.could_not_check", wireMessage(err)))
			return
		}
		got := v.([]any)
		host := asMap(got[0])
		if str(host, "ollama") == "" {
			s.c.Set("App.embedSetupHost", s.c.Trf("autodoc.text.embedsetup.no_ollama", base))
			return
		}
		s.c.Set("App.embedSetupHost", s.machine(host))
		s.cards = nil
		for _, c := range asList(got[1]) {
			card := asMap(c)
			if s.pulled[base][str(card, "name")] > since { // finished after this Check began
				card["installed"] = true
			}
			s.cards = append(s.cards, card)
		}
		s.pick = max(0, slices.IndexFunc(s.cards, func(c map[string]any) bool { return c["recommended"] == true }))
		s.listModels()
		s.ws = s.ws[:0]
		for _, w := range asList(got[2]) {
			m := asMap(w)
			if str(m, "state") == "error" {
				continue // not served: nothing to embed
			}
			r := setupRow{name: str(m, "name"), policy: str(m, "embedding_policy"), provider: str(m, "provider")}
			r.ticked = r.provider == "" && r.policy != store.EmbeddingNever
			s.ws = append(s.ws, r)
		}
		s.estimate()
	})
}

// machine is host.probe's answer as one line.
func (s *embedSetup) machine(host map[string]any) string {
	gpu := asMap(host["gpu"])
	var g string
	switch vendor, vram := str(gpu, "vendor"), num(gpu, "vram"); {
	case vendor == "":
		g = s.c.Tr("autodoc.text.embedsetup.no_gpu")
	case vram > 0:
		g = s.c.Trf("autodoc.text.embedsetup.gpu_with_memory", strings.TrimSpace(vendor+" "+str(gpu, "name")), bytesText(vram))
	default:
		g = s.c.Trf("autodoc.text.embedsetup.gpu_memory_unknown", strings.TrimSpace(vendor+" "+str(gpu, "name")))
	}
	return s.c.Trf("autodoc.text.embedsetup.this_machine", g, bytesText(num(host, "ram")), bytesText(num(host, "free")), str(host, "ollama"))
}

// fitText is a card's fit as the dropdown says it.
func (s *embedSetup) fitText(fit string) string {
	switch fit {
	case string(embed.FitGPU):
		return s.c.Tr("autodoc.text.embedsetup.fits_on_the_gpu")
	case string(embed.FitCPU):
		return s.c.Tr("autodoc.text.embedsetup.fits_in_memory")
	case string(embed.FitNone):
		return s.c.Tr("autodoc.text.embedsetup.too_large_here")
	}
	return s.c.Tr("autodoc.text.embedsetup.can_t_tell_here")
}

func (s *embedSetup) listModels() {
	rows := make([]rowOf, len(s.cards))
	for i, c := range s.cards {
		label := s.c.Trf("autodoc.text.embedsetup.model_row", str(c, "name"), s.fitText(str(c, "fit")), bytesText(num(c, "download")))
		if c["installed"] == true {
			label += s.c.Tr("autodoc.text.embedsetup.installed")
		}
		rows[i] = rowOf{"key": str(c, "name"), "label": label}
	}
	s.models.Reset(rows)
	s.c.Set("App.embedSetupModelIndex", -1) // moved away first, so the same index still moves it
	s.c.Set("App.embedSetupModelIndex", s.pick)
}

// picked is another model: the last one's estimate says nothing of it, so Use waits for its own.
func (s *embedSetup) picked(i int) {
	if i < 0 || i >= len(s.cards) || i == s.pick {
		return
	}
	s.pick, s.exactFor, s.plan = i, "", nil
	for j := range s.ws {
		s.ws[j].texts, s.ws[j].room, s.ws[j].seconds = 0, 0, nil
	}
	s.listRows()
	s.c.Set("App.embedSetupStatus", "")
	s.buttons(true)
	s.estimate()
}

// toggle ticks a workspace or unticks it: another plan, so a stopped one's Retry gives way to Use.
func (s *embedSetup) toggle(i int) {
	if i < 0 || i >= len(s.ws) || s.running {
		return
	}
	s.ws[i].ticked = !s.ws[i].ticked
	s.plan = nil
	s.listRows()
	s.buttons(true)
}

// estimate asks embedding.estimate for the model picked, for every workspace.
func (s *embedSetup) estimate() {
	s.asked++
	session, asked, model, base := s.session, s.asked, s.model(), s.base
	if model == "" {
		s.buttons(true)
		return
	}
	s.c.Go(func(ctx context.Context) any {
		v, err := s.c.Call(ctx, "embedding.estimate", model, []any{}, base)
		return answered(asMap(v), err)
	}, func(v any) {
		a := v.(answerOf[map[string]any])
		if session != s.session || asked != s.asked { // a model picked since, even the same again
			return
		}
		if a.err != nil {
			s.c.Set("App.embedSetupStatus", s.c.Trf("autodoc.text.embedsetup.could_not_estimate", wireMessage(a.err)))
			s.buttons(true)
			return
		}
		if s.exactFor = ""; str(a.v, "basis") == "installed" {
			s.exactFor = model
			if c := s.card(); c != nil && c["installed"] != true {
				c["installed"] = true // the daemon found it installed: newer than the catalog the Check read
				s.listModels()
			}
		}
		for _, w := range asList(a.v["workspaces"]) {
			m := asMap(w)
			i := slices.IndexFunc(s.ws, func(r setupRow) bool { return r.name == str(m, "workspace") })
			if i < 0 {
				continue
			}
			s.ws[i].texts, s.ws[i].room, s.ws[i].seconds = num(m, "texts"), num(m, "logical_bytes"), nil
			if sec, ok := m["seconds"].(float64); ok {
				s.ws[i].seconds = &sec
			}
		}
		s.listRows()
		switch {
		case !s.installed():
			s.c.Set("App.embedSetupStatus", s.c.Trf("autodoc.text.embedsetup.to_download", bytesText(num(s.card(), "download"))))
		case s.exactFor == model:
			s.c.Set("App.embedSetupStatus", s.c.Trf("autodoc.text.embedsetup.ready_to_use", model))
		}
		s.buttons(true)
	})
}

// listRows shows the workspaces: ticked or not, what they do now, what Use will do, and the estimate.
func (s *embedSetup) listRows() {
	rows := make([]rowOf, len(s.ws))
	for i, r := range s.ws {
		mark := "[ ]"
		if r.ticked {
			mark = "[x]"
		}
		t := "—"
		if r.seconds != nil {
			t = s.c.Trf("autodoc.text.embedsetup.about", durationText(*r.seconds))
		}
		rows[i] = rowOf{"key": r.name, "mark": mark, "workspace": r.name, "now": s.nowText(r), "will": s.willText(r),
			"texts": fmt.Sprint(r.texts), "room": bytesText(r.room), "time": t}
	}
	s.rows.Reset(rows)
}

func (s *embedSetup) nowText(r setupRow) string {
	if r.provider != "" {
		return s.c.Trf("autodoc.text.embedsetup.its_own", r.provider)
	}
	if r.policy == store.EmbeddingNever {
		return s.c.Tr("autodoc.text.embedsetup.off")
	}
	return s.c.Tr("autodoc.text.embedsetup.on")
}

func (s *embedSetup) willText(r setupRow) string {
	_, setProvider, policy, setPolicy := r.target()
	switch {
	case setProvider:
		return s.c.Trf("autodoc.text.embedsetup.switches_from", r.provider)
	case setPolicy && policy == store.EmbeddingAlways:
		return s.c.Tr("autodoc.text.embedsetup.turned_on")
	case setPolicy && policy == store.EmbeddingNever:
		return s.c.Tr("autodoc.text.embedsetup.turned_off")
	case r.ticked:
		return s.c.Tr("autodoc.text.embedsetup.embeds")
	case r.provider != "":
		return s.c.Trf("autodoc.text.embedsetup.keeps_its_own", r.provider)
	}
	return s.c.Tr("autodoc.text.embedsetup.stays_off")
}

// pull starts the model's download and follows it.
func (s *embedSetup) pull() {
	session, model, base := s.session, s.model(), s.base
	if model == "" || s.job != "" || s.installed() {
		return
	}
	s.c.Go(func(ctx context.Context) any {
		v, err := s.c.Call(ctx, "embedding.pull", model, base)
		return answered(str(asMap(v), "job"), err)
	}, func(v any) {
		a := v.(answerOf[string])
		if a.err != nil {
			if session == s.session {
				s.c.Set("App.embedSetupStatus", s.c.Trf("autodoc.text.embedsetup.download_failed", wireMessage(a.err)))
			}
			return
		}
		// the job is the daemon's whatever the view did meanwhile: kept, so Stop and the poll reach it
		s.job, s.jobModel, s.jobBase = a.v, model, base
		if s.shown {
			s.buttons(s.ready)
			s.follow()
		}
	})
}

// follow starts a poll chain on the running download, ending any older one.
func (s *embedSetup) follow() {
	s.polls++
	s.poll(s.polls)
}

// poll reads the download's state, and again a second later while it runs and chain is the latest.
func (s *embedSetup) poll(chain uint64) {
	job := s.job
	s.c.Go(func(ctx context.Context) any {
		v, err := s.c.Call(ctx, "embedding.pull_status", job)
		return answered(asMap(v), err)
	}, func(v any) {
		a := v.(answerOf[map[string]any])
		if chain != s.polls || job != s.job {
			return
		}
		if a.err != nil {
			s.job = ""
			s.c.Set("App.embedSetupStatus", s.c.Trf("autodoc.text.embedsetup.download_failed", wireMessage(a.err)))
			s.buttons(s.ready)
			return
		}
		switch str(a.v, "state") {
		case "running":
			done, total := num(a.v, "completed"), num(a.v, "total")
			if eta, ok := a.v["eta"].(float64); ok {
				s.c.Set("App.embedSetupStatus", s.c.Trf("autodoc.text.embedsetup.downloading_eta", bytesText(done), bytesText(total), durationText(eta)))
			} else {
				s.c.Set("App.embedSetupStatus", s.c.Trf("autodoc.text.embedsetup.downloading", bytesText(done), bytesText(total)))
			}
			if s.after != nil {
				s.after(pollPullEvery, func() { s.poll(chain) })
			}
			return
		case "done":
			s.job = ""
			s.finished++
			if s.pulled == nil {
				s.pulled = map[string]map[string]uint64{}
			}
			if s.pulled[s.jobBase] == nil {
				s.pulled[s.jobBase] = map[string]uint64{}
			}
			s.pulled[s.jobBase][s.jobModel] = s.finished
			// the model downloaded is the job's, on the job's Ollama: neither need be the one shown now
			if s.jobBase == s.base {
				if i := slices.IndexFunc(s.cards, func(c map[string]any) bool { return str(c, "name") == s.jobModel }); i >= 0 {
					s.cards[i]["installed"] = true
				}
				s.listModels()
				if s.jobModel == s.model() {
					s.c.Set("App.embedSetupStatus", s.c.Tr("autodoc.text.embedsetup.downloaded_timing_it"))
					s.estimate()
				}
			}
		case "cancelled":
			s.job = ""
			s.c.Set("App.embedSetupStatus", s.c.Tr("autodoc.text.embedsetup.stopped"))
		default:
			s.job = ""
			s.c.Set("App.embedSetupStatus", s.c.Trf("autodoc.text.embedsetup.download_failed", str(a.v, "error")))
		}
		s.buttons(s.ready)
	})
}

func (s *embedSetup) stop() {
	job := s.job
	if job == "" {
		return
	}
	s.c.Go(func(ctx context.Context) any {
		_, err := s.c.Call(ctx, "embedding.pull_cancel", job)
		return err
	}, func(any) {}) // the poll reads the job's end and says it
}

// setupStep is one step of Use's plan: what it is called if it stops there, and its call.
type setupStep struct {
	what string
	run  func(ctx context.Context, now map[string]setupRow) error
}

// stepFailed is the step of Use's plan that failed, and why.
type stepFailed struct {
	what string
	err  error
}

// planRun is a run of a plan: the step that failed (nil for none), and the workspaces as the run
// left them (nil when they could not be read).
type planRun struct {
	failed *stepFailed
	after  map[string]setupRow
}

// workspaces are the daemon's workspaces now, by name: each one's policy and own provider.
func (s *embedSetup) workspaces(ctx context.Context) (map[string]setupRow, error) {
	v, err := s.c.Call(ctx, "workspace.list")
	if err != nil {
		return nil, err
	}
	now := map[string]setupRow{}
	for _, w := range asList(v) {
		m := asMap(w)
		now[str(m, "name")] = setupRow{name: str(m, "name"), policy: str(m, "embedding_policy"), provider: str(m, "provider")}
	}
	return now, nil
}

// use fixes the plan, the model picked and the workspaces as ticked, and applies it.
func (s *embedSetup) use() {
	if !s.canUse() {
		return // the button shows only then; a command sent anyway is refused here
	}
	tokens := int64(store.DefaultContext)
	if n := num(s.card(), "context"); n > 0 && n < tokens {
		tokens = n
	}
	s.apply(&setupPlan{model: s.model(), base: s.base, tokens: tokens, rows: slices.Clone(s.ws)})
}

// retry applies the plan that stopped, as it was fixed.
func (s *embedSetup) retry() {
	if s.plan == nil || s.running {
		return
	}
	s.apply(s.plan)
}

// apply runs plan's steps in order (ADR §4.4): the workspaces turned off, the provider found or
// added, used unless it already is, then the ticked workspaces' targets. Each run reads the
// workspaces as they are first, and a setter whose target already holds is skipped: the daemon
// restarts a workspace on every workspace.set_provider, so a Retry must not repeat one that took.
// A step that fails stops the run there, saying which, and keeps plan for Retry.
func (s *embedSetup) apply(plan *setupPlan) {
	session := s.session
	var name string
	var steps []setupStep
	for _, r := range plan.rows {
		if _, _, policy, set := r.target(); set && policy == store.EmbeddingNever {
			steps = append(steps, setupStep{s.c.Trf("autodoc.text.embedsetup.step_turning_off", r.name), func(ctx context.Context, now map[string]setupRow) error {
				if cur, ok := now[r.name]; ok && cur.policy == store.EmbeddingNever {
					return nil
				}
				_, err := s.c.Call(ctx, "workspace.embedding_policy", r.name, store.EmbeddingNever)
				return err
			}})
		}
	}
	steps = append(steps, setupStep{s.c.Tr("autodoc.text.embedsetup.step_the_provider"), func(ctx context.Context, _ map[string]setupRow) error {
		n, err := s.provider(ctx, plan.base, plan.model, plan.tokens)
		name = n
		return err
	}}, setupStep{s.c.Tr("autodoc.text.embedsetup.step_using_it"), func(ctx context.Context, _ map[string]setupRow) error {
		v, err := s.c.Call(ctx, "embedding.providers") // checked again just before: Use is no no-op
		if err != nil {
			return err
		}
		if str(asMap(v), "active") == name {
			return nil
		}
		_, err = s.c.Call(ctx, "embedding.use", name)
		return err
	}})
	for _, r := range plan.rows {
		_, setProvider, policy, setPolicy := r.target()
		if !r.ticked || (!setProvider && !setPolicy) {
			continue
		}
		steps = append(steps, setupStep{s.c.Trf("autodoc.text.embedsetup.step_turning_on", r.name), func(ctx context.Context, now map[string]setupRow) error {
			cur, known := now[r.name]
			if setProvider && (!known || cur.provider != "") {
				if _, err := s.c.Call(ctx, "workspace.set_provider", r.name, ""); err != nil {
					return err
				}
			}
			if setPolicy && (!known || cur.policy != policy) {
				_, err := s.c.Call(ctx, "workspace.embedding_policy", r.name, policy)
				return err
			}
			return nil
		}})
	}
	s.running, s.plan = true, plan
	s.c.Set("App.embedSetupStatus", s.c.Tr("autodoc.text.embedsetup.setting_up"))
	s.buttons(true)
	s.c.Go(func(ctx context.Context) any {
		var out planRun
		now, err := s.workspaces(ctx)
		if err != nil {
			out.failed = &stepFailed{s.c.Tr("autodoc.text.embedsetup.step_reading_the_workspaces"), err}
			return out
		}
		for _, st := range steps {
			if err := st.run(ctx, now); err != nil {
				out.failed = &stepFailed{st.what, err} // not wrapped: wireMessage answers the daemon's message alone
				break
			}
		}
		// the workspaces as the run left them, a step that failed or not: the rows a next plan is made
		// from are these, not the ones from before it
		out.after, _ = s.workspaces(ctx)
		return out
	}, func(v any) {
		run := v.(planRun)
		s.running = false // the run has ended, whether or not its dialog is still open
		if session != s.session {
			if s.shown {
				// the view now open read the workspaces before this run changed them: its rows are not
				// what the daemon has, so Use waits for a Check that reads them again
				s.stale = true
				s.c.Set("App.embedSetupStatus", s.c.Tr("autodoc.text.embedsetup.workspaces_changed_check_again"))
				s.buttons(s.ready)
			}
			return
		}
		if run.after == nil {
			s.stale = true // what the run left is unknown: Use waits for a Check
		}
		for i, r := range s.ws {
			if cur, ok := run.after[r.name]; ok {
				s.ws[i].provider, s.ws[i].policy = cur.provider, cur.policy
			}
		}
		s.listRows()
		if f := run.failed; f != nil {
			s.c.Set("App.embedSetupStatus", s.c.Trf("autodoc.text.embedsetup.stopped_at", f.what, wireMessage(f.err)))
			s.buttons(true)
			return
		}
		s.plan = nil
		s.c.Set("App.embedSetupStatus", s.c.Trf("autodoc.text.embedsetup.done", plan.model))
		s.buttons(true)
		s.c.Set("App.embedSetupUseShown", false)
		s.c.SearchModelsChanged(false)
	})
}

// provider is the stored provider equal to the setup's spec (kind ollama, base, model, tokens, no
// key): the one in use when it is one of them, else the first by name; none, one added under a name
// made of the model and the context, -2, -3 … past an unequal one's. Another context's provider is
// left alone (ADR §4.4, rationale §R6a).
func (s *embedSetup) provider(ctx context.Context, base, model string, tokens int64) (string, error) {
	v, err := s.c.Call(ctx, "embedding.providers")
	if err != nil {
		return "", err
	}
	m := asMap(v)
	var equal, names []string
	for _, p := range asList(m["providers"]) {
		pm := asMap(p)
		names = append(names, str(pm, "name"))
		if str(pm, "kind") == store.KindOllama && strings.TrimRight(str(pm, "base_url"), "/") == strings.TrimRight(base, "/") &&
			str(pm, "model") == model && num(pm, "context") == tokens && pm["has_key"] != true {
			equal = append(equal, str(pm, "name"))
		}
	}
	if len(equal) > 0 {
		if slices.Contains(equal, str(m, "active")) {
			return str(m, "active"), nil
		}
		slices.Sort(equal)
		return equal[0], nil
	}
	name := providerName(model, tokens)
	for n := 2; slices.Contains(names, name); n++ {
		name = fmt.Sprintf("%s-%d", providerName(model, tokens), n)
	}
	_, err = s.c.Call(ctx, "embedding.add", map[string]any{"name": name, "kind": store.KindOllama, "base_url": base, "model": model, "context": tokens})
	return name, err
}

var nameRunes = regexp.MustCompile(`[^a-z0-9.-]+`)

// providerName is a provider's name made of its model and context: qwen3-embedding-0.6b-8192.
func providerName(model string, tokens int64) string {
	return strings.Trim(nameRunes.ReplaceAllString(strings.ToLower(model), "-"), "-") + fmt.Sprintf("-%d", tokens)
}

// durationText is a time as the setup says it: seconds, minutes or hours.
func durationText(seconds float64) string {
	switch {
	case seconds < 90:
		return fmt.Sprintf("%.0f s", seconds)
	case seconds < 90*60:
		return fmt.Sprintf("%.0f min", seconds/60)
	}
	return fmt.Sprintf("%.1f h", seconds/3600)
}
