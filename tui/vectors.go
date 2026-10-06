package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/search/embed"
)

// VECTORS — AI models › Vectors…: the workspace's models, each with the room its vectors take (the
// float32 vectors, the 1-bit codes and the rows' keys), and a purge for one no longer used. And
// AI models › Cancel indexing: a model switch goes back to the model still active; embedding
// outside a switch stops, keeping what it made.

// vectorRow is one of the workspace's models as index.models says.
type vectorRow struct {
	fp, name, state          string
	dims                     int64
	vectors, f32, bits, keys int64
}

// openVectors lists the workspace's models, and opens the list.
func (h *Host) openVectors() {
	h.set("App.vectorsTitle", "vectors in "+h.ws)
	h.set("App.vectorsStatus", "listing…")
	h.set("App.vectorsRefusals", "")
	h.loadVectors()
	h.open("vectorsDialog")
}

func (h *Host) loadVectors() {
	ep, ws := h.epoch, h.ws
	type answer struct {
		rows     []vectorRow
		refusals string
		err      error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "index.models", ws)
		if err != nil {
			return answer{err: err}
		}
		var rows []vectorRow
		for _, x := range asList(res) {
			m := asMap(x)
			rows = append(rows, vectorRow{fp: str(m, "fp"), name: str(m, "name"), state: str(m, "state"), dims: num(m, "dims"),
				vectors: num(m, "vectors"), f32: num(m, "f32_bytes"), bits: num(m, "bits_bytes"), keys: num(m, "key_bytes")})
		}
		st, err := h.call(ctx, "index.status", ws)
		if err != nil {
			return answer{err: err}
		}
		emb := asMap(asMap(st)["embeddings"])
		var details []string
		for _, item := range asList(emb["refused_texts"]) {
			r := asMap(item)
			details = append(details, fmt.Sprintf("%s › %s · %s (%d bytes, %d estimated tokens; retry %s)",
				str(r, "path"), str(r, "breadcrumb"), str(r, "error"), num(r, "bytes"), num(r, "estimated_tokens"),
				time.Unix(num(r, "retry_at"), 0).Format("15:04")))
		}
		return answer{rows: rows, refusals: strings.Join(details, "\n")}
	}, func(a answer) {
		if ep != h.epoch {
			return
		}
		if a.err != nil {
			h.set("App.vectorsStatus", "vectors: "+wireMessage(a.err))
			return
		}
		h.vectorList = a.rows
		rows := make([]rowOf, len(a.rows))
		var total, unused int64
		for i, r := range a.rows {
			all := r.f32 + r.bits + r.keys
			total += all
			if r.state == "unused" {
				unused += all
			}
			rows[i] = rowOf{"key": r.fp, "state": r.state, "model": r.name, "dims": fmt.Sprint(r.dims),
				"vectors": fmt.Sprint(r.vectors), "f32": bytesText(r.f32), "bits": bytesText(r.bits),
				"keys": bytesText(r.keys), "total": bytesText(all)}
		}
		h.vectors.Reset(rows)
		status := fmt.Sprintf("%d models, %s in all", len(a.rows), bytesText(total))
		if unused > 0 {
			status += fmt.Sprintf(" · %s in models no longer used: Purge… removes one", bytesText(unused))
		}
		h.set("App.vectorsStatus", status)
		h.set("App.vectorsRefusals", a.refusals)
	})
}

// bytesText is a size as the list shows it.
func bytesText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// startPurge asks before model i's vectors go; only a model no longer used can be purged.
func (h *Host) startPurge(i int) {
	if i < 0 || i >= len(h.vectorList) {
		return
	}
	r := h.vectorList[i]
	if r.state != "unused" {
		what := "the active model: choose another model, or remove its provider in AI models"
		if r.state != "active" {
			what = "the switch's target: cancel the switch"
		}
		h.set("App.vectorsStatus", fmt.Sprintf("%s is %s", r.name, what))
		return
	}
	h.purging = r
	h.set("App.purgeQuestion", fmt.Sprintf("Purge %s's %d vectors (%s) from %s? Switching back to it would embed "+
		"every section again; the store gives its room back.", r.name, r.vectors,
		bytesText(r.f32+r.bits+r.keys), h.ws))
	h.open("purgeModel")
}

func (h *Host) purgeConfirmed() {
	r, ws := h.purging, h.ws
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "index.purge_model", ws, r.fp)
		return err
	}, func(err error) {
		if err != nil {
			h.set("App.vectorsStatus", "not purged: "+wireMessage(err))
			return
		}
		h.notify(fmt.Sprintf("purged %s's %d vectors from %s", r.name, r.vectors, ws))
		h.loadVectors()
	})
}

// cancelIndexing ends the embedding under way: a model switch goes back to the model still active,
// which covers every section, and the switch's partial vectors are deleted; embedding outside a
// switch stops (semantic search off), keeping the vectors made, so Use carries on from them.
func (h *Host) cancelIndexing() {
	e := h.prog.emb
	switch {
	case e.target != "":
		target := embed.ModelName(e.target)
		do(h, func(ctx context.Context) (out struct {
			model string
			err   error
		}) {
			res, err := h.call(ctx, "embedding.cancel_switch")
			out.model, out.err = str(asMap(res), "model"), err
			return
		}, func(a struct {
			model string
			err   error
		}) {
			if a.err != nil {
				h.failed("cancel the switch", a.err)
				return
			}
			h.notify(fmt.Sprintf("switch cancelled: semantic search with %s again; %s's vectors so far are deleted", a.model, target))
			h.loadProviders()
		})
	case e.working() > 0:
		do(h, func(ctx context.Context) error {
			_, err := h.call(ctx, "embedding.use", "")
			return err
		}, func(err error) {
			if err != nil {
				h.failed("stop embedding", err)
				return
			}
			h.notify("embedding stopped, the vectors made kept: Use carries on from them")
			h.loadProviders()
		})
	default:
		h.say("nothing is being embedded")
	}
}
