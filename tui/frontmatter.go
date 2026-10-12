package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// FRONTMATTER — the workspace's schema, and the open file checked against it (ADR 0212 §5).
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
	m := asMap(v)
	active, _ := m["active"].(bool)
	return schemaInfo{path: str(m, "path"), active: active, fields: num(m, "fields"), err: str(m, "error"), line: num(m, "line")}
}

// state is the schema's standing, a sentence for the settings dialog.
func (s schemaInfo) state() string {
	var b strings.Builder
	switch {
	case s.path == "":
		b.WriteString("no schema: files are not checked, and field:value searches as words")
	case s.active:
		fmt.Fprintf(&b, "active: %d fields", s.fields)
	default:
		b.WriteString("not active")
	}
	if s.err != "" {
		b.WriteString(" · ")
		if s.line > 0 {
			fmt.Fprintf(&b, "line %d: ", s.line)
		}
		b.WriteString(s.err)
		if s.active {
			b.WriteString(" (the last valid schema stays in use)")
		}
	}
	return b.String()
}

// validateSoon checks the page's text after wait; a later call supersedes it. A typing pause waits
// the rest of validateDelay, so the check comes validateDelay after the last keystroke.
func (h *Host) validateSoon(wait time.Duration) {
	tk := h.ticket(&h.fmGen, TicketFile)
	h.after(wait, func() {
		if tk.Current() {
			h.validateNow(tk)
		}
	})
}

// validateNow sends the editor's text to doc.validate. Only Markdown has frontmatter.
func (h *Host) validateNow(tk Ticket) {
	p := h.file.path
	if !h.file.open {
		p = untitled + ".md"
	}
	if h.ws == "" || h.file.outside() || !h.resolver().Of(p).Traits().Frontmatter {
		h.showDiagnostics(nil)
		return
	}
	ws, text := h.ws, h.page.Text
	do(h, func(ctx context.Context) answerOf[[]fmDiagnostic] {
		res, err := h.call(ctx, "doc.validate", ws, p, text)
		if err != nil {
			return answerOf[[]fmDiagnostic]{err: err}
		}
		var out []fmDiagnostic
		for _, d := range asList(asMap(res)["diagnostics"]) {
			m := asMap(d)
			out = append(out, fmDiagnostic{field: str(m, "field"), line: num(m, "line"), message: str(m, "message")})
		}
		return answerOf[[]fmDiagnostic]{v: out}
	}, func(a answerOf[[]fmDiagnostic]) {
		if !tk.Current() {
			return // later text was sent, another file opened, or another workspace or connection
		}
		if a.err != nil {
			h.showDiagnostics(nil) // a check that could not run says nothing about the file
			return
		}
		h.showDiagnostics(a.v)
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
	line := fmt.Sprintf("⚠ frontmatter line %d: %s", ds[0].line, ds[0].message)
	if len(ds) > 1 {
		line += fmt.Sprintf("  (+%d more)", len(ds)-1)
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
