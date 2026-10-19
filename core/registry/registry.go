// Package registry is the AutoDoc registry's index format and its rules (ADR 1792351195): the one
// owner of both. AutoDoc's client decodes and validates the index with it, and the registry's own
// tools (github.com/yongjohnlee80/autodoc-plugin-registry) build and check it with it, imported at a
// pinned AutoDoc version as they import core/manifests. It has no UI dependencies.
package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Format is the index's format version: a client refuses an index of a format it does not know.
const Format = 1

// PagesURL is where the registry's site is served from: index.json, and the audit reports.
const PagesURL = "https://yongjohnlee80.github.io/autodoc-plugin-registry/"

// The kinds an entry may be.
const (
	KindPlugin = "plugin"
	KindLua    = "lua"
	KindKBType = "kb-type"
)

// Kinds are every kind, in the order a list shows them.
var Kinds = []string{KindPlugin, KindLua, KindKBType}

// Hosts are where an entry's repo may live.
var Hosts = []string{"github.com", "gitlab.com", "codeberg.org"}

// Index is index.json.
type Index struct {
	Format    int          `json:"format"`
	Generated string       `json:"generated"` // RFC 3339, UTC: the registry's own commit time
	Entries   []IndexEntry `json:"entries"`
}

// IndexEntry is an entry as the index lists it: what the entry says, what its manifest declares at
// a named commit, and its audits, newest first, each with its report's URL.
type IndexEntry struct {
	Kind        string       `json:"kind"`
	Name        string       `json:"name"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Repo        string       `json:"repo"`
	Author      string       `json:"author"`
	Declares    Declares     `json:"declares"`
	Audits      []IndexAudit `json:"audits"`
}

// IndexAudit is an audit as the index lists it.
type IndexAudit struct {
	Commit string `json:"commit"` // the full SHA audited
	Model  string `json:"model"`
	Date   string `json:"date"` // YYYY-MM-DD
	By     string `json:"by"`
	Report string `json:"report"` // PagesURL + ReportPath(kind, name, commit)
}

// Declares is what an entry's manifest declares, at a commit: the newest audited one, or HEAD when
// none is.
type Declares struct {
	Commit string   `json:"commit"`
	Items  []string `json:"items"`
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// IsFullSHA reports whether s is a full 40-character commit ID: a tag can move, a commit can't.
func IsFullSHA(s string) bool { return fullSHA.MatchString(s) }

// Short is a commit as people read it: its first seven characters. It is never how commits are
// compared (AuditAt compares them in full).
func Short(commit string) string { return commit[:min(7, len(commit))] }

// ReportPath is where the report of an audit of kind/name at commit lives, under the registry's root
// and under PagesURL.
func ReportPath(kind, name, commit string) string {
	return path.Join("audits", kind, name, commit+".md")
}

// CheckRepo is why a repo URL is refused: https only, on one of Hosts, owner and repo, nothing else.
func CheckRepo(repo string) error {
	rest, ok := strings.CutPrefix(repo, "https://")
	if !ok {
		return errors.New("want an https:// URL")
	}
	host, p, _ := strings.Cut(rest, "/")
	if !slices.Contains(Hosts, host) {
		return fmt.Errorf("host %q: want one of %s", host, strings.Join(Hosts, ", "))
	}
	parts := strings.Split(strings.TrimSuffix(p, ".git"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(p, "?#@ ") {
		return errors.New("want https://<host>/<owner>/<repo>")
	}
	return nil
}

// SameRepo reports whether two repo URLs name one repository: an install by URL matches an entry
// whatever its trailing ".git" or "/".
func SameRepo(a, b string) bool {
	norm := func(s string) string {
		return strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s), "/"), ".git"))
	}
	return a != "" && norm(a) == norm(b)
}

// Decode reads an index and validates it (Validate). Fields it does not know are ignored: a format
// adds them without breaking a client; a format change is what Format is for.
func Decode(b []byte) (Index, error) {
	var ix Index
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&ix); err != nil {
		return Index{}, fmt.Errorf("registry index: %w", err)
	}
	if err := Validate(ix); err != nil {
		return Index{}, err
	}
	return ix, nil
}

// Validate is the client's check of an index (ADR 1792351195 §4.5): its format; each entry's kind,
// name and repo, (kind, name) unique; every commit a full SHA; each audit's report under the
// registry's own Pages origin, at its ReportPath.
func Validate(ix Index) error {
	if ix.Format != Format {
		return fmt.Errorf("registry index: format %d; this AutoDoc reads format %d", ix.Format, Format)
	}
	if _, err := time.Parse(time.RFC3339, ix.Generated); err != nil {
		return fmt.Errorf("registry index: generated %q: want an RFC 3339 time", ix.Generated)
	}
	seen := map[string]bool{}
	for i, e := range ix.Entries {
		bad := func(format string, args ...any) error {
			return fmt.Errorf("registry index: entry %d (%s %s): "+format, append([]any{i + 1, e.Kind, e.Name}, args...)...)
		}
		switch {
		case !slices.Contains(Kinds, e.Kind):
			return bad("kind %q: want one of %s", e.Kind, strings.Join(Kinds, ", "))
		case e.Name == "" || strings.ContainsAny(e.Name, `/\ `):
			return bad("name %q", e.Name)
		case seen[e.Kind+"/"+e.Name]:
			return bad("listed twice")
		case e.Declares.Commit != "" && !IsFullSHA(e.Declares.Commit):
			return bad("declares at %q: want a full commit", e.Declares.Commit)
		}
		seen[e.Kind+"/"+e.Name] = true
		if err := CheckRepo(e.Repo); err != nil {
			return bad("repo %q: %v", e.Repo, err)
		}
		for j, a := range e.Audits {
			switch {
			case !IsFullSHA(a.Commit):
				return bad("audit %d: commit %q: want the full 40-character SHA", j+1, a.Commit)
			case a.Report != PagesURL+ReportPath(e.Kind, e.Name, a.Commit):
				return bad("audit %d: report %q: want %s", j+1, a.Report, PagesURL+ReportPath(e.Kind, e.Name, a.Commit))
			case strings.TrimSpace(a.Model) == "" || strings.TrimSpace(a.Date) == "":
				return bad("audit %d: model and date are required", j+1)
			}
		}
	}
	return nil
}

// Entry is the entry of kind and name; nil for none.
func (ix Index) Entry(kind, name string) *IndexEntry {
	for i := range ix.Entries {
		if ix.Entries[i].Kind == kind && ix.Entries[i].Name == name {
			return &ix.Entries[i]
		}
	}
	return nil
}

// ByRepo is the entry of kind whose repo is repo (SameRepo); nil for none.
func (ix Index) ByRepo(kind, repo string) *IndexEntry {
	for i := range ix.Entries {
		if ix.Entries[i].Kind == kind && SameRepo(ix.Entries[i].Repo, repo) {
			return &ix.Entries[i]
		}
	}
	return nil
}

// AuditAt is e's audit of commit, compared in full: a prefix, however long, is never a match.
func (e *IndexEntry) AuditAt(commit string) *IndexAudit {
	if e == nil || !IsFullSHA(commit) {
		return nil
	}
	for i := range e.Audits {
		if e.Audits[i].Commit == commit {
			return &e.Audits[i]
		}
	}
	return nil
}
