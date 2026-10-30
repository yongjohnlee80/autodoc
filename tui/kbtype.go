package tui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc"
	"github.com/yongjohnlee80/autodoc/core/kbtype"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// KB TYPES — a workspace scaffolded from a KB type's git repo
// (docs/decisions/contract.md#kb-types-are-git-repos-of-files). The add dialog's KB type field
// stages a type: a shallow clone, read and validated by core/kbtype, never run. Add then writes its
// plan through the daemon's workspace.scaffold, between workspace.add and workspace.configure. A
// choice is fenced by a Ticket, so only the latest choice's stage can land.

// kbTypeChoice is one of the KB type field's rows: None has no URL, Custom URL… is asked for.
type kbTypeChoice struct{ name, title, url string }

// builtinKBTypes are the first-party types, shown until the registry lists them
// (docs/decisions/plugins.md#the-registry), and kept for offline use after.
var builtinKBTypes = []kbTypeChoice{
	{"autovim.kb", "AutoVim coding KB", "https://github.com/yongjohnlee80/autovim.kb"},
	{"zettelkasten.kb", "Zettelkasten", "https://github.com/yongjohnlee80/zettelkasten.kb"},
	{"para.kb", "PARA", "https://github.com/yongjohnlee80/para.kb"},
	{"slipbox.kb", "Slip-box", "https://github.com/yongjohnlee80/slipbox.kb"},
}

// noneNote is what None says, in Johno's words for a coding repo (the dialog task): the English of
// noneNoteText, which says it in the UI's language.
const noneNote = "Nothing is created. If the location is a coding repo, consider turning off embedding in the advanced option."

func (h *Host) noneNoteText() string {
	return h.tr("autodoc.text.kbtype.nothing_is_created_if_the_location_is_a_coding_r")
}

// kbStageLimit is the most a staged clone may hold, checked before it is read: the limits of
// core/kbtype bound what is applied, this bounds what a clone may leave on disk.
const kbStageLimit = 16 << 20

// kbTypeRows are the KB type field's rows: None, the types (the registry's, else the built-ins),
// then Custom URL…. None's and Custom URL…'s labels are in the UI's language, their keys "" and
// "custom".
func (h *Host) kbTypeRows() []rowOf {
	rows := []rowOf{{"key": "", "label": h.tr("autodoc.text.kbtype.none_an_existing_folder_or_a_coding_repo")}}
	for _, c := range h.kbTypeChoices() {
		rows = append(rows, rowOf{"key": c.name, "label": c.title})
	}
	return append(rows, rowOf{"key": "custom", "label": h.tr("autodoc.text.kbtype.custom_url")})
}

// stagedKBType is a type staged for the add dialog: its clone, read and validated.
type stagedKBType struct {
	url, stage, commit string // commit in full
	t                  *kbtype.Type
	err                error
}

// stageKBType clones url into a stage of its own and reads it as a KB type. It reuses the plugin
// installer's git helper (plugininstall.go): no prompts, a timeout, a shallow clone. Nothing in the
// clone runs. On any failure the stage is removed.
func stageKBType(ctx context.Context, url string) stagedKBType {
	s := stagedKBType{url: url}
	fail := func(err error) stagedKBType {
		if s.stage != "" {
			_ = os.RemoveAll(s.stage)
		}
		return stagedKBType{url: url, err: err}
	}
	// https only: a KB type runs nothing, and git's other transports (ext::, a local path's hooks)
	// are not ones a type needs
	if !strings.HasPrefix(url, "https://") {
		return fail(errors.New("a KB type is cloned over https:// only"))
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fail(errors.New("a KB type needs git"))
	}
	stage, err := os.MkdirTemp("", "autodoc-kbtype-")
	if err != nil {
		return fail(err)
	}
	s.stage = stage
	if _, err := git(ctx, pluginCloneTimeout, "", "clone", "--depth", "1", "--", url, stage); err != nil {
		return fail(err)
	}
	if s.commit, err = git(ctx, pluginCloneTimeout, stage, "rev-parse", "HEAD"); err != nil {
		return fail(err)
	}
	if size, err := dirSize(stage); err != nil {
		return fail(err)
	} else if size > kbStageLimit {
		return fail(fmt.Errorf("the repository holds over %d MiB: not a KB type", kbStageLimit>>20))
	}
	if s.t, err = kbtype.Read(stage); err != nil {
		return fail(err)
	}
	return s
}

func dirSize(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			n += info.Size()
		}
		return nil
	})
	return n, err
}

// stageKB stages url: stageKBType's clone, or a test's stager.
func (h *Host) stageKB(ctx context.Context, url string) stagedKBType {
	if h.kbStage != nil {
		return h.kbStage(ctx, url)
	}
	return stageKBType(ctx, url)
}

// kbTypePicked is the KB type field's choice, row i of kbTypeRows.
func (h *Host) kbTypePicked(i int) {
	rows := h.kbTypeRows()
	if i < 0 || i >= len(rows) {
		return
	}
	key, _ := rows[i]["key"].(string)
	h.kbTypeKey = key // kept across the list's reload (registryLoaded)
	switch key {
	case "":
		h.pickKBType("")
	case "custom":
		h.dropKBType()
		h.set("App.kbTypeCustomShown", true)
		h.set("App.kbTypeSummary", h.tr("autodoc.text.kbtype.a_kb_type_s_git_url_then_stage"))
	default:
		for _, c := range h.kbTypeChoices() {
			if c.name == key {
				h.pickKBType(c.url)
			}
		}
	}
}

// kbTypeURL stages the custom URL typed.
func (h *Host) kbTypeURL(url string) {
	if url = strings.TrimSpace(url); url != "" {
		h.pickKBType(url)
	}
}

// pickKBType stages url for the open add dialog ("" is None). Every pick, and the dialog's opening
// and closing (dropKBType), bump h.kbTypeSeq, so only the latest pick's stage can land: a stale
// one is removed unseen (docs/decisions/contract.md#choosing-a-type-stages-it).
func (h *Host) pickKBType(url string) {
	h.dropKBType()
	h.set("App.kbTypeCustomShown", false)
	if url == "" {
		h.set("App.kbTypeSummary", h.noneNoteText())
		return
	}
	tk := h.ticket(&h.kbTypeSeq, TicketSession)
	h.set("App.kbTypeSummary", h.trf("autodoc.text.kbtype.staging", url))
	do(h, func(ctx context.Context) stagedKBType { return h.stageKB(ctx, url) }, func(s stagedKBType) {
		if !tk.Current() {
			if s.stage != "" {
				_ = os.RemoveAll(s.stage)
			}
			return
		}
		if s.err != nil {
			h.set("App.kbTypeSummary", h.trf("autodoc.text.kbtype.not_a_kb_type", s.err.Error()))
			return
		}
		h.kbStaged = &s
		h.set("App.kbTypeSummary", kbTypeSummary(core{h}, &s))
	})
}

// dropKBType forgets the staged type and a partial add's kept one, removing their stages; a pick
// still staging is stale from now.
func (h *Host) dropKBType() {
	h.kbTypeSeq++
	if h.kbStaged != nil {
		_ = os.RemoveAll(h.kbStaged.stage)
		h.kbStaged = nil
	}
	if r := h.kbResume; r != nil {
		h.kbResume = nil
		if r.s != h.kbStaged {
			_ = os.RemoveAll(r.s.stage)
		}
	}
}

// kbResume is a partial add's unfinished type: the workspace it added and the type as staged. The
// dialog reopened on that workspace finishes the files, create-only, before its Save configures,
// so a retry never adds again and never writes into another workspace (Zen #240 r0 R2).
type kbResume struct {
	ws string
	s  *stagedKBType
}

// keepForResume holds s for workspace ws after a partial add: the stage is no longer the add
// dialog's pick (that dialog is gone), and is kept until the resume ends or the dialog is cancelled.
func (h *Host) keepForResume(ws string, s *stagedKBType) {
	h.kbTypeSeq++ // no pick still staging may land on the reopened dialog
	h.kbStaged = nil
	h.kbResume = &kbResume{ws: ws, s: s}
}

// kbTypeSummary is what the field shows of a staged type: its folders and purposes, and the
// settings it proposes.
func kbTypeSummary(t wording, s *stagedKBType) string {
	var b strings.Builder
	b.WriteString(t.Trf("autodoc.text.kbtype.at", s.t.Title, s.commit[:min(7, len(s.commit))]) + "\n")
	width := 0
	for _, f := range s.t.Folders {
		width = max(width, len(f.Path)+1)
	}
	for _, f := range s.t.Folders {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, f.Path+"/", f.Purpose)
	}
	w := s.t.Workspace
	var proposes []string
	if len(w.Include) > 0 {
		proposes = append(proposes, t.Trf("autodoc.text.kbtype.include", strings.Join(w.Include, "; ")))
	}
	if w.Schema != "" {
		proposes = append(proposes, t.Trf("autodoc.text.kbtype.schema", w.Schema))
	}
	if w.EmbeddingPolicy != "" {
		proposes = append(proposes, t.Trf("autodoc.text.kbtype.embedding", w.EmbeddingPolicy))
	}
	if len(proposes) > 0 {
		b.WriteString(t.Trf("autodoc.text.kbtype.proposes_where_you_haven_t_changed_the_field", strings.Join(proposes, " · ")))
	}
	return strings.TrimRight(b.String(), "\n")
}

// withKBType is f with t's proposed settings in each field still as the dialog opened it: a field
// the user changed is theirs.
func (b settingsBase) withKBType(f settingsForm, t *kbtype.Type) settingsForm {
	opened := b.form()
	w := t.Workspace
	if len(w.Include) > 0 && f.include == opened.include {
		f.include = strings.Join(w.Include, "; ")
	}
	if w.Schema != "" && f.schema == opened.schema {
		f.schema = w.Schema
	}
	if i := slices.Index(policyChoices, w.EmbeddingPolicy); i >= 0 && f.policy == opened.policy {
		f.policy = i
	}
	return f
}

// kbApply is how far an apply got, file by file, from workspace.scaffold's answers.
type kbApply struct {
	created, kept, updated, skipped []string
	refused                         []string // "path: why"
}

// text is how far the apply got, in t's language.
func (a kbApply) text(t wording) string {
	s := t.Trf("autodoc.text.kbtype.created_kept", len(a.created), len(a.kept))
	if len(a.updated) > 0 {
		s += ", " + t.Trf("autodoc.text.kbtype.updated", len(a.updated))
	}
	if len(a.skipped) > 0 {
		s += ", " + t.Trf("autodoc.text.kbtype.skipped", len(a.skipped))
	}
	if len(a.refused) > 0 {
		s += ", " + t.Trf("autodoc.text.kbtype.refused", len(a.refused), a.refused[0])
	}
	return s
}

// errKBRefused is an apply whose every call answered but some file was refused.
var errKBRefused = errors.New("some files were refused")

// applyKBType sends plan to workspace ws by workspace.scaffold, in batches of rpc.MaxScaffoldFiles:
// one call each. ctx's end stops between batches. A call's error ends the apply with what landed so
// far; a refused file is reported, and makes the apply's error errKBRefused once all have been sent.
func (h *Host) applyKBType(ctx context.Context, ws string, plan []kbtype.Write) (kbApply, error) {
	var a kbApply
	for start := 0; start < len(plan); start += rpc.MaxScaffoldFiles {
		if err := ctx.Err(); err != nil {
			return a, err
		}
		batch := plan[start:min(start+rpc.MaxScaffoldFiles, len(plan))]
		files := make([]any, len(batch))
		for i, w := range batch {
			m := map[string]any{"path": w.Rel, "content": string(w.Text)}
			if w.Managed {
				m["managed"] = true
			}
			if w.Immutable != "" {
				m["immutable"] = w.Immutable
			}
			files[i] = m
		}
		res, err := h.call(ctx, "workspace.scaffold", ws, files)
		for _, r := range asList(res) {
			m := asMap(r)
			p := str(m, "path")
			switch str(m, "outcome") {
			case rpc.ScaffoldCreated:
				a.created = append(a.created, p)
			case rpc.ScaffoldKept:
				a.kept = append(a.kept, p)
			case rpc.ScaffoldUpdated:
				a.updated = append(a.updated, p)
			case rpc.ScaffoldSkipped:
				a.skipped = append(a.skipped, p)
			default:
				a.refused = append(a.refused, p+": "+str(m, "error"))
			}
		}
		if err != nil {
			return a, err
		}
	}
	if len(a.refused) > 0 {
		return a, errKBRefused
	}
	return a, nil
}

// kbPlan is t's plan for a KB, AutoDoc's own KB_OPERATIONS.md included.
func (h *Host) kbPlan(s *stagedKBType) []kbtype.Write {
	return s.t.Plan(kbtype.Vars{Date: time.Now().Format(time.DateOnly), AutoDocVersion: h.aboutInfo.Version,
		URL: s.url, Commit: s.commit}, autodoc.KBOperations)
}

// kbTypeApply is kbtype.apply: the KB type named (a built-in's name) or at a git URL, applied to
// the active workspace. Create-only, so it also finishes an add whose type stopped part way.
func (h *Host) kbTypeApply(x Invocation) error {
	if len(x.Args) != 1 {
		return errors.New("kbtype.apply: takes a KB type's name or git URL")
	}
	url := x.Args[0]
	for _, c := range h.kbTypeChoices() { // the registry's as last loaded, else the built-ins
		if c.name == url {
			url = c.url
		}
	}
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("kbtype.apply: %s is not a built-in KB type's name or an https:// git URL", url)
	}
	ws, ok := h.activeWorkspaceInfo()
	if !ok {
		return errors.New("kbtype.apply: no workspace is open")
	}
	h.notifyOngoing("kbtype", h.trf("autodoc.notice.kbtype.staging", url))
	type answer struct {
		s   stagedKBType
		a   kbApply
		err error
	}
	do(h, func(ctx context.Context) answer {
		s := h.stageKB(ctx, url)
		if s.err != nil {
			return answer{s: s, err: s.err}
		}
		defer os.RemoveAll(s.stage)
		a, err := h.applyKBType(ctx, ws.name, h.kbPlan(&s))
		return answer{s: s, a: a, err: err}
	}, func(r answer) {
		switch {
		case r.s.err != nil:
			h.notifyDone("kbtype", h.trf("autodoc.notice.kbtype.not_a_kb_type", r.err.Error()))
		case r.err != nil:
			h.notifyDone("kbtype", h.trf("autodoc.notice.kbtype.stopped_part_way", ws.name, r.s.t.Title, r.a.text(core{h}), wireMessage(r.err)))
		default:
			h.notifyDone("kbtype", h.trf("autodoc.notice.kbtype.applied", ws.name, r.s.t.Title, r.a.text(core{h})))
		}
	})
	return nil
}
