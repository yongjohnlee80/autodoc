package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	regindex "github.com/yongjohnlee80/autodoc/core/registry"
)

const (
	regC1 = "1111111111111111111111111111111111111111"
	regC2 = "2222222222222222222222222222222222222222"
	regC3 = "3333333333333333333333333333333333333333"
)

// servedReg is a registry served to the test: its index.json, with an ETag, answering 304 to a
// request that names the ETag it has; status, when set, is every answer instead. Its cache is the
// test's own.
type servedReg struct {
	mu     sync.Mutex
	body   []byte
	etag   string
	status int
	seen   []string // each request's If-None-Match
}

func serveRegistry(t *testing.T, body []byte) *servedReg {
	t.Helper()
	s := &servedReg{body: body, etag: `"v1"`}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.seen = append(s.seen, r.Header.Get("If-None-Match"))
		switch {
		case s.status != 0:
			w.WriteHeader(s.status)
		case r.Header.Get("If-None-Match") == s.etag:
			w.WriteHeader(http.StatusNotModified)
		default:
			w.Header().Set("ETag", s.etag)
			_, _ = w.Write(s.body)
		}
	}))
	t.Cleanup(srv.Close)
	url, dir := registryURL, registryCacheDir
	t.Cleanup(func() { registryURL, registryCacheDir = url, dir })
	cache := t.TempDir()
	registryURL = srv.URL + "/index.json"
	registryCacheDir = func() (string, error) { return cache, nil }
	return s
}

func (s *servedReg) set(body []byte, etag string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body, s.etag, s.status = body, etag, status
}

func (s *servedReg) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// testIndex is an index of entries, as the registry writes one.
func testIndex(t *testing.T, entries ...regindex.IndexEntry) []byte {
	t.Helper()
	b, err := json.Marshal(regindex.Index{Format: regindex.Format, Generated: "2026-10-19T00:00:00Z", Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// testEntry is an entry of kind, name and repo, audited at commits, newest first.
func testEntry(kind, name, repo string, commits ...string) regindex.IndexEntry {
	e := regindex.IndexEntry{Kind: kind, Name: name, Title: name + " title", Description: name + " says hello",
		Repo: repo, Author: "example"}
	for i, c := range commits {
		e.Audits = append(e.Audits, regindex.IndexAudit{Commit: c, Model: "claude-opus-5-5",
			Date: time.Date(2026, 10, 10-i, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), By: "example",
			Report: regindex.PagesURL + regindex.ReportPath(kind, name, c)})
	}
	return e
}

var regNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// The index is fetched as a list opens, at most daily: the cache is used while it is fresh, then
// asked for on its ETag, a 304 keeping it for another day and a 200 replacing it.
func TestTheRegistryIsReadAtMostDailyOnItsETag(t *testing.T) {
	ctx := context.Background()
	s := serveRegistry(t, testIndex(t, testEntry("plugin", "echo", "https://github.com/example/echo", regC1)))
	l := fetchRegistry(ctx, regNow)
	if l.note != "" || l.ix.Entry("plugin", "echo") == nil {
		t.Fatalf("the first fetch: %+v", l)
	}
	if l := fetchRegistry(ctx, regNow.Add(time.Hour)); len(s.requests()) != 1 || l.ix.Entry("plugin", "echo") == nil {
		t.Fatalf("an hour on: %d requests, %+v", len(s.requests()), l)
	}
	l = fetchRegistry(ctx, regNow.Add(25*time.Hour))
	if got := s.requests(); len(got) != 2 || got[1] != `"v1"` {
		t.Fatalf("a day on, the requests' ETags: %q", got)
	}
	if l.note != "" || l.ix.Entry("plugin", "echo") == nil {
		t.Fatalf("after the 304: %+v", l)
	}
	if fetchRegistry(ctx, regNow.Add(26*time.Hour)); len(s.requests()) != 2 {
		t.Fatal("a 304 did not keep the list for another day")
	}
	s.set(testIndex(t, testEntry("plugin", "echo", "https://github.com/example/echo", regC1),
		testEntry("lua", "hello", "https://codeberg.org/example/hello")), `"v2"`, 0)
	l = fetchRegistry(ctx, regNow.Add(50*time.Hour))
	if l.note != "" || l.ix.Entry("lua", "hello") == nil {
		t.Fatalf("the new index: %+v", l)
	}
	if c, ok := readRegistryCache(); !ok || c.etag != `"v2"` || c.ix.Entry("lua", "hello") == nil {
		t.Fatalf("the cache: %+v %v", c, ok)
	}
}

// An index that does not validate, or is over a MiB, never replaces the list already read: it is
// kept, said to be offline, and the cache is untouched.
func TestABadIndexKeepsTheListAlreadyRead(t *testing.T) {
	good := testEntry("plugin", "echo", "https://github.com/example/echo", regC1)
	whole := testIndex(t, good)
	short := good
	short.Audits = []regindex.IndexAudit{good.Audits[0]}
	short.Audits[0].Commit = regC1[:7]
	big := good
	big.Description = strings.Repeat("x", httpMaxBody)
	for name, bad := range map[string][]byte{
		"malformed":  []byte("{"),
		"truncated":  whole[:len(whole)/2],
		"duplicate":  testIndex(t, good, good),
		"short SHA":  testIndex(t, short),
		"format":     []byte(strings.Replace(string(whole), `"format":1`, `"format":2`, 1)),
		"over a MiB": testIndex(t, big),
	} {
		s := serveRegistry(t, whole)
		fetchRegistry(context.Background(), regNow)
		s.set(bad, `"bad"`, 0)
		l := fetchRegistry(context.Background(), regNow.Add(25*time.Hour))
		if !strings.HasPrefix(l.note, "offline: the list as of 2026-10-19 (") || l.ix.Entry("plugin", "echo") == nil {
			t.Errorf("%s: %q, %d entries", name, l.note, len(l.ix.Entries))
		}
		if c, ok := readRegistryCache(); !ok || c.etag != `"v1"` {
			t.Errorf("%s: the cache was replaced: %q %v", name, c.etag, ok)
		}
	}
}

// With no registry and no cache, the list is the built-in KB types, said as such; a 304 to a request
// that named no ETag is no answer.
func TestNoRegistryIsTheBuiltinKBTypes(t *testing.T) {
	s := serveRegistry(t, nil)
	for status, why := range map[int]string{500: "500 Internal Server Error", 304: "304 Not Modified"} {
		s.set(nil, `"v1"`, status)
		l := fetchRegistry(context.Background(), regNow)
		if l.note != "offline: the built-in KB types only (the registry answered "+why+")" {
			t.Errorf("%d: %q", status, l.note)
		}
		if len(l.ix.Entries) != len(builtinKBTypes) || l.ix.Entries[0].Kind != regindex.KindKBType {
			t.Errorf("%d: %+v", status, l.ix.Entries)
		}
	}
	if got := s.requests(); len(got) != 2 || got[0] != "" || got[1] != "" {
		t.Errorf("the requests' ETags: %q", got)
	}
}

// A read that panicked (its answer's err, failed in crash.go) is listed as the built-in KB types,
// said as such: a list never shows a zero index as the registry.
func TestAFailedRegistryReadIsTheBuiltins(t *testing.T) {
	l := registryList{err: errors.New("boom\nstack")}.usable()
	if l.note != "offline: the built-in KB types only (boom)" || len(l.ix.Entries) != len(builtinKBTypes) {
		t.Errorf("%q, %d entries", l.note, len(l.ix.Entries))
	}
	ok := registryList{note: "x"}
	if got := ok.usable(); got.note != "x" {
		t.Errorf("a read that did not fail changed: %+v", got)
	}
	if l, ok := failed[registryList](errPanicked{value: "boom"}); !ok || l.err == nil {
		t.Errorf("a panicked read does not carry its failure: %+v %v", l, ok)
	}
}

// The card's line compares commits in full: a prefix of the audited commit, however long, is not it.
func TestTheAuditLineComparesFullCommits(t *testing.T) {
	e := testEntry("plugin", "echo", "https://github.com/example/echo", regC2, regC1)
	never := testEntry("plugin", "echo", "https://github.com/example/echo")
	notAt := "not audited at this commit (last audited: 2222222, 2026-10-10)"
	for _, c := range []struct {
		e      *regindex.IndexEntry
		commit string
		want   string
	}{
		{nil, regC2, ""},
		{&e, regC2, "✓ audited by claude-opus-5-5 on 2026-10-10 at this commit"},
		{&e, regC1, "✓ audited by claude-opus-5-5 on 2026-10-09 at this commit"},
		{&e, regC2[:7], notAt},
		{&e, regC2[:39], notAt},
		{&e, regC3, notAt},
		{&never, regC2, "never audited"},
	} {
		if got := auditLine(c.e, c.commit); got != c.want {
			t.Errorf("%.12s: %q, want %q", c.commit, got, c.want)
		}
	}
}

// Each install card says the audit line above its risk sentence, and the plugin's risk sentence is
// word for word what it was before the registry (ADR 1792351195 §4.7).
func TestTheInstallCardsSayTheAuditAboveTheRisk(t *testing.T) {
	const risk = "A plugin is a program that runs as you, with your files. AutoDoc does " +
		"not check what a plugin does: add only plugins you trust. Adding one is at your own risk."
	if pluginRisk != risk {
		t.Fatalf("the plugin's risk sentence changed:\n%s", pluginRisk)
	}
	e := testEntry("plugin", "echo", "https://github.com/example/echo", regC2)
	c := pluginChange{m: manifest{Name: "echo", Command: []string{"x"}}, url: e.Repo, commit: regC2, entry: &e}
	q := pluginQuestion(c, "Echo")
	line := "✓ audited by claude-opus-5-5 on 2026-10-10 at this commit"
	if !strings.Contains(q, "at    2222222\n") || !strings.Contains(q, line+"\n\n"+pluginRisk) {
		t.Errorf("the audited card:\n%s", q)
	}
	c.entry = nil
	if q := pluginQuestion(c, "Echo"); strings.Contains(q, "audited") || !strings.Contains(q, "\n\n"+pluginRisk) {
		t.Errorf("a card for a repo the registry does not list:\n%s", q)
	}
}

// Add from a git URL… takes a registry name: the entry's repo is cloned, and its card says whether
// the commit cloned is the one audited. A name the registry does not list is not added.
func TestAPluginIsAddedByItsRegistryName(t *testing.T) {
	repo := pluginRepo(t, "Echo", "true")
	full := runGit(t, repo, "rev-parse", "HEAD")
	url := "https://github.com/example/echo"
	insteadOf(t, repo, url)
	serveRegistry(t, testIndex(t, testEntry("plugin", "echo", url, full)))
	r, _ := pluginTUI(t, t.TempDir())

	r.h.p.Post(func() { r.h.plugins.addPlugin("echo") })
	q := r.question(t)
	want := "✓ audited by claude-opus-5-5 on 2026-10-10 at this commit\n\n" + pluginRisk
	if !strings.Contains(q, "from  "+url+"\n") || !strings.Contains(q, "at    "+full[:7]+"\n") || !strings.Contains(q, want) {
		t.Errorf("the card:\n%s", q)
	}
	r.keys(t, key('n'))
	r.waitNotice(t, "echo not added")

	r.h.p.Post(func() { r.h.plugins.addPlugin("nope") })
	r.waitNotice(t, "not added: no plugin named nope in the registry")
}

// :lua-install takes a registry name as Add from a git URL… does, its card saying the package was
// never audited; a name the registry does not list is not installed.
func TestALuaPackageIsInstalledByItsRegistryName(t *testing.T) {
	repo := t.TempDir()
	writeFiles(t, repo, map[string]string{
		"package.toml": "name = \"hello\"\nversion = \"0.1.0\"\ndescription = \"says hello\"\n",
		"init.lua":     `autodoc.command{ name = "hello-pkg", run = function(ed) ed:insert("hello") end }`,
	})
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "hello")
	url := "https://codeberg.org/example/hello"
	insteadOf(t, repo, url)
	serveRegistry(t, testIndex(t, testEntry("lua", "hello", url)))
	dir := t.TempDir()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{LuaDir: dir})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.waitNamespaces(t)

	r.mustRun(t, "lua-install hello")
	r.s.WaitForText(t, "install the Lua package hello?")
	text := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.askText"); return v.Raw })
	if !strings.Contains(text, "from "+url+" at ") || !strings.Contains(text, "never audited\n\n"+packageRisk) {
		t.Errorf("the card:\n%s", text)
	}
	onLoop(r, func() bool { r.h.asker.rejected(); return true })

	r.clearNotices()
	r.mustRun(t, "lua-install nope")
	r.waitNotice(t, "lua-install: not installed: no Lua package named nope in the registry")
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("lua/ holds %d entries", len(entries))
	}
}

// The add dialog's KB type field lists the registry's KB types, and a pick stages the entry's repo.
func TestTheKBTypeFieldListsTheRegistrysTypes(t *testing.T) {
	team := testEntry("kb-type", "team.kb", "https://github.com/example/team.kb")
	serveRegistry(t, testIndex(t, testEntry("plugin", "echo", "https://github.com/example/echo"), team))
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitFor(t, "the workspaces", func(string) bool { return len(r.listed()) > 0 })
	staged := make(chan string, 1)
	r.h.kbStage = func(_ context.Context, url string) stagedKBType {
		staged <- url
		return stagedKBType{url: url, err: os.ErrNotExist}
	}
	r.openAdd(t)
	r.s.WaitFor(t, "the registry read", func(string) bool { return onLoop(r, func() bool { return r.h.registryNow != nil }) })
	if got := onLoop(r, func() []kbTypeChoice { return r.h.kbTypeChoices() }); len(got) != 1 || got[0].name != "team.kb" || got[0].url != team.Repo {
		t.Fatalf("the KB types: %+v", got)
	}
	r.h.p.Post(func() { r.h.kbTypePicked(1) })
	select {
	case url := <-staged:
		if url != team.Repo {
			t.Fatalf("staged %s, want %s", url, team.Repo)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the pick staged nothing")
	}
}

// Plugins › Browse the registry… lists the plugins and packages, not the KB types, previews each
// with its audits as history, and Install asks as an add by URL does.
func TestBrowsingTheRegistryListsPluginsAndPackages(t *testing.T) {
	repo := pluginRepo(t, "Echo", "true")
	full := runGit(t, repo, "rev-parse", "HEAD")
	url := "https://github.com/example/echo"
	insteadOf(t, repo, url)
	serveRegistry(t, testIndex(t, testEntry("plugin", "echo", url, regC1),
		testEntry("lua", "hello", "https://codeberg.org/example/hello"),
		testEntry("kb-type", "team.kb", "https://github.com/example/team.kb")))
	r, _ := pluginTUI(t, t.TempDir())
	b := onLoop(r, func() *registryBrowse {
		for _, f := range r.h.reg.features {
			if b, ok := f.(*registryBrowse); ok {
				return b
			}
		}
		return nil
	})
	if b == nil {
		t.Fatal("no registry feature")
	}
	r.h.p.Post(b.browse)
	r.s.WaitForText(t, "the registry: plugins and Lua packages")
	source := func(name string) string {
		return onLoop(r, func() string { v, _ := r.h.p.Tree().Source(name); return v.Raw })
	}
	r.s.WaitFor(t, "the preview", func(string) bool { return strings.Contains(source("App.registryPreviewText"), "### Audits") })
	if got := source("App.registryList"); got != "plugins and packages (2 of 2)" {
		t.Errorf("the list's title: %q", got)
	}
	if p := source("App.registryPreviewText"); !strings.Contains(p, "- audited by claude-opus-5-5, 2026-10-10, at 1111111") {
		t.Errorf("the preview:\n%s", p)
	}
	r.h.p.Post(func() { b.pick.accept(0, "") })
	q := r.question(t)
	if !strings.Contains(q, "not audited at this commit (last audited: 1111111, 2026-10-10)\n\n"+pluginRisk) || !strings.Contains(q, full[:7]) {
		t.Errorf("the card:\n%s", q)
	}
	r.keys(t, key('n'))
	r.waitNotice(t, "echo not added")
}

// insteadOf has git clone repo wherever it is asked for url: a registry entry's repo is on a host,
// and the test's is a folder.
func insteadOf(t *testing.T, repo, url string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+filepath.ToSlash(repo)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", url)
}
