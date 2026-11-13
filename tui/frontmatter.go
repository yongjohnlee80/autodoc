package tui

import (
	"context"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"time"
)

// FRONTMATTER — a workspace's schema, and the open file checked against its own workspace's
// (docs/decisions/search.md#frontmatter-schemas).
//
// The file is checked by the daemon's own validator (doc.validate), the one the index uses, so the
// line over the page and the index never disagree. It is checked as it is typed, a moment after
// the last key, and the answer is applied only if no later text was sent since. A diagnostic never
// blocks a save: the file is the user's, and the schema only describes it.

// validateDelay is how long the text must rest before it is checked.
const validateDelay = 300 * time.Millisecond

// suggestedSchema is where the manager suggests a workspace's schema, under its root.
const suggestedSchema = ".autodoc/schema.yaml"

// schemaInfo is a workspace's schema as workspace.list reports it.
type schemaInfo struct {
	path   string
	active bool
	fields int64
	err    string
	line   int64
}

func readSchemaInfo(v any) schemaInfo {
	m := feature.AsMap(v)
	active, _ := m["active"].(bool)
	return schemaInfo{path: feature.Str(m, "path"), active: active, fields: feature.Num(m, "fields"), err: feature.Str(m, "error"), line: feature.Num(m, "line")}
}

// state is the schema's standing, a sentence for the settings dialog.
func (s schemaInfo) state(t feature.Wording) string {
	var out string
	switch {
	case s.path == "":
		out = t.Tr("autodoc.text.frontmatter.no_schema_files_are_not_checked_and_field_value")
	case s.active:
		out = t.Trf("autodoc.text.frontmatter.active_fields", s.fields)
	default:
		out = t.Tr("autodoc.text.frontmatter.not_active")
	}
	switch {
	case s.err == "":
		return out
	case s.line > 0 && s.active:
		return out + " · " + t.Trf("autodoc.text.frontmatter.line_the_last_valid_schema_stays_in_use", s.line, s.err)
	case s.line > 0:
		return out + " · " + t.Trf("autodoc.text.frontmatter.line", s.line, s.err)
	case s.active:
		return out + " · " + t.Trf("autodoc.text.frontmatter.the_last_valid_schema_stays_in_use", s.err)
	}
	return out + " · " + s.err
}

// validateSoon checks the page's text after wait; a later call supersedes it. A typing pause waits
// the rest of validateDelay, so the check comes validateDelay after the last keystroke.
func (h *Host) validateSoon(wait time.Duration) {
	tk := h.ticket(&h.fmGen, feature.TicketFile)
	h.after(wait, func() {
		if tk.Current() {
			h.validateNow(tk)
		}
	})
}

// checkedIn is the workspace whose schema checks the page: an open file's own workspace, which its
// path is relative to, or the workspace in use for the draft. A buffer stays open, in its own
// workspace, when another is entered (buffers.go), so the two differ: checked against the
// workspace in use, a file from another one would be judged by a schema that does not describe it,
// and at a path that is not its own. "" for a file outside every workspace, which is not checked.
func (h *Host) checkedIn() string {
	if h.file.open {
		return h.file.ws
	}
	return h.ws
}

// validateNow sends the editor's text to doc.validate, in the workspace checkedIn names. Only
// Markdown has frontmatter.
func (h *Host) validateNow(tk feature.Ticket) {
	p := h.file.path
	if !h.file.open {
		// a draft's hint says its kind: a .sql draft has no frontmatter; a Markdown one is checked as
		// the draft always was
		p = h.file.hint
		if h.resolver().Of(p).Traits().Frontmatter {
			p = untitled + ".md"
		}
	}
	ws := h.checkedIn()
	if ws == "" || !h.resolver().Of(p).Traits().Frontmatter {
		h.showDiagnostics(nil)
		return
	}
	text := h.page.Text
	do(h, func(ctx context.Context) feature.AnswerOf[[]fmDiagnostic] {
		res, err := h.call(ctx, "doc.validate", ws, p, text)
		if err != nil {
			return feature.AnswerFailed[[]fmDiagnostic](err)
		}
		var out []fmDiagnostic
		for _, d := range feature.AsList(feature.AsMap(res)["diagnostics"]) {
			m := feature.AsMap(d)
			out = append(out, fmDiagnostic{field: feature.Str(m, "field"), line: feature.Num(m, "line"), message: feature.Str(m, "message")})
		}
		return feature.AnswerOf[[]fmDiagnostic]{Value: out}
	}, func(a feature.AnswerOf[[]fmDiagnostic]) {
		if !tk.Current() {
			return // later text was sent, another file opened, or another workspace or connection
		}
		if a.Err() != nil {
			h.showDiagnostics(nil) // a check that could not run says nothing about the file
			return
		}
		h.showDiagnostics(a.Value)
	})
}

type fmDiagnostic struct {
	field   string
	line    int64
	message string
}

// showDiagnostics puts the first diagnostic over the page, with how many follow; none hides it.
func (h *Host) showDiagnostics(ds []fmDiagnostic) {
	h.fmDiagnostics = ds
	if len(ds) == 0 {
		h.set("App.diagnosticsShown", false)
		h.set("App.diagnosticsLine", "")
		return
	}
	line := h.trf("autodoc.text.frontmatter.frontmatter_line", ds[0].line, ds[0].message)
	if len(ds) > 1 {
		line = h.trf("autodoc.text.frontmatter.frontmatter_line_more", ds[0].line, ds[0].message, len(ds)-1)
	}
	h.set("App.diagnosticsLine", line)
	h.set("App.diagnosticsShown", true)
}

// clearDiagnostics forgets the file's diagnostics and any check still under way.
func (h *Host) clearDiagnostics() {
	h.fmGen++
	h.showDiagnostics(nil)
}

// textExtensions are the active workspace's own plain-text extensions.
func (h *Host) textExtensions() []string {
	if h.file.outside() {
		return nil // in no workspace: the built-in kinds and the build's registrations alone
	}
	if w, ok := h.activeWorkspaceInfo(); ok {
		return w.textExtensions
	}
	return nil
}
