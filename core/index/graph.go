package index

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/graph"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// ErrNoDocument is a path the index does not hold.
var ErrNoDocument = errs.Sentinel(errs.ErrInvalidArgument, "index: no such document")

// Link is one link of graph.links or graph.backlinks (ADR 0204 §4.6). Path is the other end: the
// target of a link, the source of a backlink; "" for an unresolved link.
type Link struct {
	Path     string
	Raw      string // the link as written
	Anchor   string // the heading, or "^" and the block id; "" for none
	Kind     string // LinkWikilink, LinkEmbed or LinkMarkdown, or a relation's field
	Resolved bool
	// External is a link's file outside the workspace, absolute: a todo task's ref (ADR 1792015212
	// §2.4). It reaches no document, and is not unresolved. "" for every other link.
	External string
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

// kindsIn narrows a link query to links of the given kinds; nil leaves it every kind. The filter is
// in the query, so a walk never passes through a link of a kind it was not given.
func kindsIn(q dao.DAO[*LinkRow, LinkField, int64], kinds []string) dao.DAO[*LinkRow, LinkField, int64] {
	if kinds == nil {
		return q
	}
	vs := make([]any, len(kinds))
	for i, k := range kinds {
		vs[i] = k
	}
	return q.WithPredicate(dao.In(`"link"."kind"`, vs))
}

// Links lists the links of the document at path, in source order: those of the given kinds, every
// kind when kinds is nil.
func (s *Store) Links(ctx context.Context, path string, kinds []string) ([]Link, error) {
	out := []Link{}
	err := s.read(ctx, func(tx *store.Tx) error {
		id, err := s.docID(tx, path)
		if err != nil {
			return err
		}
		rows, err := kindsIn(s.linksOut(tx), kinds).With(LinkSrc, id).OrderBy(dao.Asc(LinkByID)).
			Select(LinkOtherPath, LinkRaw, LinkAnchor, LinkKind, LinkDst, LinkTarget)
		for _, r := range rows {
			out = append(out, Link{Path: r.OtherPath, Raw: r.Raw, Anchor: deref(r.Anchor), Kind: r.Kind, Resolved: r.DstDoc != nil,
				External: deref(r.Target)})
		}
		return err
	})
	return out, err
}

// Backlinks lists the links of the given kinds (nil: every kind) that reach the document at path,
// by source path, then source order.
func (s *Store) Backlinks(ctx context.Context, path string, kinds []string) ([]Link, error) {
	out := []Link{}
	err := s.read(ctx, func(tx *store.Tx) error {
		id, err := s.docID(tx, path)
		if err != nil {
			return err
		}
		rows, err := kindsIn(s.linksIn(tx), kinds).With(LinkDst, id).OrderBy(dao.Asc(LinkByOtherPath)).
			Select(LinkOtherPath, LinkRaw, LinkAnchor, LinkKind)
		for _, r := range rows {
			out = append(out, Link{Path: r.OtherPath, Raw: r.Raw, Anchor: deref(r.Anchor), Kind: r.Kind, Resolved: true})
		}
		return err
	})
	return out, err
}

// MapNode is a document of graph.map: its path, its title, its frontmatter "type" ("" when none),
// whether another document supersedes it, and its tags (sorted; nil for none).
type MapNode struct {
	Path, Title, Type string
	Superseded        bool
	Tags              []string
}

// Map is graph.map: documents and the resolved links among them, both sorted.
type Map struct {
	Nodes []MapNode
	Edges []Edge
}

// MapOpts selects graph.map's documents: every document of the workspace, or with Root those within
// Depth (1 or 2; others are clamped) resolved links of it. Kinds are the links followed and returned
// (nil: every kind), as Neighborhood takes them.
type MapOpts struct {
	Root  string
	Depth int
	Kinds []string
	// TypeField is the frontmatter field a node's Type is read from: the schema's discriminator;
	// "" is "type".
	TypeField string
}

// Map gathers graph.map. The workspace's resolved links of the given kinds are read as a
// graph.Graph. With a root, the subgraph within depth of it, both ways, as Neighborhood walks it;
// without, every document, linked or not, and every one of those links. Each node's Superseded
// comes from every resolved supersedes and superseded_by link, whatever kinds were asked for, so a
// filter never unmarks a document; a document naming itself is not superseded by it.
func (s *Store) Map(ctx context.Context, o MapOpts) (Map, error) {
	return s.graphMap(ctx, o, o.Root != "")
}

// graphMap is Map, rooted at o.Root when rooted: an empty or unknown root is then ErrNoDocument,
// as Neighborhood has always answered; unrooted, o.Root is unused.
//
// Rooted, it reads only the neighbourhood (AP-6): the links of the frontier, both ways, one step
// at a time, then the links among the documents reached, and those documents' rows, tags and
// supersession; never the workspace's every link, tag and supersession, which a rooted map of a
// few documents used to read whole. Its answer is golib's graph.Neighborhood's over the whole
// graph: every document within depth links of the root either way, and every link of the given
// kinds whose two ends were both reached.
func (s *Store) graphMap(ctx context.Context, o MapOpts, rooted bool) (Map, error) {
	var m Map
	err := s.read(ctx, func(tx *store.Tx) error {
		var ids []int64 // the documents of the map; nil: every one
		var links []*LinkRow
		if rooted {
			start, err := s.docID(tx, o.Root)
			if err != nil {
				return err
			}
			if ids, links, err = s.neighbourhood(tx, start, min(max(o.Depth, 1), 2), o.Kinds); err != nil {
				return err
			}
		} else {
			var err error
			if links, err = kindsIn(s.linksOut(tx), o.Kinds).WithPredicate(dao.IsNotNull(`"link"."dst_doc"`)).
				Select(LinkSrc, LinkDst, LinkKind); err != nil {
				return err
			}
		}
		found, err := s.docsByID(tx, ids)
		if err != nil {
			return err
		}
		superseded, err := s.superseded(tx, ids)
		if err != nil {
			return err
		}
		tags, err := s.tagsByDoc(tx, ids)
		if err != nil {
			return err
		}
		paths := map[int64]string{}
		for _, d := range found {
			paths[d.ID] = d.Path
			n := MapNode{Path: d.Path, Type: frontmatterType(d.FrontmatterJSON, cmp.Or(o.TypeField, "type")), Superseded: superseded[d.ID], Tags: tags[d.ID]}
			if d.Title != nil {
				n.Title = *d.Title
			}
			m.Nodes = append(m.Nodes, n)
		}
		g := graph.New[int64]() // a link repeated (one document linking another twice) is one edge
		for _, l := range links {
			g.Add(graph.Edge[int64]{Src: l.SrcDoc, Dst: *l.DstDoc, Kind: l.Kind})
		}
		for e := range g.Edges(graph.Filter{}) {
			m.Edges = append(m.Edges, Edge{paths[e.Src], paths[e.Dst], e.Kind})
		}
		return nil
	})
	if err != nil {
		return Map{}, err
	}
	sort.Slice(m.Nodes, func(i, j int) bool { return m.Nodes[i].Path < m.Nodes[j].Path })
	sortEdges(m.Edges)
	return m, nil
}

// AN UNROOTED MAP, A PAGE AT A TIME (ADR 1793321612) — the whole workspace's map in one reply fails
// past the transport's message limit, so a client from protocol 36 reads it in pages: documents in
// path order after a cursor, through document's (workspace_id, path) key, each page with the links
// whose source is on it (link_src, the destination's path joined), and those documents' tags and
// supersession. Every link is sent once, by the page of its source; one to a document of a later
// page names a node the client has not had yet, and it holds the link until the map ends. No
// snapshot crosses pages: a document added or removed meanwhile may be missing or extra.

// The pages of an unrooted map: DefaultMapPage documents unless asked, MaxMapPage at most.
const (
	DefaultMapPage = 500
	MaxMapPage     = 2000
)

// ErrBadMapCursor is a map page's after that is not a cursor of this map: not one at all, or one
// asked under other kinds.
var ErrBadMapCursor = errs.Sentinel(errs.ErrInvalidArgument, "index: after is not a cursor of this map")

// MapPageOpts asks for one page of an unrooted map.
type MapPageOpts struct {
	Kinds     []string // the links returned (nil: every kind), as MapOpts
	TypeField string   // as MapOpts
	After     string   // the previous page's next; "" from the start
	Limit     int      // documents a page: DefaultMapPage when 0, MaxMapPage at most
}

// MapPage is one page of an unrooted map: its documents, the links whose source is among them,
// and the cursor of the next page ("" at the end).
type MapPage struct {
	Map
	Next string
}

// mapCursorPrefix marks a map cursor's encoding.
const mapCursorPrefix = "m1."

// mapCursor is a map page's next, decoded: the last document's path and the hash of the kinds.
type mapCursor struct {
	Path  string `json:"p"`
	Kinds string `json:"k"`
}

// kindsHash fingerprints a map's kinds for its cursor: nil (every kind) and a list differ, and a
// list is a set (its order and repeats do not matter).
func kindsHash(kinds []string) string {
	if kinds == nil {
		return "*"
	}
	b, _ := json.Marshal(sortedSet(kinds))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// MapPage gathers one page of the unrooted map: the next documents in path order after o.After,
// and the resolved links of o.Kinds whose source is one of them, to any document. Superseded and
// tags are each node's own, as Map answers them.
func (s *Store) MapPage(ctx context.Context, o MapPageOpts) (MapPage, error) {
	if o.Limit <= 0 {
		o.Limit = DefaultMapPage
	}
	o.Limit = min(o.Limit, MaxMapPage)
	kh := kindsHash(o.Kinds)
	after := ""
	if o.After != "" {
		raw, ok := strings.CutPrefix(o.After, mapCursorPrefix)
		b, err := base64.RawURLEncoding.DecodeString(raw)
		var c mapCursor
		if ok && err == nil {
			err = json.Unmarshal(b, &c)
		}
		switch {
		case !ok || err != nil:
			return MapPage{}, fmt.Errorf("%w: not a next graph.map answered", ErrBadMapCursor)
		case c.Kinds != kh:
			return MapPage{}, fmt.Errorf("%w: it is from other kinds", ErrBadMapCursor)
		}
		after = c.Path
	}
	var p MapPage
	err := s.read(ctx, func(tx *store.Tx) error {
		d := s.documents(tx)
		if after != "" {
			d = d.WithPredicate(dao.Raw(`"document"."path" > ?`, after))
		}
		docs, err := d.OrderBy(dao.Asc(byPath)).Limit(uint64(o.Limit+1)).Select(DocID, DocPath, DocTitle, DocFrontmatterJSON)
		if err != nil {
			return err
		}
		if len(docs) > o.Limit {
			docs = docs[:o.Limit]
			b, _ := json.Marshal(mapCursor{Path: docs[len(docs)-1].Path, Kinds: kh})
			p.Next = mapCursorPrefix + base64.RawURLEncoding.EncodeToString(b)
		}
		if len(docs) == 0 {
			return nil
		}
		ids := make([]int64, len(docs))
		paths := make(map[int64]string, len(docs))
		for i, doc := range docs {
			ids[i], paths[doc.ID] = doc.ID, doc.Path
		}
		superseded, err := s.superseded(tx, ids)
		if err != nil {
			return err
		}
		tags, err := s.tagsByDoc(tx, ids)
		if err != nil {
			return err
		}
		for _, doc := range docs {
			n := MapNode{Path: doc.Path, Type: frontmatterType(doc.FrontmatterJSON, cmp.Or(o.TypeField, "type")),
				Superseded: superseded[doc.ID], Tags: tags[doc.ID]}
			if doc.Title != nil {
				n.Title = *doc.Title
			}
			p.Nodes = append(p.Nodes, n)
		}
		// the page's links, by link_src; no "dst_doc IS NOT NULL" (see neighbourhood): an
		// unresolved link is skipped here, and a link repeated is one edge, as Map has it
		seen := map[Edge]bool{}
		for part := range inParts(ids) {
			rows, err := kindsIn(s.linksOut(tx), o.Kinds).With(LinkSrc, part...).Select(LinkSrc, LinkDst, LinkKind, LinkOtherPath)
			if err != nil {
				return err
			}
			for _, l := range rows {
				if l.DstDoc == nil {
					continue
				}
				if e := (Edge{paths[l.SrcDoc], l.OtherPath, l.Kind}); !seen[e] {
					seen[e] = true
					p.Edges = append(p.Edges, e)
				}
			}
		}
		return nil
	})
	if err != nil {
		return MapPage{}, err
	}
	sortEdges(p.Edges)
	return p, nil
}

// sortEdges orders a map's links by source, destination, then kind.
func sortEdges(es []Edge) {
	sort.Slice(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.Src != b.Src {
			return a.Src < b.Src
		}
		if a.Dst != b.Dst {
			return a.Dst < b.Dst
		}
		return a.Kind < b.Kind
	})
}

// neighbourhood walks the resolved links of the given kinds from start, both ways, depth steps: the
// documents reached (start first), and the links among them. Each step reads only its frontier's
// links, through link_src and link_dst, in parts small enough for one IN list.
func (s *Store) neighbourhood(tx *store.Tx, start int64, depth int, kinds []string) ([]int64, []*LinkRow, error) {
	// NOTE: no "dst_doc IS NOT NULL" here. Without statistics, SQLite takes that predicate for a
	// range on link_dst and walks the workspace's resolved links instead of seeking link_src; an
	// unresolved link (a nil destination) is skipped in Go instead, and a dst_doc IN list never
	// matches one.
	links := func() dao.DAO[*LinkRow, LinkField, int64] { return kindsIn(s.linksOut(tx), kinds) }
	reached := map[int64]bool{start: true}
	ids := []int64{start}
	frontier := []int64{start}
	for step := 0; step < depth && len(frontier) > 0; step++ {
		var next []int64
		visit := func(id int64) {
			if !reached[id] {
				reached[id] = true
				next = append(next, id)
			}
		}
		for part := range inParts(frontier) {
			out, err := links().With(LinkSrc, part...).Select(LinkDst)
			if err != nil {
				return nil, nil, err
			}
			for _, l := range out {
				if l.DstDoc != nil {
					visit(*l.DstDoc)
				}
			}
			in, err := links().With(LinkDst, part...).Select(LinkSrc)
			if err != nil {
				return nil, nil, err
			}
			for _, l := range in {
				visit(l.SrcDoc)
			}
		}
		ids = append(ids, next...)
		frontier = next
	}
	var among []*LinkRow
	for part := range inParts(ids) {
		rows, err := links().With(LinkSrc, part...).Select(LinkSrc, LinkDst, LinkKind)
		if err != nil {
			return nil, nil, err
		}
		for _, l := range rows {
			if l.DstDoc != nil && reached[*l.DstDoc] {
				among = append(among, l)
			}
		}
	}
	return ids, among, nil
}

// docsByID is the rows graph.map shows of the documents ids, in parts; every document when ids is
// nil.
func (s *Store) docsByID(tx *store.Tx, ids []int64) ([]*Document, error) {
	if ids == nil {
		return s.documents(tx).Select(DocID, DocPath, DocTitle, DocFrontmatterJSON)
	}
	var out []*Document
	for part := range inParts(ids) {
		rows, err := s.documents(tx).With(DocID, part...).Select(DocID, DocPath, DocTitle, DocFrontmatterJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// tagsByDoc is the tags of the documents ids, sorted, by document; every document's when ids is
// nil.
func (s *Store) tagsByDoc(tx *store.Tx, ids []int64) (map[int64][]string, error) {
	var rows []*DocValue
	if ids == nil {
		var err error
		if rows, err = s.tags(tx).Select(DocValueDoc, DocValueValue); err != nil {
			return nil, err
		}
	}
	for part := range inParts(ids) {
		r, err := s.tags(tx).With(DocValueDoc, part...).Select(DocValueDoc, DocValueValue)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r...)
	}
	out := map[int64][]string{}
	for _, r := range rows {
		out[r.DocID] = append(out[r.DocID], r.Value)
	}
	for _, t := range out {
		sort.Strings(t)
	}
	return out, nil
}

// superseded is the documents among ids (every document when ids is nil) that another one
// supersedes, by the resolved supersession links: a supersedes link's target, and a superseded_by
// link's source.
func (s *Store) superseded(tx *store.Tx, ids []int64) (map[int64]bool, error) {
	// no "dst_doc IS NOT NULL" (see neighbourhood): an unresolved link is skipped below
	supersession := func() dao.DAO[*LinkRow, LinkField, int64] {
		return kindsIn(s.linksOut(tx), []string{"supersedes", "superseded_by"})
	}
	var rows []*LinkRow
	if ids == nil {
		var err error
		if rows, err = supersession().Select(LinkSrc, LinkDst, LinkKind); err != nil {
			return nil, err
		}
	}
	for part := range inParts(ids) {
		// a document is superseded by a supersedes link reaching it or a superseded_by link leaving it
		to, err := supersession().With(LinkDst, part...).Select(LinkSrc, LinkDst, LinkKind)
		if err != nil {
			return nil, err
		}
		from, err := supersession().With(LinkSrc, part...).Select(LinkSrc, LinkDst, LinkKind)
		if err != nil {
			return nil, err
		}
		rows = append(append(rows, to...), from...)
	}
	out := map[int64]bool{}
	for _, l := range rows {
		if l.DstDoc == nil || l.SrcDoc == *l.DstDoc {
			continue // unresolved, or a document naming itself, which supersedes nothing
		}
		if l.Kind == "supersedes" {
			out[*l.DstDoc] = true
		} else {
			out[l.SrcDoc] = true
		}
	}
	return out, nil
}

// frontmatterType is a document's frontmatter "type", "" when it has none or it is not a string.
func frontmatterType(raw *string, field string) string {
	if raw == nil || *raw == "" {
		return ""
	}
	var front map[string]any
	if json.Unmarshal([]byte(*raw), &front) != nil {
		return ""
	}
	t, _ := front[field].(string)
	return t
}

// Neighborhood gathers the documents within depth (1 or 2; others are clamped) resolved links of
// the one at path, following links of the given kinds (nil: every kind) both ways, and those links
// among them: graph.map's rooted walk, as paths. A path that names no document, the empty one
// included, is ErrNoDocument: unlike Map, Neighborhood always has a root.
func (s *Store) Neighborhood(ctx context.Context, path string, depth int, kinds []string) (Neighborhood, error) {
	m, err := s.graphMap(ctx, MapOpts{Root: path, Depth: depth, Kinds: kinds}, true)
	if err != nil {
		return Neighborhood{}, err
	}
	nb := Neighborhood{Edges: m.Edges}
	for _, n := range m.Nodes {
		nb.Nodes = append(nb.Nodes, n.Path)
	}
	return nb, nil
}

// Unresolved lists every link of the given kinds (nil: every kind) that reaches no document, by
// source path, then source order; then, when the kinds take in supersession, the supersession
// relations that go round a loop (ReasonCycle), which resolve but have no order.
func (s *Store) Unresolved(ctx context.Context, kinds []string) ([]Unresolved, error) {
	out := []Unresolved{}
	err := s.read(ctx, func(tx *store.Tx) error {
		// an external link reaches a file outside the workspace: it is where it should be
		rows, err := kindsIn(s.linksIn(tx), kinds).WithPredicate(dao.IsNull(`"link"."dst_doc"`)).
			WithPredicate(dao.IsNull(`"link"."target"`)).OrderBy(dao.Asc(LinkByOtherPath)).
			Select(LinkID, LinkOtherPath, LinkRaw, LinkKind, LinkName)
		if err != nil {
			return err
		}
		for _, r := range rows {
			// the reason is not stored: resolving again, in this snapshot, gives the writer's answer
			_, reason, err := s.resolveStored(tx, r.ID, r.Kind, r.Name)
			if err != nil {
				return err
			}
			out = append(out, Unresolved{Src: r.OtherPath, Raw: r.Raw, Reason: reason})
		}
		if kinds != nil && !slices.Contains(kinds, kindSupersedes) && !slices.Contains(kinds, kindSupersededBy) {
			return nil
		}
		loops, err := s.supersessionLoops(tx, kinds)
		out = append(out, loops...)
		return err
	})
	return out, err
}

// supersessionLoops lists the supersession relations that go round a loop (A superseded by B, B by
// A): each resolves, so none is unresolved, yet a loop has no order, and the demotion leaves its
// documents where they are. A relation is on a loop when its successor leads back to the document
// it replaces. Listed by source path, then source order, with ReasonCycle: of the given kinds only
// (nil: both), though a loop is found through either.
func (s *Store) supersessionLoops(tx *store.Tx, kinds []string) ([]Unresolved, error) {
	rows, err := kindsIn(s.linksIn(tx), []string{kindSupersedes, kindSupersededBy}).
		WithPredicate(dao.IsNotNull(`"link"."dst_doc"`)).OrderBy(dao.Asc(LinkByOtherPath), dao.Asc(LinkByID)).
		Select(LinkSrc, LinkDst, LinkKind, LinkRaw, LinkOtherPath)
	if err != nil {
		return nil, err
	}
	// every relation as "replaced -> successor", whichever side wrote it
	g := graph.New[int64]()
	edge := func(src, dst int64, kind string) (old, successor int64) {
		if kind == kindSupersededBy {
			return src, dst
		}
		return dst, src
	}
	for _, r := range rows {
		old, successor := edge(r.SrcDoc, *r.DstDoc, r.Kind)
		g.Add(graph.Edge[int64]{Src: old, Dst: successor, Kind: kindSupersededBy})
	}
	var out []Unresolved
	for _, r := range rows {
		old, successor := edge(r.SrcDoc, *r.DstDoc, r.Kind)
		// the loop is found through both kinds; a row is listed only when its own kind was asked for
		if (kinds == nil || slices.Contains(kinds, r.Kind)) && old != successor &&
			g.Neighborhood(successor, len(rows), graph.Out, graph.Filter{}).Has(old) {
			out = append(out, Unresolved{Src: r.OtherPath, Raw: r.Raw, Reason: ReasonCycle})
		}
	}
	return out, nil
}

func (s *Store) docID(tx *store.Tx, path string) (int64, error) {
	d, err := s.documents(tx).With(DocPath, path).Get(DocID)
	if errors.Is(err, dao.ErrNoRows) {
		return 0, ErrNoDocument
	}
	if err != nil {
		return 0, err
	}
	return d.ID, nil
}
