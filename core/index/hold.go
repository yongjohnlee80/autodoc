package index

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// Identity is a document.indexer string read back (ADR 0216 §1.6):
//
//	built-in   = "c" CHUNKV ".s" SCHEMA ".t" TOKENS [".f" SCHEMAFP]
//	registered = "c" EXT "@" VERSION ".s" SCHEMA ".t" TOKENS
//	derived    = "d" EXT ":" DERIVER "@" VERSION "+" built-in
//
// '@', ':' and whitespace never occur inside a field, so a string parses one way: the first
// character names the form ("c" and a digit is built-in, "c." registered); EXT ends at the first
// '@' or ':', DERIVER at the first '@'. A VERSION may hold '+' and '.' (autorag's Go chunker is
// "code-go-1+text-5"), so a derived string splits at its last '+', since a built-in never holds one,
// and a registered string is read from the right: TOKENS after the last ".t", SCHEMA after the ".s"
// before it.
type Identity struct {
	Form    Form
	Ext     string // registered and derived: the extension, with its dot
	Version string // registered: the chunker's; derived: the deriver's
	Deriver string // derived: the deriver's id
	// Built is a derived document's built-in identity: its text is Markdown, chunked as such.
	Built             string
	Chunker, SchemaFP string // built-in: golib's chunker version, and Markdown's schema fingerprint
	Schema, Tokens    int    // built-in and registered
}

// Form is which of document.indexer's forms a string is.
type Form byte

const (
	BuiltIn    Form = 'b'
	Registered Form = 'r'
	Derived    Form = 'd'
)

// ErrIdentity is a document.indexer string outside the grammar.
var ErrIdentity = errors.New("index: not a document.indexer identity")

// ParseIdentity reads a document.indexer string.
func ParseIdentity(s string) (Identity, error) {
	bad := fmt.Errorf("%w: %q", ErrIdentity, s)
	switch {
	case strings.HasPrefix(s, "d"):
		ext, rest, ok := strings.Cut(s[1:], ":")
		if !ok || !kind.ValidExtension(ext) {
			return Identity{}, bad
		}
		id, rest, ok := strings.Cut(rest, "@")
		if !ok || !registrations.ValidDeriverID(id) {
			return Identity{}, bad
		}
		i := strings.LastIndexByte(rest, '+')
		if i < 0 || !registrations.ValidVersion(rest[:i]) {
			return Identity{}, bad
		}
		if b, err := ParseIdentity(rest[i+1:]); err != nil || b.Form != BuiltIn {
			return Identity{}, bad
		}
		return Identity{Form: Derived, Ext: ext, Deriver: id, Version: rest[:i], Built: rest[i+1:]}, nil
	case strings.HasPrefix(s, "c."):
		ext, rest, ok := strings.Cut(s[1:], "@")
		if !ok || !kind.ValidExtension(ext) {
			return Identity{}, bad
		}
		t := strings.LastIndex(rest, ".t")
		if t < 0 {
			return Identity{}, bad
		}
		sc := strings.LastIndex(rest[:t], ".s")
		if sc < 0 || !registrations.ValidVersion(rest[:sc]) {
			return Identity{}, bad
		}
		schema, ok1 := digits(rest[sc+2 : t])
		tokens, ok2 := digits(rest[t+2:])
		if !ok1 || !ok2 {
			return Identity{}, bad
		}
		return Identity{Form: Registered, Ext: ext, Version: rest[:sc], Schema: schema, Tokens: tokens}, nil
	case len(s) > 1 && s[0] == 'c' && s[1] >= '0' && s[1] <= '9':
		chunker, rest, ok := strings.Cut(s[1:], ".s")
		if _, isNum := digits(chunker); !ok || !isNum {
			return Identity{}, bad
		}
		sch, rest, ok := strings.Cut(rest, ".t")
		schema, ok1 := digits(sch)
		if !ok || !ok1 {
			return Identity{}, bad
		}
		tok, fp, hasFP := strings.Cut(rest, ".f")
		tokens, ok2 := digits(tok)
		if !ok2 || hasFP && !isHex12(fp) {
			return Identity{}, bad
		}
		return Identity{Form: BuiltIn, Chunker: chunker, Schema: schema, Tokens: tokens, SchemaFP: fp}, nil
	}
	return Identity{}, bad
}

func digits(s string) (int, bool) {
	if s == "" || len(s) > 9 {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

func isHex12(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// derivedVersion is what document.indexer records for a file a deriver made text of: the format,
// the deriver's id and version, then the built-in identity the derived Markdown was chunked under.
func derivedVersion(ext, id, version, builtIn string) string {
	return "d" + ext + ":" + id + "@" + version + "+" + builtIn
}

// The states of a held document's file (Hit.Hold), "" for a document this daemon interprets.
const (
	HoldCurrent   = "current"   // the file is as it was indexed
	HoldStale     = "stale"     // the file changed since: its spans may have moved
	HoldUnchecked = "unchecked" // neither the scan nor the watcher has seen the file yet
)

// HeldError is a reinterpretation refused: the document was made by a chunker or a format this
// daemon lacks (ADR 0216 §1.4).
type HeldError struct{ Path, Ext, What string }

func (e *HeldError) Error() string {
	return fmt.Sprintf("held: this backend has no %s for %s: start a build that has it, or take %s out of the workspace's includes to drop these documents", e.What, e.Ext, e.Ext)
}

// heldBy says why this daemon holds a document of identity s: the chunker or the format it
// lacks, "" when it interprets it. A string outside the grammar is not held: it is indexed again,
// as any document of another version is.
func heldBy(s string, reg *registrations.Table) (ext, what string) {
	id, err := ParseIdentity(s)
	if err != nil {
		return "", ""
	}
	switch id.Form {
	case Registered:
		if !reg.Has(id.Ext) {
			return id.Ext, "chunker"
		}
	case Derived:
		return id.Ext, "deriver" // no build derives yet: AutoDoc 03 wires the deriver
	}
	return "", ""
}

// holds is the documents this daemon holds and how each one's file stands: held from the row (its
// document.indexer against the frozen registrations), read once, when first asked, and never
// waiting for a scan. The set only shrinks: the registrations are frozen, a daemon writes only its
// own identities, and one daemon serves a store, so no row becomes held mid-session. A held
// document leaves when it is deleted from disk, excluded, or its workspace removed. The file's
// state is set by the indexer's writer from prepare's result, never in core/follow, so a result of
// an older touch, which the writer discards, never sets it.
type holds struct {
	mu     sync.Mutex
	loaded bool
	m      map[string]held // by path
}

type held struct{ ext, what, state string }

// load reads the held documents once.
func (h *holds) load(ctx context.Context, s *Store, reg *registrations.Table) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.loaded {
		return nil
	}
	m := map[string]held{}
	err := s.read(ctx, func(tx *store.Tx) error {
		docs, err := s.sc.Documents(tx).OrderBy(dao.Asc(store.ByPath)).Select(store.DocPath, store.DocIndexer)
		for _, d := range docs {
			if ext, what := heldBy(d.Indexer, reg); what != "" {
				m[d.Path] = held{ext: ext, what: what, state: HoldUnchecked}
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	h.m, h.loaded = m, true
	return nil
}

// of is path's hold, if it is held.
func (h *holds) of(path string) (held, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d, ok := h.m[path]
	return d, ok
}

// set records how a held document's file stands, after the writer committed it.
func (h *holds) set(path, state string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if d, ok := h.m[path]; ok {
		d.state = state
		h.m[path] = d
	}
}

// drop forgets a document that left the index.
func (h *holds) drop(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.m, path)
}

// counts are the held documents by their files' states.
func (h *holds) counts() (current, stale, unchecked int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, d := range h.m {
		switch d.state {
		case HoldCurrent:
			current++
		case HoldStale:
			stale++
		default:
			unchecked++
		}
	}
	return current, stale, unchecked
}

// holding is the indexer's held set, read on first use.
func (x *Indexer) holding(ctx context.Context) (*holds, error) {
	if err := x.holds.load(ctx, x.store, x.opts.Registrations); err != nil {
		return nil, fmt.Errorf("index: reading the held documents: %w", err)
	}
	return &x.holds, nil
}
