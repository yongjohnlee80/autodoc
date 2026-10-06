package index

import (
	"context"
	"slices"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/graph"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// The supersession kinds: a document's superseded_by names its successor, and a successor's
// supersedes names what it replaced.
const (
	kindSupersedes   = "supersedes"
	kindSupersededBy = "superseded_by"
)

// successorsOf reads the supersession among the documents at paths: for each superseded one, the
// documents that replace it, from either side's relation. Only resolved relations count.
func (s *Store) successorsOf(ctx context.Context, paths []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(paths) == 0 {
		return out, nil
	}
	in := make([]any, len(paths))
	for i, p := range paths {
		in[i] = p
	}
	err := s.read(ctx, func(tx *store.Tx) error {
		docs, err := s.sc.Documents(tx).With(store.DocPath, in...).Select(store.DocID, store.DocPath)
		if err != nil {
			return err
		}
		path := map[int64]string{}
		ids := make([]any, 0, len(docs))
		for _, d := range docs {
			path[d.ID] = d.Path
			ids = append(ids, d.ID)
		}
		if len(ids) == 0 {
			return nil
		}
		kinds := kindsIn(s.sc.LinksOut(tx), []string{kindSupersedes, kindSupersededBy})
		rows, err := kinds.WithPredicate(dao.IsNotNull(`"link"."dst_doc"`)).With(store.LinkSrc, ids...).
			Select(store.LinkSrc, store.LinkDst, store.LinkKind)
		if err != nil {
			return err
		}
		add := func(old, successor string) {
			if old != "" && successor != "" && old != successor && !slices.Contains(out[old], successor) {
				out[old] = append(out[old], successor)
			}
		}
		for _, l := range rows {
			src, dst := path[l.SrcDoc], path[*l.DstDoc]
			if l.Kind == kindSupersededBy {
				add(src, dst) // src says dst replaces it
			} else {
				add(dst, src) // src says it replaces dst
			}
		}
		return nil
	})
	return out, err
}

// demote moves each superseded document's hits that rank above its successor's last hit to just
// below it, and marks every superseded hit with a successor. A hit only ever moves down, and the
// others keep their order. Documents are moved successors first, so in a chain (A replaced by B,
// B by C) each lands below the place its successor ends at; documents whose supersession goes round
// a loop are not moved, since a loop has no order.
func demote(hits []Hit, successors map[string][]string) []Hit {
	present := map[string]bool{}
	for _, h := range hits {
		present[h.Path] = true
	}
	succ := func(p string) []string {
		var out []string
		for _, s := range successors[p] {
			if present[s] {
				out = append(out, s)
			}
		}
		return out
	}
	// the supersession among the listed documents, and which of them sit on a loop
	g := graph.New[string]()
	for p := range present {
		for _, s := range succ(p) {
			g.Add(graph.Edge[string]{Src: p, Dst: s, Kind: kindSupersededBy})
		}
	}
	onLoop := func(p string) bool {
		for n := range g.Neighborhood(p, len(present), graph.Out, graph.Filter{}).Nodes() {
			if n == p {
				continue
			}
			for e := range g.Out(n, graph.Filter{}) {
				if e.Dst == p {
					return true
				}
			}
		}
		return false
	}
	out := slices.Clone(hits)
	for i := range out {
		if ss := successors[out[i].Path]; len(ss) > 0 {
			if s := succ(out[i].Path); len(s) > 0 {
				out[i].SupersededBy = s[0]
			} else {
				out[i].SupersededBy = ss[0]
			}
		}
	}
	// the documents to move, by first hit, then successors first (reverse topological)
	var movers []string
	seen := map[string]bool{}
	for _, h := range hits {
		if !seen[h.Path] && len(succ(h.Path)) > 0 && !onLoop(h.Path) {
			seen[h.Path] = true
			movers = append(movers, h.Path)
		}
	}
	placed := map[string]bool{}
	var order []string
	for len(order) < len(movers) {
		progressed := false
		for _, p := range movers {
			if placed[p] {
				continue
			}
			ready := true
			for _, s := range succ(p) {
				if slices.Contains(movers, s) && !placed[s] {
					ready = false
				}
			}
			if ready {
				placed[p] = true
				order = append(order, p)
				progressed = true
				break
			}
		}
		if !progressed {
			break // only a loop leaves nothing ready, and loops were left out: never wait on one
		}
	}
	for _, p := range order {
		boundary := -1 // the lowest-placed last hit among p's successors
		for i, h := range out {
			if slices.Contains(succ(p), h.Path) {
				boundary = i
			}
		}
		var moved, rest []Hit
		for i, h := range out {
			if h.Path == p && i < boundary {
				moved = append(moved, h)
			} else {
				rest = append(rest, h)
			}
		}
		if len(moved) == 0 {
			continue
		}
		at := -1
		for i, h := range rest {
			if slices.Contains(succ(p), h.Path) {
				at = i
			}
		}
		out = slices.Concat(rest[:at+1], moved, rest[at+1:])
	}
	return out
}
