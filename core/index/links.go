package index

import (
	"context"
	"errors"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/parse/markdown"
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

// linkT is one link a note makes. name is what resolution looks up: for a wikilink or an embed the
// lowercased page name without ".md", after a '/' when it is a relative path (which names one path,
// never a file name elsewhere); for a markdown link the workspace path it reaches.
type linkT struct {
	raw, name, anchor, kind string
}

// attachments are the file types Obsidian opens that are not notes
// (https://help.obsidian.md/file-formats). A link to one is not an edge between notes, unless the
// workspace indexes that type.
var attachments = map[string]bool{
	".base": true, ".canvas": true, ".pdf": true,
	".avif": true, ".bmp": true, ".gif": true, ".jpeg": true, ".jpg": true, ".png": true, ".svg": true, ".webp": true,
	".flac": true, ".m4a": true, ".mp3": true, ".ogg": true, ".wav": true, ".webm": true, ".3gp": true,
	".mkv": true, ".mov": true, ".mp4": true, ".ogv": true,
}

// extractLinks takes the links of note p, in source order: wikilinks and embeds, and markdown
// links. Left out: a link to a heading or block of p itself, a URL, a path out of the workspace, and
// a file that is not a note. A wikilink names a note unless it names an attachment (a page may hold
// dots: "Meeting 2024.05.01"); a markdown link, which names files of every kind (a KB links to its
// code), only with ".md" or no extension. Either counts when eligible (nil: nothing) indexes it,
// judged on the workspace path for a path, and on the name as written for a wikilink by name.
func extractLinks(doc *markdown.Document, p string, eligible func(string) bool) []linkT {
	var out []linkT
	indexed := func(target string) bool { return eligible != nil && eligible(target) }
	wikiNote := func(page string) bool { return !attachments[strings.ToLower(path.Ext(page))] || indexed(page) }
	fileNote := func(target string) bool {
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
				if !wikiNote(target) {
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
				if target, anchor, ok := localTarget(p, string(c.Dest)); ok && fileNote(target) {
					out = append(out, linkT{raw: string(doc.Source[c.Span.Start:c.Span.End]), name: target, anchor: anchor, kind: LinkMarkdown})
				}
			}
			walk(c)
		}
	}
	walk(doc.Root)
	return out
}

// localTarget reads a markdown link's destination as a workspace path: relative to the note's
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
func resolve(ctx context.Context, q dao.Querier, kind, name string) (dst int64, reason string, err error) {
	if kind == LinkMarkdown {
		for _, p := range []string{name, name + ".md"} {
			err := scanOne(ctx, q, &dst, "SELECT id FROM document WHERE path = ?", p)
			if err == nil {
				return dst, "", nil
			}
			if !errors.Is(err, errNoRow) {
				return 0, "", err
			}
			if strings.HasSuffix(name, ".md") {
				break
			}
		}
		return 0, ReasonMissing, nil
	}
	query := "SELECT d.id, d.path, n.is_path FROM doc_name n JOIN document d ON d.id = n.doc_id WHERE n.key = ?"
	if strings.HasPrefix(name, "/") {
		query, name = query+" AND n.is_path", name[1:]
	}
	rows, err := q.QueryContext(ctx, query, name)
	if err != nil {
		return 0, "", err
	}
	type cand struct {
		id     int64
		path   string
		isPath bool
	}
	var all, paths []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.path, &c.isPath); err != nil {
			rows.Close()
			return 0, "", err
		}
		all = append(all, c)
		if c.isPath {
			paths = append(paths, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, "", err
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
func storedNames(ctx context.Context, tx dao.TxConn, docID int64) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT key, is_path FROM doc_name WHERE doc_id = ?", docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		var isPath bool
		if err := rows.Scan(&k, &isPath); err != nil {
			return nil, err
		}
		out[k] = isPath
	}
	return out, rows.Err()
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
func writeNames(ctx context.Context, tx dao.TxConn, docID int64, names []docName) ([]string, error) {
	old, err := storedNames(ctx, tx, docID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM doc_name WHERE doc_id = ?", docID); err != nil {
		return nil, err
	}
	var changed []string
	for _, n := range names {
		if _, err := tx.ExecContext(ctx, "INSERT INTO doc_name(key, doc_id, is_path) VALUES (?, ?, ?)", n.key, docID, n.isPath); err != nil {
			return nil, err
		}
		if wasPath, ok := old[n.key]; !ok || wasPath != n.isPath {
			changed = append(changed, linkNames(n.key, n.isPath || wasPath)...)
		}
		delete(old, n.key)
	}
	for k, isPath := range old {
		changed = append(changed, linkNames(k, isPath)...)
	}
	return changed, nil
}

// writeLinks replaces a document's links with links, each resolved as the index stands.
func writeLinks(ctx context.Context, tx dao.TxConn, docID, gen int64, links []linkT) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM link WHERE src_doc = ?", docID); err != nil {
		return err
	}
	for _, l := range links {
		dst, _, err := resolve(ctx, tx, l.kind, l.name)
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
		if _, err := tx.ExecContext(ctx, "INSERT INTO link(src_doc, gen_from, raw, name, dst_doc, anchor, kind) VALUES (?, ?, ?, ?, ?, ?, ?)",
			docID, gen, l.raw, l.name, d, anchor, l.kind); err != nil {
			return err
		}
	}
	return nil
}

// reresolve resolves again every link stored under one of names, and every link that reached
// docID (0: none), in the same transaction as the change that moved them.
func reresolve(ctx context.Context, tx dao.TxConn, names []string, docID int64) error {
	if len(names) == 0 && docID == 0 {
		return nil
	}
	q := "SELECT rowid, kind, name, COALESCE(dst_doc, 0) FROM link WHERE dst_doc = ?"
	args := []any{docID}
	if len(names) > 0 {
		q += " OR name IN (?" + strings.Repeat(", ?", len(names)-1) + ")"
		for _, n := range names {
			args = append(args, n)
		}
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	type row struct {
		id, dst    int64
		kind, name string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.kind, &r.name, &r.dst); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range all {
		dst, _, err := resolve(ctx, tx, r.kind, r.name)
		if err != nil {
			return err
		}
		if dst == r.dst {
			continue
		}
		var d any
		if dst != 0 {
			d = dst
		}
		if _, err := tx.ExecContext(ctx, "UPDATE link SET dst_doc = ? WHERE rowid = ?", d, r.id); err != nil {
			return err
		}
	}
	return nil
}
