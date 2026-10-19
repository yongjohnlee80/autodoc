package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	regindex "github.com/yongjohnlee80/autodoc/core/registry"
)

// THE REGISTRY — AutoDoc's list of plugins, Lua packages and KB types (ADR 1792351195 §4.5–§4.7):
// one index.json, its format core/registry's, fetched from the registry's Pages site only when a
// list opens (never at startup, never in the background), conditionally on the cached ETag and at
// most once a day. A fetch or a validation that fails keeps the cached list, then the built-in KB
// types, and says which. Plugins › Browse the registry… lists the plugins and packages to install;
// the add dialog's KB type field lists the KB types; :lua-install and Add from a git URL take a
// name as well as a URL. An install card says whether the commit it would install was audited.

// registryURL is the index; a test points it elsewhere, as latestURL.
var registryURL = regindex.PagesURL + "index.json"

// registryCacheDir is where the index is kept between runs; a test points it elsewhere.
var registryCacheDir = func() (string, error) {
	d, err := os.UserCacheDir()
	return filepath.Join(d, "autodoc", "registry"), err
}

// registryMaxAge is how long a fetched index is used before it is asked for again.
const registryMaxAge = 24 * time.Hour

// cachedIndex is the cache's one file: the index with its ETag and when it was fetched, written
// whole (temp and rename), so the ETag and the index never disagree.
type cachedIndex struct {
	ETag    string          `json:"etag"`
	Fetched time.Time       `json:"fetched"`
	Index   json.RawMessage `json:"index"`
}

// registryList is the index a list shows, and what to say of it: "" for a fresh one, or
// "offline: …" for the cached one or the built-in KB types.
type registryList struct {
	ix   regindex.Index
	note string
}

// fetchRegistry is the index as a list opens: the cache while it is younger than registryMaxAge;
// else a GET, conditional on the cache's ETag, a 304 keeping the cache and a 200 replacing it once
// it validates. Anything failing falls back to the cache, then to the built-in KB types. Off the loop.
func fetchRegistry(ctx context.Context, now time.Time) registryList {
	cache, cacheOK := readRegistryCache()
	if cacheOK && now.Sub(cache.fetched) < registryMaxAge {
		return registryList{ix: cache.ix}
	}
	etag := ""
	if cacheOK {
		etag = cache.etag
	}
	g, err := httpGet(ctx, registryURL, "application/json", etag)
	switch {
	case err != nil:
		return registryFallback(cache, cacheOK, err)
	case g.status == http.StatusNotModified && cacheOK:
		_ = writeRegistryCache(cache.etag, now, cache.raw) // fresh for another day
		return registryList{ix: cache.ix}
	case g.status != http.StatusOK: // a 304 with no cache too: the request was unconditional
		return registryFallback(cache, cacheOK, fmt.Errorf("the registry answered %d %s", g.status, http.StatusText(g.status)))
	}
	ix, err := regindex.Decode(g.body)
	if err != nil {
		return registryFallback(cache, cacheOK, err)
	}
	_ = writeRegistryCache(g.etag, now, g.body) // a cache that can't be written is only slower
	return registryList{ix: ix}
}

// registryFallback is the list when a fetch or its validation failed: the cached one, else the
// built-in KB types, each said as such.
func registryFallback(cache registryCache, ok bool, why error) registryList {
	if ok {
		return registryList{ix: cache.ix, note: "offline: the list as of " + cache.fetched.Format(time.DateOnly) + " (" + firstLine(why.Error()) + ")"}
	}
	return registryList{ix: builtinRegistry(), note: "offline: the built-in KB types only (" + firstLine(why.Error()) + ")"}
}

// builtinRegistry is the built-in KB types as an index: what the add dialog lists with no registry.
func builtinRegistry() regindex.Index {
	ix := regindex.Index{Format: regindex.Format}
	for _, c := range builtinKBTypes {
		ix.Entries = append(ix.Entries, regindex.IndexEntry{Kind: regindex.KindKBType, Name: c.name, Title: c.title, Repo: c.url})
	}
	return ix
}

// registryCache is the cache as read: its index decoded and valid, and the bytes it holds.
type registryCache struct {
	ix      regindex.Index
	raw     []byte
	etag    string
	fetched time.Time
}

// readRegistryCache is the cached index; false when there is none, or it does not validate.
func readRegistryCache() (registryCache, bool) {
	dir, err := registryCacheDir()
	if err != nil {
		return registryCache{}, false
	}
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return registryCache{}, false
	}
	var c cachedIndex
	if json.Unmarshal(b, &c) != nil {
		return registryCache{}, false
	}
	ix, err := regindex.Decode(c.Index)
	if err != nil {
		return registryCache{}, false
	}
	return registryCache{ix: ix, raw: c.Index, etag: c.ETag, fetched: c.Fetched}, true
}

// writeRegistryCache writes the cache's file whole: a temp file renamed over it.
func writeRegistryCache(etag string, fetched time.Time, index []byte) error {
	dir, err := registryCacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(cachedIndex{ETag: etag, Fetched: fetched, Index: index})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".index-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "index.json"))
}

// cachedEntry is the cached index's entry of kind for repo, read off the loop for an install card;
// nil with no cache or no entry. A card never fetches.
func cachedEntry(kind, repo string) *regindex.IndexEntry {
	c, ok := readRegistryCache()
	if !ok {
		return nil
	}
	return c.ix.ByRepo(kind, repo)
}

// auditLine is an install card's line about audits, for entry e (nil: not from the registry) at
// commit (in full). Commits are compared in full: a prefix is never a match.
func auditLine(e *regindex.IndexEntry, commit string) string {
	switch {
	case e == nil:
		return ""
	case e.AuditAt(commit) != nil:
		a := e.AuditAt(commit)
		return "✓ audited by " + a.Model + " on " + a.Date + " at this commit"
	case len(e.Audits) > 0:
		last := e.Audits[0] // newest first
		return "not audited at this commit (last audited: " + regindex.Short(last.Commit) + ", " + last.Date + ")"
	}
	return "never audited"
}

// auditHistory is an entry's newest audit as history, not a claim about any commit installed.
func auditHistory(e regindex.IndexEntry) string {
	if len(e.Audits) == 0 {
		return "never audited"
	}
	a := e.Audits[0]
	return "✓ audited by " + a.Model + ", " + a.Date + ", at " + regindex.Short(a.Commit)
}

// isRegistryName reports whether an install's argument names a registry entry rather than a repo:
// a name holds no slash, backslash, colon or space, and every URL, path and scp-style address does.
func isRegistryName(arg string) bool {
	arg = strings.TrimSpace(arg)
	return arg != "" && !strings.ContainsAny(arg, `/\: `)
}

// resolveRegistry calls then, on the loop, with the repo of the registry's entry of kind named name,
// the index fetched as a list's is; or why there is none. loaded, when set, takes the list too (the
// host's KB type field).
func resolveRegistry(c Core, kind, name string, loaded func(registryList), then func(repo string, err error)) {
	name = strings.TrimSpace(name)
	coreDo(c, func(ctx context.Context) registryList { return fetchRegistry(ctx, time.Now()) }, func(l registryList) {
		if loaded != nil {
			loaded(l)
		}
		if e := l.ix.Entry(kind, name); e != nil {
			then(e.Repo, nil)
			return
		}
		why := "no " + kindNoun(kind) + " named " + name + " in the registry"
		if l.note != "" {
			why += " (" + l.note + ")"
		}
		then("", errors.New(why))
	})
}

func kindNoun(kind string) string {
	switch kind {
	case regindex.KindLua:
		return "Lua package"
	case regindex.KindKBType:
		return "KB type"
	}
	return "plugin"
}

// registryLoaded keeps the list last loaded and shows its KB types in the field, the type chosen
// kept chosen at its new row. An offline list says so while None is chosen.
func (h *Host) registryLoaded(l registryList) {
	h.registryNow = &l
	rows := h.kbTypeRows()
	h.kbTypes.Reset(rows)
	at := 0
	for i, r := range rows {
		if k, _ := r["key"].(string); k == h.kbTypeKey {
			at = i
		}
	}
	h.set("App.kbTypeIndex", -1)
	h.set("App.kbTypeIndex", at)
	if h.kbTypeKey == "" && l.note != "" {
		h.set("App.kbTypeSummary", noneNote+"\n"+l.note)
	}
}

// kbTypeChoices are the KB type field's types: the registry's, as last loaded, or the built-ins.
func (h *Host) kbTypeChoices() []kbTypeChoice {
	if h.registryNow == nil {
		return builtinKBTypes
	}
	var out []kbTypeChoice
	for _, e := range h.registryNow.ix.Entries {
		if e.Kind == regindex.KindKBType {
			out = append(out, kbTypeChoice{name: e.Name, title: e.Title, url: e.Repo})
		}
	}
	if len(out) == 0 {
		return builtinKBTypes
	}
	return out
}

// loadKBTypes fetches the registry as the add dialog's KB type list opens.
func (h *Host) loadKBTypes() {
	do(h, func(ctx context.Context) registryList { return fetchRegistry(ctx, time.Now()) }, h.registryLoaded)
}
