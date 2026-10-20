package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/yongjohnlee80/autodoc"
	regindex "github.com/yongjohnlee80/autodoc/core/registry"
)

// ONE CANDIDATE PER INSTALL OR UPDATE (ADR 1792351195 §4.7) — every add, update and Lua package
// install stages one candidate: a fresh clone pinned to a full commit. Its card names that commit,
// Audit… hands a fresh copy of it to an agent, and Install builds it; the installed tree is never
// audited or installed from. A candidate owns its card, its stage, its audit copies and its toast,
// and ends exactly once (end). Two live candidates never share a target (kind/name).

// opState is where a candidate is.
type opState int

const (
	opConfirming opState = iota // its card is up, or is about to be
	opSuspended                 // its card is down, its toast reopens it (an audit handed off)
	opCopying                   // Audit… is making its copy: nothing else acts on it until the copy's answer
	opInstalling                // Install began: nothing else acts on it
	opEnded
)

// candidate is one install or update. Its identity is immutable; only state moves, on the loop.
type candidate struct {
	kind, name, repo string // kind: regindex.KindPlugin or KindLua
	commit           string // in full; the card shows its first seven
	stage            string // the clone, at commit: what Install builds and moves in
	update           bool   // replaces an installed plugin, which is never this candidate's to remove
	entry            *regindex.IndexEntry
	m                manifest         // a plugin's manifest, as the stage holds it
	pkg              packageManifest  // a Lua package's
	card             func(*candidate) // shows its card: the kind's own (askPlugin, askPackage)
	hide             func(*candidate) // takes its card down when another candidate's replaces it

	state       opState
	auditCopies []string // one root per Audit handed off: the copy, and its task file beside it
	toasted     bool     // its own toast is up (toastID)
}

func (c *candidate) key() string { return c.kind + "/" + c.name }
func (c *candidate) toastID() string {
	return "audit:" + c.key() + ":" + c.commit[:min(12, len(c.commit))]
}

// auditNote is the card's sentence about Audit…, above its buttons (rationale §R3), in English: the
// audit task file's, as the agent reads it. A card shows it in the UI's language.
const auditNote = "Audit asks your agent to read this code. AutoDoc can't stop the agent from running " +
	"code or changing files: pick an agent you trust, and watch its tab."

// candidates are the live candidates, by key, and Audit…'s picker of agents.
type candidates struct {
	c      Core
	allocs *allocs // every stage and audit copy a worker made, until it is removed
	ops    map[string]*candidate
	onCard *candidate // the candidate whose card is up
	pick   *pick[AgentEntry]
	// auditing is the candidate whose agent picker is open
	auditing *candidate
}

func newCandidates(c Core) *candidates {
	cs := &candidates{c: c, ops: map[string]*candidate{}, allocs: newAllocs()}
	cs.pick = newAgentPick(c, "auditAgentPicker", "auditAgent", cs.auditWith)
	cs.pick.scope = TicketLocal
	cs.pick.cancel = cs.auditCancelled
	return cs
}

func (*candidates) Name() string { return "candidates" }

func (cs *candidates) State() map[string]any {
	return map[string]any{
		"App.auditAgentRows": cs.pick.list.Model(), "App.auditAgentField": "", "App.auditAgentStatus": "agents",
		"App.auditAgentPreviewTitle": "", "App.auditAgentPreviewText": "", "App.auditAgentPreviewAt": 0,
	}
}

func (*candidates) Commands() map[string]Command { return nil }

func (cs *candidates) Picks() map[string]picker { return map[string]picker{"auditAgent": cs.pick} }

// Stopped removes every stage, audit copy and task file a candidate's worker made, the ones whose
// answers never reached the loop included: the session ended. The loop has ended, so nothing is
// posted. A worker still making one removes it itself (allocs.hold refuses it).
func (cs *candidates) Stopped() {
	for _, c := range cs.ops {
		c.state = opEnded
	}
	clear(cs.ops)
	cs.allocs.close()
}

// allocs are the directories candidates' workers made (stages, audit copies with their task files),
// each held from the moment it exists until its removal ran. They are owned here, not by a job's
// answer: the session's end (close) removes every one held, whether or not its answer was
// delivered, and a worker that makes one after the end removes it itself.
type allocs struct {
	mu     sync.Mutex
	held   map[string]bool
	closed bool
}

func newAllocs() *allocs { return &allocs{held: map[string]bool{}} }

// hold records dir; false once the session has ended, and the caller removes dir itself.
func (a *allocs) hold(dir string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return false
	}
	a.held[dir] = true
	return true
}

// remove removes each dir and lets it go: a worker's or an ending candidate's cleanup.
func (a *allocs) remove(dirs ...string) {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		_ = os.RemoveAll(d)
		a.mu.Lock()
		delete(a.held, d)
		a.mu.Unlock()
	}
}

// keep is a worker's last word on dir, once it has written all it will: nil while the session
// lives (dir stays held, its candidate's to remove, and an end after this removes it), or
// errSessionEnded when the session ended while it worked: dir, removed by the end and perhaps
// written again since (a clone still running), is removed here. Either way no dir outlives the end.
func (a *allocs) keep(dir string) error {
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		_ = os.RemoveAll(dir)
		return errSessionEnded
	}
	return nil
}

// close removes every dir held and refuses any held after it.
func (a *allocs) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	for d := range a.held {
		_ = os.RemoveAll(d)
	}
	clear(a.held)
}

// begin makes c live, its card shown by card and taken down by hide (the kind's own), unless another
// candidate holds its target: then c's stage is removed and why is returned.
func (cs *candidates) begin(c *candidate, card, hide func(*candidate)) error {
	if cs.ops[c.key()] != nil {
		cs.allocs.remove(c.stage)
		return fmt.Errorf("%s is already being installed or audited", c.name)
	}
	cs.allocs.hold(c.stage) // a stage made outside cloneStage (a test's) is owned from here
	c.card, c.hide = card, hide
	cs.ops[c.key()] = c
	return nil
}

// show puts c's card up. A card it replaces, still confirming, is suspended to its toast first, so
// its own Cancel (a replaced question's OnCancel) finds it suspended and ends nothing. A card of
// another kind is then taken down; one of c's kind is the same dialog, which c's card takes over
// (askPlugin sets PluginConfirm anew, askPackage's question replaces the one asked).
func (cs *candidates) show(c *candidate) {
	if o := cs.onCard; o != nil && o != c && o.state == opConfirming {
		cs.suspend(o, cs.waits(o))
		if o.kind != c.kind && o.hide != nil {
			o.hide(o)
		}
	}
	cs.onCard = c
	c.state = opConfirming
	c.card(c)
}

// waits is the toast of c, suspended while another card is up: a sentence for each kind.
func (cs *candidates) waits(c *candidate) string {
	switch c.kind {
	case regindex.KindLua:
		return cs.c.Trf("autodoc.notice.candidate.the_lua_package_waits_click_to_install_or_cancel", c.name)
	case regindex.KindKBType:
		return cs.c.Trf("autodoc.notice.candidate.the_kb_type_waits_click_to_install_or_cancel", c.name)
	}
	return cs.c.Trf("autodoc.notice.candidate.the_plugin_waits_click_to_install_or_cancel", c.name)
}

// suspend takes c's card down and leaves its toast, which reopens the card.
func (cs *candidates) suspend(c *candidate, text string) {
	if cs.onCard == c {
		cs.onCard = nil
	}
	c.state = opSuspended
	c.toasted = true
	cs.c.NotifyOngoingClick(c.toastID(), text, func() { cs.reopen(c) })
}

// reopen is a click on c's toast: its card again while c is suspended; once c has ended, a click
// says so and does nothing else.
func (cs *candidates) reopen(c *candidate) {
	switch c.state {
	case opSuspended:
		cs.show(c)
	case opEnded:
		cs.c.Notify(cs.c.Tr("autodoc.notice.candidate.that_install_has_ended"))
	}
}

// claim is Install's guard: true once, for a candidate confirming or suspended, which is then
// installing. A double click, or a card left over, does nothing the second time.
func (cs *candidates) claim(c *candidate) bool {
	if c == nil || (c.state != opConfirming && c.state != opSuspended) {
		return false
	}
	c.state = opInstalling
	if cs.onCard == c {
		cs.onCard = nil
	}
	return true
}

// cancel is the card's No: c ends while its card is up (confirming); false when it did not. A
// suspended candidate's card is down, so a Cancel reaching it is a replaced card's, never the user's.
func (cs *candidates) cancel(c *candidate, outcome string) bool {
	if c == nil || c.state != opConfirming {
		return false
	}
	cs.end(c, outcome)
	return true
}

// end ends c exactly once: its stage (the candidate's clone; never the installed tree, and gone
// already when Install moved it in) and every audit copy with its task file are removed off the
// loop, its toast ends with outcome, and it leaves the live candidates.
func (cs *candidates) end(c *candidate, outcome string) {
	if c == nil || c.state == opEnded {
		return
	}
	c.state = opEnded
	if cs.ops[c.key()] == c {
		delete(cs.ops, c.key())
	}
	if cs.onCard == c {
		cs.onCard = nil
	}
	if cs.auditing == c {
		cs.auditing = nil
	}
	dirs := append([]string{c.stage}, c.auditCopies...)
	cs.c.Go(func(context.Context) any { cs.allocs.remove(dirs...); return nil }, func(any) {})
	if c.toasted {
		cs.c.NotifyDone(c.toastID(), cmp.Or(outcome, cs.c.Trf("autodoc.notice.candidate.ended", c.name)))
	}
}

// ---------------------------------------------------------------- Audit…

// audit is the card's Audit…: the card goes down and the agent picker opens, the default agent
// first. Cancel there brings the card back.
func (cs *candidates) audit(c *candidate) {
	if c == nil || c.state != opConfirming {
		return
	}
	if cs.onCard == c {
		cs.onCard = nil
	}
	cs.auditing = c
	// Audit… is the card's button, run before the card's own dismissal, which closes every dialog
	// above the card first (golib Modal.Dismiss): the picker, or the card again, opens once the card
	// has closed.
	cs.c.Post(func() {
		if cs.auditing != c || c.state != opConfirming {
			return
		}
		agents := defaultFirst(assignable(cs.c.AgentEntries()), cs.c.Prefs().Value(prefAgentDefaultCodename))
		if len(agents) == 0 {
			cs.auditing = nil
			cs.c.Notify(cs.c.Tr("autodoc.notice.candidate.no_autodoc_agent_to_audit_with_add_one_in_system"))
			cs.show(c)
			return
		}
		cs.pick.setAll(agents)
		cs.pick.open()
	})
}

// auditCancelled is the agent picker closed without an agent: the card again.
func (cs *candidates) auditCancelled() {
	c := cs.auditing
	cs.auditing = nil
	if c != nil && c.state == opConfirming {
		cs.show(c)
	}
}

// auditWith hands c's audit to agent a: a fresh copy of the pristine stage and its task file, made
// off the loop; then c is suspended to its toast and the task handed over. A copy an agent may
// still be reading is never reused or removed before c ends.
//
// While the copy is made c is opCopying, which only this sets and only its answer (or c's end)
// leaves: an old toast, a stale card's Install or Cancel and another Audit all refuse it, and the
// answer moves c on only if c is still copying, the attempt it was made for.
func (cs *candidates) auditWith(a AgentEntry) {
	c := cs.auditing
	cs.auditing = nil
	if c == nil || c.state != opConfirming {
		return
	}
	c.state = opCopying
	if c.toasted {
		cs.c.NotifyOngoingClick(c.toastID(), cs.c.Trf("autodoc.notice.candidate.copying_for_its_audit", c.name), func() { cs.reopen(c) })
	}
	type copied struct {
		root, task string
		err        error
	}
	cs.c.Go(func(ctx context.Context) any {
		root, task, err := auditCopy(ctx, c, cs.allocs)
		return copied{root, task, err}
	}, func(v any) {
		r := v.(copied)
		if c.state != opCopying { // ended while it was copied: the copy is its own to remove
			cs.allocs.remove(r.root)
			return
		}
		if r.err != nil {
			c.state = opConfirming
			cs.c.Notify(cs.c.Trf("autodoc.notice.candidate.audit_of_not_handed_over", c.name, r.err.Error()))
			cs.show(c)
			return
		}
		c.auditCopies = append(c.auditCopies, r.root)
		cs.handOver(c, a, r.task)
	})
}

// handOver gives the audit task to a. done(nil) means typed and submitted, never that the audit
// ran: the toast says it was handed over, and no more. A refusal ends the toast and reopens the card.
func (cs *candidates) handOver(c *candidate, a AgentEntry, task string) {
	cs.suspend(c, cs.c.Trf("autodoc.notice.candidate.audit_of_handed_to_click_to_install_or_cancel", c.name, a.Name))
	failed := func(err error) {
		if c.state != opSuspended {
			return
		}
		why := err.Error()
		if errors.Is(err, ErrAgentCancelled) {
			why = cs.c.Trf("autodoc.notice.candidate.you_didn_t_start", a.Name)
		}
		c.toasted = false
		cs.c.NotifyDone(c.toastID(), cs.c.Trf("autodoc.notice.candidate.audit_of_not_delivered", c.name, why))
		cs.show(c)
	}
	ref := TaskRef{Title: "Audit " + c.name + " at " + regindex.Short(c.commit), Path: task}
	done := func(err error) {
		if err != nil {
			failed(err)
		}
	}
	if err := cs.c.HandTask(AgentRef{Codename: a.Ref.Codename}, ref, "Audit the repository as the task file says. Read it; do not run or change it.", done); err != nil {
		failed(err)
	}
}

// defaultFirst is agents with the default one first, the rest in their order.
func defaultFirst(agents []AgentEntry, codename string) []AgentEntry {
	if i := slices.IndexFunc(agents, func(e AgentEntry) bool { return e.Ref.Codename == codename }); i > 0 {
		agents = append([]AgentEntry{agents[i]}, slices.Delete(slices.Clone(agents), i, i+1)...)
	}
	return agents
}

// pristine is why c's stage may not be built or copied: a file changed, added or ignored in it, or
// its HEAD moved off c.commit. The audit copies are clones of their own, so an agent working in one
// can't change the stage; this catches anything that touched the stage anyway.
func pristine(ctx context.Context, c *candidate) error {
	status, err := git(ctx, pluginCloneTimeout, c.stage, "status", "--porcelain=v1", "--ignored", "--untracked-files=all")
	if err != nil {
		return err
	}
	head, err := git(ctx, pluginCloneTimeout, c.stage, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if status != "" || head != c.commit {
		return fmt.Errorf("the staged copy of %s changed; stage it again", c.name)
	}
	return nil
}

// auditCopy is a fresh copy of c's pristine stage for one Audit…: a root of its own holding the
// copy and, beside it (never in it), the task file. The root is held in own from the moment it
// exists, so the session's end removes it even if this answer never reaches the loop.
func auditCopy(ctx context.Context, c *candidate, own *allocs) (root, task string, err error) {
	if err := pristine(ctx, c); err != nil {
		return "", "", err
	}
	root, err = os.MkdirTemp("", "autodoc-audit-")
	if err != nil {
		return "", "", err
	}
	fail := func(err error) (string, string, error) { own.remove(root); return "", "", err }
	if !own.hold(root) {
		return fail(errSessionEnded)
	}
	dir := filepath.Join(root, c.name)
	if _, err := git(ctx, pluginCloneTimeout, "", "clone", "--quiet", "--local", "--no-hardlinks", "--", c.stage, dir); err != nil {
		return fail(err)
	}
	task = filepath.Join(root, "audit-task.md")
	if err := os.WriteFile(task, []byte(auditTask(c, dir)), 0o600); err != nil {
		return fail(err)
	}
	if err := own.keep(root); err != nil { // the session ended while the copy was made
		return "", "", err
	}
	return root, task, nil
}

// errSessionEnded is a worker's answer when the session ended while it worked: what it made is gone.
var errSessionEnded = errors.New("the session ended")

// auditTask is the task file: the prompt every audit reads (docs/audit-prompt.md), then this one's
// copy, commit and repository, and the advisory sentence.
func auditTask(c *candidate, dir string) string {
	return autodoc.AuditPrompt + "\n## This audit\n\n" +
		"- What: the " + kindNoun(c.kind) + " " + c.name + "\n" +
		"- The copy: " + dir + "\n" +
		"- The commit: " + c.commit + "\n" +
		"- From: " + c.repo + "\n\n" +
		auditNote + "\n"
}
