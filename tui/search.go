package tui

import (
	"context"
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE WORKSPACE'S NOTES — the list the pickers filter, read when a workspace is entered and after
// a file is made.

type rowOf = tuidecl.Row

// listFiles reads every file of the workspace in use, in path order.
func (h *Host) listFiles() {
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
			// a daemon still warming up can fail a listing; an empty list would stay empty until a
			// file changed, so try again, a little later each time, while nothing newer has asked
			if h.listRetry == 0 {
				h.failed("files", a.err)
			}
			h.listRetry = min(max(2*h.listRetry, listRetryFirst), listRetryMax)
			h.after(h.listRetry, func() {
				if seq == h.listSeq && ep == h.epoch {
					h.listFiles()
				}
			})
			return
		}
		h.listRetry = 0
		h.filesAll = a.paths
	})
}

// The files' listing retries after a failure: first after listRetryFirst, doubling to listRetryMax.
const (
	listRetryFirst = 500 * time.Millisecond
	listRetryMax   = 10 * time.Second
)

// the msgpack vocabulary, read

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asList(v any) []any         { l, _ := v.([]any); return l }
func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}
