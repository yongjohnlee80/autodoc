package tui

import (
	"context"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE WORKSPACE'S NOTES — the list the pickers filter, read when a workspace is entered and after
// a note is made.

type rowOf = tuidecl.Row

// listNotes reads every note of the workspace in use, in path order.
func (h *Host) listNotes() {
	h.listSeq++
	seq, ep, ws := h.listSeq, h.epoch, h.ws
	type answer struct {
		paths []string
		err   error
	}
	do(h, func(ctx context.Context) answer {
		paths, err := h.listAll(ctx, ws)
		return answer{paths, err}
	}, func(a answer) {
		if seq != h.listSeq || ep != h.epoch {
			return
		}
		if a.err != nil {
			h.failed("notes", a.err)
			return
		}
		h.notesAll = a.paths
	})
}

// the msgpack vocabulary, read

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asList(v any) []any         { l, _ := v.([]any); return l }
func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}
