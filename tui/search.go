package tui

import (
	"context"
	"fmt"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE NOTES PANE — every note of the workspace, or a search's hits.

type rowOf = tuidecl.Row

// listNotes fills the pane with every note, in path order.
func (h *Host) listNotes() {
	h.listSeq++
	seq, ep, ws := h.listSeq, h.epoch, h.ws
	type answer struct {
		paths []string
		err   error
	}
	do(h, func(ctx context.Context) answer {
		var out []string
		after := ""
		for {
			res, err := h.call(ctx, "index.list", ws, after, int64(1000))
			if err != nil {
				return answer{err: err}
			}
			m := asMap(res)
			for _, d := range asList(m["docs"]) {
				after = str(asMap(d), "path")
				out = append(out, after)
			}
			if more, _ := m["more"].(bool); !more {
				return answer{paths: out}
			}
		}
	}, func(a answer) {
		if seq != h.listSeq || ep != h.epoch {
			return
		}
		if a.err != nil {
			h.failed("notes", a.err)
			return
		}
		h.notesAll = a.paths
		h.prog.listed = true
		h.showPaths(a.paths)
		h.set("App.resultsTitle", fmt.Sprintf("notes (%d)", len(a.paths)))
	})
}

// search fills the pane with the query's hits, a note once, best first.
func (h *Host) search(q string) {
	q = strings.TrimSpace(q)
	h.set("App.lastQuery", q)
	if q == "" {
		h.listNotes()
		return
	}
	h.listSeq++
	seq, ep, ws := h.listSeq, h.epoch, h.ws
	type answer struct {
		paths          []string
		mode, semantic string
		err            error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "search.query", ws, q, map[string]any{"limit": int64(100)})
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		seen := map[string]bool{}
		var out []string
		for _, hit := range asList(m["hits"]) {
			if p := str(asMap(hit), "path"); !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
		return answer{paths: out, mode: str(m, "mode_used"), semantic: str(m, "semantic")}
	}, func(a answer) {
		if seq != h.listSeq || ep != h.epoch {
			return
		}
		if a.err != nil {
			h.failed("search", a.err)
			return
		}
		h.prog.listed = false
		h.showPaths(a.paths)
		h.set("App.resultsTitle", fmt.Sprintf("search: %s (%d)", q, len(a.paths)))
		h.setStatus(fmt.Sprintf("%d notes · %s search · semantic %s", len(a.paths), a.mode, a.semantic))
		h.keep(h.p.Call("notes", "forceActiveFocus")) // the hits are what a search was for: Enter opens the first
	})
}

func (h *Host) showPaths(paths []string) {
	rows := make([]rowOf, len(paths))
	for i, p := range paths {
		rows[i] = rowOf{"key": p, "path": p}
	}
	h.results.Reset(rows)
}

// openResult opens the pane's row.
func (h *Host) openResult(i int) {
	if i >= 0 && i < h.results.Len() {
		h.openPath(h.results.At(i)["path"].(string))
	}
}

// openPicker opens File › Open over the workspace's notes.
func (h *Host) openPicker() {
	h.pickerFilter("")
	h.open("noteOpen")
}

// pickerFilter keeps the notes whose path holds text (case aside).
func (h *Host) pickerFilter(text string) {
	text = strings.ToLower(text)
	var rows []rowOf
	for _, p := range h.notesAll {
		if strings.Contains(strings.ToLower(p), text) {
			rows = append(rows, rowOf{"key": p, "path": p})
		}
	}
	h.picker.Reset(rows)
	h.set("App.pickerStatus", fmt.Sprintf("%d of %d notes · Enter opens", len(rows), len(h.notesAll)))
}

// pickerSelect opens the picker's row.
func (h *Host) pickerSelect(i int) {
	if i < 0 || i >= h.picker.Len() {
		return
	}
	p := h.picker.At(i)["path"].(string)
	h.closeDialog("noteOpen")
	h.openPath(p)
}

// the msgpack vocabulary, read

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asList(v any) []any         { l, _ := v.([]any); return l }
func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}
