package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/kbtype"
	"github.com/yongjohnlee80/autodoc/core/store"
)

const testKBManifest = `format = 1
name = "kb-zettelkasten"
title = "Zettelkasten"
description = "linked notes"

[workspace]
include = ["**/*.md"]
embedding_policy = "never"
schema = "_schema/frontmatter.yaml"

[[folder]]
path = "permanent"
purpose = "one idea per note"

[[folder]]
path = "_schema"
purpose = "the frontmatter types"
`

// testKBType writes a valid KB type repo, with extra files under permanent/, and reads it.
func testKBType(t *testing.T, extra int) *kbtype.Type {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"kbtype.toml":                   testKBManifest,
		"tree/AGENTS.md":                "# Agents of {{kb_type.name}}\n",
		"tree/ABOUT.md":                 "---\nkb_type_commit: \"{{kb_type.commit}}\"\n---\n",
		"tree/permanent/ABOUT.md":       "# permanent/\n",
		"tree/_schema/_ABOUT.md":        "# _schema/\n",
		"tree/_schema/frontmatter.yaml": "version: 2\nstrict: false\ndiscriminator: type\ncommon:\n  type: {type: string}\n",
	}
	for i := range extra {
		files[fmt.Sprintf("tree/permanent/note-%03d.md", i)] = "a note\n"
	}
	for rel, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ty, err := kbtype.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ty
}

const testKBCommit = "0123456789abcdef0123456789abcdef01234567"

// stagedAs is a stager answering ty for every URL, each in a stage directory of its own.
func stagedAs(t *testing.T, ty *kbtype.Type) func(context.Context, string) stagedKBType {
	return func(_ context.Context, url string) stagedKBType {
		stage, err := os.MkdirTemp(t.TempDir(), "stage-")
		if err != nil {
			return stagedKBType{url: url, err: err}
		}
		return stagedKBType{url: url, stage: stage, commit: testKBCommit, t: ty}
	}
}

// openAdd opens the add dialog and waits for it.
func (r *running) openAdd(t *testing.T) {
	t.Helper()
	r.h.p.Post(r.h.startAddWorkspace)
	r.s.WaitForText(t, "add a workspace")
}

// pickKB picks row i of the KB type field and waits until a type is staged.
func (r *running) pickKB(t *testing.T, i int) {
	t.Helper()
	r.h.p.Post(func() { r.h.kbTypePicked(i) })
	r.s.WaitFor(t, "the KB type staged", func(string) bool { return onLoop(r, func() bool { return r.h.kbStaged != nil }) })
}

// An add with a KB type creates the type's files and AutoDoc's KB_OPERATIONS.md under the new
// root, then saves the type's proposed settings where the dialog's fields were left as they opened.
func TestAKBTypeIsAppliedOnAdd(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitFor(t, "the workspaces", func(string) bool { return len(r.listed()) > 0 })
	r.h.kbStage = stagedAs(t, testKBType(t, 0))
	root := t.TempDir()
	r.openAdd(t)
	r.pickKB(t, 2) // Zettelkasten
	r.s.WaitForText(t, "permanent/")
	r.saveForm(func(f *settingsForm) { f.name, f.root = "zk", root })
	r.s.WaitForText(t, "added workspace zk as Zettelkasten")
	for _, rel := range []string{"AGENTS.md", "ABOUT.md", "permanent/ABOUT.md", "_schema/_ABOUT.md", "_schema/frontmatter.yaml", "KB_OPERATIONS.md"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("%s was not created: %v", rel, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "ABOUT.md")); !strings.Contains(string(b), testKBCommit) {
		t.Errorf("the root ABOUT.md does not record the commit: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "KB_OPERATIONS.md")); strings.Contains(string(b), "{{autodoc_version}}") {
		t.Error("KB_OPERATIONS.md was not rendered")
	}
	ws, err := d.db.Workspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var zk *store.WorkspaceInfo
	for i := range ws {
		if ws[i].Name == "zk" {
			zk = &ws[i]
		}
	}
	if zk == nil {
		t.Fatal("zk was not stored")
	}
	if !slices.Equal(zk.Include, []string{"**/*.md"}) || zk.EmbeddingPolicy != store.EmbeddingNever {
		t.Errorf("the type's settings were not saved: include %v, embedding %q", zk.Include, zk.EmbeddingPolicy)
	}
	if onLoop(r, func() bool { return r.h.kbStaged != nil }) {
		t.Error("the stage was kept after the add")
	}
}

// A field the user changed keeps their value; the type's applies only to a field left as opened.
func TestAKBTypesSettingsGiveWayToTheUsers(t *testing.T) {
	b := baseFor(kbInfo())
	b.adding = true
	ty := testKBType(t, 0)
	f := b.form()
	got := b.withKBType(f, ty)
	if got.include != "**/*.md" || got.schema != "_schema/frontmatter.yaml" || policyChoices[got.policy] != store.EmbeddingNever {
		t.Errorf("untouched fields: include %q, schema %q, policy %q", got.include, got.schema, policyChoices[got.policy])
	}
	f.include, f.schema = "notes/**/*.md", "mine.yaml"
	got = b.withKBType(f, ty)
	if got.include != "notes/**/*.md" || got.schema != "mine.yaml" {
		t.Errorf("the user's fields were replaced: include %q, schema %q", got.include, got.schema)
	}
}

// A slow pick that finishes after a later one never lands: the later is staged, the slow one's
// stage is removed unseen. None after a pending pick likewise leaves nothing staged.
func TestAStaleKBTypePickNeverLands(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitFor(t, "the workspaces", func(string) bool { return len(r.listed()) > 0 })
	ty := testKBType(t, 0)
	release := map[string]chan struct{}{}
	stages := map[string]string{}
	for _, c := range builtinKBTypes {
		release[c.url] = make(chan struct{})
		stages[c.url] = filepath.Join(t.TempDir(), c.name)
		if err := os.MkdirAll(stages[c.url], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.h.kbStage = func(ctx context.Context, url string) stagedKBType {
		select {
		case <-release[url]:
		case <-ctx.Done():
		}
		return stagedKBType{url: url, stage: stages[url], commit: testKBCommit, t: ty}
	}
	a, b := builtinKBTypes[0].url, builtinKBTypes[1].url
	r.openAdd(t)
	r.h.p.Post(func() { r.h.kbTypePicked(1) }) // A, held
	r.h.p.Post(func() { r.h.kbTypePicked(2) }) // B
	close(release[b])
	r.s.WaitFor(t, "B staged", func(string) bool {
		return onLoop(r, func() bool { return r.h.kbStaged != nil && r.h.kbStaged.url == b })
	})
	close(release[a])
	r.s.WaitFor(t, "A's stage removed", func(string) bool { _, err := os.Stat(stages[a]); return os.IsNotExist(err) })
	if got := onLoop(r, func() string { return r.h.kbStaged.url }); got != b {
		t.Fatalf("the staged type is %s, want %s", got, b)
	}
	// None while C is still staging: nothing staged, C removed unseen
	c := builtinKBTypes[2].url
	r.h.p.Post(func() { r.h.kbTypePicked(3) })
	r.h.p.Post(func() { r.h.kbTypePicked(0) })
	close(release[c])
	r.s.WaitFor(t, "C's stage removed", func(string) bool { _, err := os.Stat(stages[c]); return os.IsNotExist(err) })
	if onLoop(r, func() bool { return r.h.kbStaged != nil }) {
		t.Fatal("a type is staged after None")
	}
	if _, err := os.Stat(stages[b]); !os.IsNotExist(err) {
		t.Fatal("B's stage was kept after another pick")
	}
}

// A type whose files stop part way reopens the dialog on the workspace it added, saying what
// landed, with no second add: the workspace exists, and a refused file is named.
func TestAPartialKBTypeReopensOnTheAddedWorkspace(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitFor(t, "the workspaces", func(string) bool { return len(r.listed()) > 0 })
	r.h.kbStage = stagedAs(t, testKBType(t, 0))
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "permanent")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	r.openAdd(t)
	r.pickKB(t, 2)
	r.saveForm(func(f *settingsForm) { f.name, f.root = "zk", root })
	r.s.WaitForText(t, "workspace settings · zk")
	r.s.WaitForText(t, "stopped part way")
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a file was created outside the root: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal("the files before the refused one did not land")
	}
	if ws, _ := d.db.Workspaces(context.Background()); len(ws) != 2 {
		t.Fatalf("%d workspaces stored; want kb and zk", len(ws))
	}
}

// A plan longer than one call is sent in batches, and all of it lands.
func TestAKBTypeIsSentInBatches(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitFor(t, "the workspaces", func(string) bool { return len(r.listed()) > 0 })
	r.h.kbStage = stagedAs(t, testKBType(t, 70))
	root := t.TempDir()
	r.openAdd(t)
	r.pickKB(t, 2)
	r.saveForm(func(f *settingsForm) { f.name, f.root = "zk", root })
	r.s.WaitForText(t, "added workspace zk as Zettelkasten")
	entries, err := os.ReadDir(filepath.Join(root, "permanent"))
	if err != nil || len(entries) != 71 {
		t.Fatalf("permanent/ holds %d files, want 71: %v", len(entries), err)
	}
}

// A KB type is staged over https:// only: git's other transports (ext::, file://, a local path)
// are refused before git runs, so nothing in a type's URL can run a command.
func TestAKBTypeIsClonedOverHTTPSOnly(t *testing.T) {
	for _, url := range []string{"ext::sh -c touch% /tmp/pwned", "file:///tmp/a-type", "/tmp/a-type", "git@github.com:x/y.git", "http://example.com/x"} {
		s := stageKBType(context.Background(), url)
		if s.err == nil || !strings.Contains(s.err.Error(), "https://") || s.stage != "" {
			t.Errorf("%q: %v, stage %q; want refused before any clone", url, s.err, s.stage)
		}
	}
}
