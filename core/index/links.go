package index

import (
	"errors"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/parse/markdown"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// The kinds of link the link table holds.
const (
	LinkWikilink = "wikilink"
	LinkEmbed    = "embed"
	LinkMarkdown = "markdown"
)

// Why a link is unresolved.
const (
	ReasonMissing   = "missing"   // no document answers to its name
	ReasonAmbiguous = "ambiguous" // several do, at the same depth
)

// linkT is one link a file makes. name is what resolution looks up: for a wikilink or an embed the
// lowercased page name without ".md", after a '/' when it is a relative path (which names one path,
// never a file name elsewhere); for a markdown link the workspace path it reaches.
type linkT struct {
	raw, name, anchor, kind string
	// keys are a relation link's names, tried in order, name being the first; nil for a body link,
	// which has the one name
	keys []string
}

// attachments are the file types Obsidian opens that are not files
// (https://help.obsidian.md/file-formats). A link to one is not an edge between files, unless the
// workspace indexes that type.
var attachments = map[string]bool{
	".base": true, ".canvas": true, ".pdf": true,
	".avif": true, ".bmp": true, ".gif": true, ".jpeg": true, ".jpg": true, ".png": true, ".svg": true, ".webp": true,
	".flac": true, ".m4a": true, ".mp3": true, ".ogg": true, ".wav": true, ".webm": true, ".3gp": true,
	".mkv": true, ".mov": true, ".mp4": true, ".ogv": true,
}

// extractLinks takes the links of file p, in source order: wikilinks and embeds, and markdown
// links. Left out: a link to a heading or block of p itself, a URL, a path out of the workspace, and
// a file that is not a file. A wikilink names a file unless it names an attachment (a page may hold
// dots: "Meeting 2024.05.01"); a markdown link, which names files of every kind (a KB links to its
// code), only with ".md" or no extension. Either counts when eligible (nil: nothing) indexes it,
// judged on the workspace path for a path, and on the name as written for a wikilink by name.
func extractLinks(doc *markdown.Document, p string, eligible func(string) bool) []linkT {
	var out []linkT
	indexed := func(target string) bool { return eligible != nil && eligible(target) }
	wikiFile := func(page string) bool { return !attachments[strings.ToLower(path.Ext(page))] || indexed(page) }
	targetsFile := func(target string) bool {
		ext := strings.ToLower(path.Ext(target))
		return ext == "" || ext == ".md" || indexed(target)
	}
	var walk func(*markdown.Node)
	walk = func(n *markdown.Node) {
		for c := n.FirstChild; c != nil; c = c.Next {
			switch c.Kind {
			case markdown.KindWikilink, markdown.KindEmbed:
				t := c.Target
				page := strings.TrimSpace(string(t.Page))
				if page == "" {
					continue
				}
				// a relative path, which Obsidian writes when set to, names one workspace path: that path
				// is what eligible judges and what the link reaches
				target, relative := page, strings.HasPrefix(page, "./") || strings.HasPrefix(page, "../")
				if relative {
					if target = path.Join(path.Dir(p), page); target == ".." || strings.HasPrefix(target, "../") {
						continue
					}
				}
				if !wikiFile(target) {
					continue
				}
				name := wikiName(target)
				if relative {
					name = exactName(name)
				}
				anchor := string(t.Heading)
				if len(t.Block) > 0 {
					anchor = "^" + string(t.Block)
				}
				kind := LinkWikilink
				if c.Kind == markdown.KindEmbed {
					kind = LinkEmbed
				}
				out = append(out, linkT{raw: string(doc.Source[c.Span.Start:c.Span.End]), name: name, anchor: anchor, kind: kind})
				continue
			case markdown.KindLink:
				if target, anchor, ok := localTarget(p, string(c.Dest)); ok && targetsFile(target) {
					out = append(out, linkT{raw: string(doc.Source[c.Span.Start:c.Span.End]), name: target, anchor: anchor, kind: LinkMarkdown})
				}
			}
			walk(c)
		}
	}
	walk(doc.Root)
	return out
}

// localTarget reads a markdown link's destination as a workspace path: relative to the file's
// directory, or to the root with a leading '/'. A URL (a scheme, or "//"), a bare "#anchor" and a
// path out of the root are not local.
func localTarget(from, dest string) (target, anchor string, ok bool) {
	if dest == "" || strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "//") {
		return "", "", false
	}
	if u, err := url.Parse(dest); err == nil && u.Scheme != "" {
		return "", "", false
	}
	if i := strings.IndexByte(dest, '#'); i >= 0 {
		dest, anchor = dest[:i], dest[i+1:]
	}
	if i := strings.IndexByte(dest, '?'); i >= 0 {
		dest = dest[:i]
	}
	if d, err := url.PathUnescape(dest); err == nil {
		dest = d
	}
	if a, err := url.PathUnescape(anchor); err == nil {
		anchor = a
	}
	if strings.HasPrefix(dest, "/") {
		target = path.Clean(strings.TrimLeft(dest, "/"))
	} else {
		target = path.Join(path.Dir(from), dest)
	}
	if target == "." || target == ".." || strings.HasPrefix(target, "../") {
		return "", "", false
	}
	return target, anchor, true
}

// wikiName is how a wikilink's page is compared: lowercased (as Obsidian compares names), without
// a ".md" extension.
func wikiName(page string) string {
	return strings.TrimSuffix(strings.ToLower(page), ".md")
}

// exactName is the name of a wikilink that reaches only the document whose path is key.
func exactName(key string) string { return "/" + key }

// docName is one name a document answers to: isPath when it is the document's whole path.
type docName struct {
	key    string
	isPath bool
}

// namesOf lists the wikilink names of the document at p: every trailing run of its path's segments
// (d/x.md answers to [[d/x]] and [[x]]), and its aliases.
func namesOf(p string, aliases []string) []docName {
	segs := strings.Split(wikiName(p), "/")
	seen := map[string]int{}
	var out []docName
	add := func(key string, isPath bool) {
		if key == "" {
			return
		}
		if i, ok := seen[key]; ok {
			out[i].isPath = out[i].isPath || isPath
			return
		}
		seen[key] = len(out)
		out = append(out, docName{key, isPath})
	}
	for i := range segs {
		add(strings.Join(segs[i:], "/"), i == 0)
	}
	for _, a := range aliases {
		add(strings.ToLower(strings.TrimSpace(a)), false)
	}
	return out
}

// markdownNames are the names a markdown link reaching p is stored under: the path, and the path
// without ".md" (which resolves to it too).
func markdownNames(p string) []string {
	if s := strings.TrimSuffix(p, ".md"); s != p {
		return []string{p, s}
	}
	return []string{p}
}

// resolve finds the document a link reaches (ADR 0204 §4.6). A markdown link reaches its path, or
// the path with ".md". A wikilink reaches the document whose path is its name; else the one
// document with that file name, path suffix or alias; of several, the one nearest the root; still
// tied, none (ReasonAmbiguous). Readers and the writer resolve with this one function, so a reader's
// reason for an unresolved link is the writer's.
func (s *Store) resolve(tx *store.Tx, kind, name string) (dst int64, reason string, err error) {
	if kind == LinkMarkdown {
		for _, p := range []string{name, name + ".md"} {
			d, err := s.sc.Documents(tx).With(store.DocPath, p).Get(store.DocID)
			if err == nil {
				return d.ID, "", nil
			}
			if !errors.Is(err, dao.ErrNoRows) {
				return 0, "", err
			}
			if strings.HasSuffix(name, ".md") {
				break
			}
		}
		return 0, ReasonMissing, nil
	}
	q := s.sc.Names(tx)
	if strings.HasPrefix(name, "/") {
		q, name = q.With(store.NameIsPath, int64(1)), name[1:]
	}
	rows, err := q.With(store.NameKey, name).Select(store.NameDoc, store.NameDocPath, store.NameIsPath)
	if err != nil {
		return 0, "", err
	}
	type cand struct {
		id     int64
		path   string
		isPath bool
	}
	var all, paths []cand
	for _, r := range rows {
		c := cand{r.DocID, r.DocPath, r.IsPath == 1}
		all = append(all, c)
		if c.isPath {
			paths = append(paths, c)
		}
	}
	if len(paths) > 0 {
		all = paths // the exact path wins over every file name and alias
	}
	if len(all) == 0 {
		return 0, ReasonMissing, nil
	}
	sort.Slice(all, func(i, j int) bool { return strings.Count(all[i].path, "/") < strings.Count(all[j].path, "/") })
	if len(all) > 1 && strings.Count(all[0].path, "/") == strings.Count(all[1].path, "/") {
		return 0, ReasonAmbiguous, nil
	}
	return all[0].id, "", nil
}

// storedNames reads the wikilink names a document answers to, each with whether it is the path.
func (s *Store) storedNames(tx *store.Tx, docID int64) (map[string]bool, error) {
	rows, err := s.sc.Names(tx).With(store.NameDoc, docID).Select(store.NameKey, store.NameIsPath)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[r.Key] = r.IsPath == 1
	}
	return out, nil
}

// linkNames are the link names under which a document's name is looked up: the name, and for its
// path also the exact form.
func linkNames(key string, isPath bool) []string {
	if isPath {
		return []string{key, exactName(key)}
	}
	return []string{key}
}

// writeNames replaces a document's names, returning the ones that came or went: the links under
// those names may now resolve differently.
func (s *Store) writeNames(tx *store.Tx, docID int64, names []docName) ([]string, error) {
	old, err := s.storedNames(tx, docID)
	if err != nil {
		return nil, err
	}
	if err := s.sc.Names(tx).With(store.NameDoc, docID).Delete(); err != nil {
		return nil, err
	}
	var changed []string
	b := s.sc.NameBatch(tx)
	for _, n := range names {
		isPath := int64(0)
		if n.isPath {
			isPath = 1
		}
		b.Add(map[store.DocNameField]any{store.NameKey: n.key, store.NameDoc: docID, store.NameIsPath: isPath})
		if wasPath, ok := old[n.key]; !ok || wasPath != n.isPath {
			changed = append(changed, linkNames(n.key, n.isPath || wasPath)...)
		}
		delete(old, n.key)
	}
	if len(names) > 0 {
		if err := b.Flush(); err != nil {
			return nil, err
		}
	}
	for k, isPath := range old {
		changed = append(changed, linkNames(k, isPath)...)
	}
	return changed, nil
}

// resolveKeys finds the document a relation link reaches: the first of its names, in order, that
// resolves as a wikilink name does. Unresolved, the reason is the first name's.
func (s *Store) resolveKeys(tx *store.Tx, keys []string) (dst int64, reason string, err error) {
	for i, k := range keys {
		d, why, err := s.resolve(tx, LinkWikilink, k)
		if err != nil || d != 0 {
			return d, "", err
		}
		if i == 0 {
			reason = why
		}
	}
	return 0, reason, nil
}

// keysOf reads a relation link's names, in order.
func (s *Store) keysOf(tx *store.Tx, linkID int64) ([]string, error) {
	rows, err := s.sc.LinkKeys(tx).With(store.LinkKeyLink, linkID).Select(store.LinkKeyOrd, store.LinkKeyKey)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Ord < rows[j].Ord })
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = r.Key
	}
	return keys, nil
}

// resolveStored resolves a stored link as the writer did: a relation through its names in order, a
// body link through its one name.
func (s *Store) resolveStored(tx *store.Tx, linkID int64, kind, name string) (dst int64, reason string, err error) {
	if !isRelation(kind) {
		return s.resolve(tx, kind, name)
	}
	// writeLinks stores a relation's names with it, in the one transaction, and it has at least one
	keys, err := s.keysOf(tx, linkID)
	if err != nil {
		return 0, "", err
	}
	return s.resolveKeys(tx, keys)
}

// writeLinks replaces a document's links with links, each resolved as the index stands; a relation
// link's names go in link_key, in order, so it is found again when any of them changes.
func (s *Store) writeLinks(tx *store.Tx, docID, gen int64, links []linkT) error {
	if err := s.sc.LinksOut(tx).With(store.LinkSrc, docID).Delete(); err != nil {
		return err
	}
	for _, l := range links {
		var dst int64
		var err error
		if l.keys != nil {
			dst, _, err = s.resolveKeys(tx, l.keys)
		} else {
			dst, _, err = s.resolve(tx, l.kind, l.name)
		}
		if err != nil {
			return err
		}
		var d, anchor any
		if dst != 0 {
			d = dst
		}
		if l.anchor != "" {
			anchor = l.anchor
		}
		id, err := s.sc.LinksOut(tx).Set(store.LinkSrc, docID).Set(store.LinkGenFrom, gen).Set(store.LinkRaw, l.raw).
			Set(store.LinkName, l.name).Set(store.LinkDst, d).Set(store.LinkAnchor, anchor).Set(store.LinkKind, l.kind).Insert()
		if err != nil {
			return err
		}
		if len(l.keys) > 0 {
			b := s.sc.LinkKeyBatch(tx)
			for i, k := range l.keys {
				b.Add(map[store.LinkKeyField]any{store.LinkKeyLink: id, store.LinkKeyOrd: int64(i), store.LinkKeyKey: k})
			}
			if err := b.Flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

// reresolve resolves again every link stored under one of names, and every link that reached
// docID (0: none), in the same transaction as the change that moved them.
func (s *Store) reresolve(tx *store.Tx, names []string, docID int64) error {
	if len(names) == 0 && docID == 0 {
		return nil
	}
	// two probes, not one OR: each is one of the link table's indexes, which lead with the workspace
	var rows []*store.Link
	seen := map[int64]bool{}
	probe := func(cond dao.Predicate) error {
		got, err := s.sc.LinksOut(tx).WithPredicate(cond).Select(store.LinkID, store.LinkKind, store.LinkName, store.LinkDst)
		for _, r := range got {
			if !seen[r.ID] {
				seen[r.ID] = true
				rows = append(rows, r)
			}
		}
		return err
	}
	if docID != 0 {
		if err := probe(dao.Eq(`"link"."dst_doc"`, docID)); err != nil {
			return err
		}
	}
	if len(names) > 0 {
		vs := make([]any, len(names))
		for i, n := range names {
			vs[i] = n
		}
		if err := probe(dao.In(`"link"."name"`, vs)); err != nil {
			return err
		}
		// a relation link is found by any of its names, not only the first
		keyed, err := s.sc.LinkKeys(tx).WithPredicate(dao.In(`"link_key"."key"`, vs)).Select(store.LinkKeyLink)
		if err != nil {
			return err
		}
		var ids []any
		for _, k := range keyed {
			if !seen[k.LinkID] {
				ids = append(ids, k.LinkID)
			}
		}
		if len(ids) > 0 {
			if err := probe(dao.In(`"link"."id"`, ids)); err != nil {
				return err
			}
		}
	}
	for _, r := range rows {
		dst, _, err := s.resolveStored(tx, r.ID, r.Kind, r.Name)
		if err != nil {
			return err
		}
		var was int64
		if r.DstDoc != nil {
			was = *r.DstDoc
		}
		if dst == was {
			continue
		}
		var d any
		if dst != 0 {
			d = dst
		}
		if err := s.sc.LinksOut(tx).With(store.LinkID, r.ID).Set(store.LinkDst, d).Update(); err != nil {
			return err
		}
	}
	return nil
}
