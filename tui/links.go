package tui

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// THE LINKS PANEL — the files that link to the file in the editor, in a drawer (panels.go).

func (h *Host) loadBacklinks(path string) {
	gen, ep, ws := h.file.gen, h.epoch, h.ws
	type answer struct {
		paths []string
		err   error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "graph.backlinks", ws, path)
		if code(err) == rpc.CodeNotFound {
			return answer{} // not indexed yet (just created or written): no backlink is known
		}
		if err != nil {
			return answer{err: err}
		}
		var out []string
		seen := map[string]bool{}
		for _, l := range asList(res) {
			if p := str(asMap(l), "path"); !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
		return answer{paths: out}
	}, func(a answer) {
		if gen != h.file.gen || ep != h.epoch {
			return
		}
		if a.err != nil {
			h.failed("backlinks", a.err)
			return
		}
		rows := make([]rowOf, len(a.paths))
		for i, p := range a.paths {
			rows[i] = rowOf{"key": p, "label": p}
		}
		h.backlinks.Reset(rows)
		h.set("App.linksTitle", fmt.Sprintf("backlinks (%d)", len(a.paths)))
	})
}

func (h *Host) openBacklink(i int) {
	if i >= 0 && i < h.backlinks.Len() {
		h.keep(h.p.Call("links", "close"))
		h.openPath(h.backlinks.At(i)["label"].(string))
	}
}
