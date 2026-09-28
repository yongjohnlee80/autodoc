package index

import (
	"context"
	"errors"
	"sort"

	"github.com/yongjohnlee80/golib/dao"
)

// ErrNoDocument is a path the index does not hold.
var ErrNoDocument = errors.New("index: no such document")

// Link is one link of graph.links or graph.backlinks (ADR 0204 §4.6). Path is the other end: the
// target of a link, the source of a backlink; "" for an unresolved link.
type Link struct {
	Path     string
	Raw      string // the link as written
	Anchor   string // the heading, or "^" and the block id; "" for none
	Kind     string // LinkWikilink, LinkEmbed or LinkMarkdown
	Resolved bool
}

// Edge is a resolved link between two documents of a neighbourhood.
type Edge struct {
	Src, Dst, Kind string
}

// Neighborhood is graph.neighborhood: the documents within depth links of one, either way, and the
// resolved links among them.
type Neighborhood struct {
	Nodes []string
	Edges []Edge
}

// Unresolved is one link of graph.unresolved: its source, as written, and why it reaches nothing
// (ReasonMissing or ReasonAmbiguous).
type Unresolved struct {
	Src, Raw, Reason string
}

// Links lists the links of the document at path, in source order.
func (s *Store) Links(ctx context.Context, path string) ([]Link, error) {
	return s.links(ctx, path, `SELECT COALESCE(d.path, ''), l.raw, COALESCE(l.anchor, ''), l.kind, l.dst_doc IS NOT NULL
		FROM link l LEFT JOIN document d ON d.id = l.dst_doc WHERE l.src_doc = ? ORDER BY l.rowid`)
}

// Backlinks lists the links that reach the document at path, by source path, then source order.
func (s *Store) Backlinks(ctx context.Context, path string) ([]Link, error) {
	return s.links(ctx, path, `SELECT d.path, l.raw, COALESCE(l.anchor, ''), l.kind, 1
		FROM link l JOIN document d ON d.id = l.src_doc WHERE l.dst_doc = ? ORDER BY d.path, l.rowid`)
}

func (s *Store) links(ctx context.Context, path, q string) ([]Link, error) {
	tx, err := s.r.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	id, err := docID(ctx, tx, path)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, q, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.Path, &l.Raw, &l.Anchor, &l.Kind, &l.Resolved); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Neighborhood gathers the documents within depth (1 or 2; others are clamped) resolved links of
// the one at path, following links both ways, and the links among them.
func (s *Store) Neighborhood(ctx context.Context, path string, depth int) (Neighborhood, error) {
	depth = min(max(depth, 1), 2)
	tx, err := s.r.Begin(ctx)
	if err != nil {
		return Neighborhood{}, err
	}
	defer tx.Rollback()
	start, err := docID(ctx, tx, path)
	if err != nil {
		return Neighborhood{}, err
	}
	seen := map[int64]bool{start: true}
	frontier := []int64{start}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []int64
		for _, id := range frontier {
			rows, err := tx.QueryContext(ctx, `SELECT dst_doc FROM link WHERE src_doc = ? AND dst_doc IS NOT NULL
				UNION SELECT src_doc FROM link WHERE dst_doc = ?`, id, id)
			if err != nil {
				return Neighborhood{}, err
			}
			for rows.Next() {
				var n int64
				if err := rows.Scan(&n); err != nil {
					rows.Close()
					return Neighborhood{}, err
				}
				if !seen[n] {
					seen[n] = true
					next = append(next, n)
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return Neighborhood{}, err
			}
		}
		frontier = next
	}
	paths := map[int64]string{}
	var nb Neighborhood
	for id := range seen {
		var p string
		if err := scanOne(ctx, tx, &p, "SELECT path FROM document WHERE id = ?", id); err != nil {
			return Neighborhood{}, err
		}
		paths[id] = p
		nb.Nodes = append(nb.Nodes, p)
	}
	sort.Strings(nb.Nodes)
	edges := map[Edge]bool{}
	for id := range seen {
		rows, err := tx.QueryContext(ctx, "SELECT dst_doc, kind FROM link WHERE src_doc = ? AND dst_doc IS NOT NULL", id)
		if err != nil {
			return Neighborhood{}, err
		}
		for rows.Next() {
			var dst int64
			var kind string
			if err := rows.Scan(&dst, &kind); err != nil {
				rows.Close()
				return Neighborhood{}, err
			}
			if seen[dst] {
				edges[Edge{paths[id], paths[dst], kind}] = true
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Neighborhood{}, err
		}
	}
	for e := range edges {
		nb.Edges = append(nb.Edges, e)
	}
	sort.Slice(nb.Edges, func(i, j int) bool {
		a, b := nb.Edges[i], nb.Edges[j]
		if a.Src != b.Src {
			return a.Src < b.Src
		}
		if a.Dst != b.Dst {
			return a.Dst < b.Dst
		}
		return a.Kind < b.Kind
	})
	return nb, nil
}

// Unresolved lists every link that reaches no document, by source path, then source order.
func (s *Store) Unresolved(ctx context.Context) ([]Unresolved, error) {
	tx, err := s.r.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT d.path, l.raw, l.kind, l.name FROM link l JOIN document d ON d.id = l.src_doc
		WHERE l.dst_doc IS NULL ORDER BY d.path, l.rowid`)
	if err != nil {
		return nil, err
	}
	type row struct{ src, raw, kind, name string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.src, &r.raw, &r.kind, &r.name); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []Unresolved{}
	for _, r := range all {
		// the reason is not stored: resolving again, in this snapshot, gives the writer's answer
		_, reason, err := resolve(ctx, tx, r.kind, r.name)
		if err != nil {
			return nil, err
		}
		out = append(out, Unresolved{Src: r.src, Raw: r.raw, Reason: reason})
	}
	return out, nil
}

func docID(ctx context.Context, q dao.Querier, path string) (int64, error) {
	var id int64
	err := scanOne(ctx, q, &id, "SELECT id FROM document WHERE path = ?", path)
	if errors.Is(err, errNoRow) {
		return 0, ErrNoDocument
	}
	return id, err
}
