package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/search/embed"
)

// VECTORS — Search models › Vectors…: the workspace's models, each with the room its vectors take (the
// float32 vectors, the 1-bit codes and the rows' keys), and a purge for one no longer used. And
// Search models › Cancel indexing: a model switch goes back to the model still active; embedding
// outside a switch stops, keeping what it made.

// vectorRow is one of the workspace's models as index.models says.
type vectorRow struct {
	fp, name, state          string
	dims                     int64
	vectors, f32, bits, keys int64
}

// openVectors lists the workspace's models, and opens the list.
func (h *Host) openVectors() {
	h.set("App.vectorsTitle", h.trf("autodoc.text.vectors.vectors_in", h.ws))
	h.set("App.vectorsStatus", h.tr("autodoc.text.vectors.listing"))
	h.set("App.vectorsRefusals", "")
	h.loadVectors()
	h.open("vectorsDialog")
}

func (h *Host) loadVectors() {
	tk, ws := h.ticket(nil, TicketWorkspace), h.ws
	type answer struct {
		rows     []vectorRow
		refusals []map[string]any // index.status's refused texts
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
		var refusals []map[string]any
		for _, item := range asList(emb["refused_texts"]) {
			refusals = append(refusals, asMap(item))
		}
		return answer{rows: rows, refusals: refusals}
	}, func(a answer) {
		if !tk.Current() {
			return
		}
		if a.err != nil {
			h.set("App.vectorsStatus", h.trf("autodoc.text.vectors.vectors", wireMessage(a.err)))
			return
		}
		h.vectors.Reset(a.rows)
		var total, unused int64
		for _, r := range a.rows {
			total += r.size()
			if r.state == "unused" {
				unused += r.size()
			}
		}
		status := h.trf("autodoc.text.vectors.models_in_all", len(a.rows), bytesText(total))
		if unused > 0 {
			status = h.trf("autodoc.text.vectors.models_in_all_purge_removes_one", len(a.rows), bytesText(total), bytesText(unused))
		}
		h.set("App.vectorsStatus", status)
		details := make([]string, len(a.refusals))
		for i, r := range a.refusals {
			details[i] = h.trf("autodoc.text.vectors.bytes_estimated_tokens_retry", str(r, "path"), str(r, "breadcrumb"), str(r, "error"),
				num(r, "bytes"), num(r, "estimated_tokens"), time.Unix(num(r, "retry_at"), 0).Format("15:04"))
		}
		h.set("App.vectorsRefusals", strings.Join(details, "\n"))
	})
}

// size is the room a model's vectors take.
func (r vectorRow) size() int64 { return r.f32 + r.bits + r.keys }

// vectorRowOf is a model as the list shows it.
func vectorRowOf(_ int, r vectorRow) rowOf {
	return rowOf{"key": r.fp, "state": r.state, "model": r.name, "dims": fmt.Sprint(r.dims),
		"vectors": fmt.Sprint(r.vectors), "f32": bytesText(r.f32), "bits": bytesText(r.bits),
		"keys": bytesText(r.keys), "total": bytesText(r.size())}
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
	r, ok := h.vectors.At(i)
	if !ok {
		return
	}
	if r.state != "unused" {
		what := h.tr("autodoc.text.vectors.the_active_model_choose_another_model_or_remove")
		if r.state != "active" {
			what = h.tr("autodoc.text.vectors.the_switch_s_target_cancel_the_switch")
		}
		h.set("App.vectorsStatus", h.trf("autodoc.text.vectors.is", r.name, what))
		return
	}
	ws := h.ws
	h.ask(Question{Title: h.tr("autodoc.question.vectors.purge_the_model"), Text: h.trf("autodoc.question.vectors.purge_s_vectors_from_switching_back_to_it_would", r.name, r.vectors, bytesText(r.size()), ws), OnYes: func() { h.purgeConfirmed(r, ws) }})
}

func (h *Host) purgeConfirmed(r vectorRow, ws string) {
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "index.purge_model", ws, r.fp)
		return err
	}, func(err error) {
		if err != nil {
			h.set("App.vectorsStatus", h.trf("autodoc.text.vectors.not_purged", wireMessage(err)))
			return
		}
		h.notify(h.trf("autodoc.notice.vectors.purged_s_vectors_from", r.name, r.vectors, ws))
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
		do(h, func(ctx context.Context) (out answerOf[string]) {
			res, err := h.call(ctx, "embedding.cancel_switch")
			out.v, out.err = str(asMap(res), "model"), err
			return
		}, func(a answerOf[string]) {
			if a.err != nil {
				h.failed(h.tr("autodoc.notice.vectors.cancel_the_switch"), a.err)
				return
			}
			h.notify(h.trf("autodoc.notice.vectors.switch_cancelled_semantic_search_with_again_s_ve", a.v, target))
			h.ai.providers.load()
		})
	case e.working() > 0:
		do(h, func(ctx context.Context) error {
			_, err := h.call(ctx, "embedding.use", "")
			return err
		}, func(err error) {
			if err != nil {
				h.failed(h.tr("autodoc.notice.vectors.stop_embedding"), err)
				return
			}
			h.notify(h.tr("autodoc.notice.vectors.embedding_stopped_the_vectors_made_kept_use_carr"))
			h.ai.providers.load()
		})
	default:
		h.say(h.tr("autodoc.notice.vectors.nothing_is_being_embedded"))
	}
}
