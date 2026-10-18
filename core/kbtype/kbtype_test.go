package kbtype

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const manifest = `format = 1
name = "kb-zettelkasten"
title = "Zettelkasten"
description = "linked notes"
autodoc = ">=0.2.0"

[workspace]
include = ["**/*.md"]
embedding_policy = "always"
schema = "_schema/frontmatter.yaml"

[[folder]]
path = "permanent"
purpose = "one idea per note"

[[folder]]
path = "raw"
purpose = "sources as received"
immutable = true

[[folder]]
path = "_schema"
purpose = "the frontmatter types"
`

// files are a valid type's tree/.
func files() map[string]string {
	return map[string]string{
		"AGENTS.md":                "---\ncreated: {{autodoc.date}}\n---\n# Agents of {{kb_type.name}}\n",
		"ABOUT.md":                 "---\nkb_type: \"{{kb_type.name}}\"\nkb_type_url: \"{{kb_type.url}}\"\nkb_type_commit: \"{{kb_type.commit}}\"\n---\n",
		"permanent/ABOUT.md":       "# permanent/\n",
		"permanent/a-note.md":      "---\ncreated: {{date}}\ntitle: \"{{title}}\"\n---\n",
		"raw/ABOUT.md":             "# raw/\n",
		"_schema/_ABOUT.md":        "# _schema/\n",
		"_schema/frontmatter.yaml": "version: 2\n",
	}
}

// typeDir writes a type repo with mf as its kbtype.toml and fs as its tree/.
func typeDir(t *testing.T, mf string, fs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Manifest), []byte(mf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, Tree), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, text := range fs {
		p := filepath.Join(dir, Tree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadAValidType(t *testing.T) {
	ty, err := Read(typeDir(t, manifest, files()))
	if err != nil {
		t.Fatal(err)
	}
	if ty.Name != "kb-zettelkasten" || len(ty.Folders) != 3 || len(ty.Files) != len(files()) {
		t.Fatalf("read %q with %d folders and %d files", ty.Name, len(ty.Folders), len(ty.Files))
	}
	if ty.Workspace.Schema != "_schema/frontmatter.yaml" || ty.Workspace.EmbeddingPolicy != "always" {
		t.Fatalf("settings %+v", ty.Workspace)
	}
}

// Each rule refused names itself, and wraps ErrInvalid.
func TestReadRefusesEachRule(t *testing.T) {
	with := func(change func(map[string]string)) map[string]string {
		fs := files()
		change(fs)
		return fs
	}
	cases := []struct {
		name, mf, want string
		fs             map[string]string
	}{
		{"unlisted folder", manifest, `folder "drafts" is not listed`, with(func(fs map[string]string) { fs["drafts/x.md"] = "x" })},
		{"missing descriptor", manifest, "permanent/ABOUT.md is missing", with(func(fs map[string]string) { delete(fs, "permanent/ABOUT.md") })},
		{"underscore descriptor", manifest, "_schema/_ABOUT.md is missing", with(func(fs map[string]string) { delete(fs, "_schema/_ABOUT.md"); fs["_schema/ABOUT.md"] = "x" })},
		{"operations in tree", manifest, "that file is AutoDoc's own", with(func(fs map[string]string) { fs[Operations] = "x" })},
		{"no AGENTS.md", manifest, "AGENTS.md is missing", with(func(fs map[string]string) { delete(fs, "AGENTS.md") })},
		{"immutable with content", manifest, `folder "raw" is immutable`, with(func(fs map[string]string) { fs["raw/source.txt"] = "x" })},
		{"not text", manifest, "is not UTF-8 text", with(func(fs map[string]string) { fs["permanent/b.md"] = "a\x00b" })},
		{"over a file's size", manifest, "is over", with(func(fs map[string]string) { fs["permanent/big.md"] = strings.Repeat("a", MaxFileSize+1) })},
		{"schema not a file", strings.Replace(manifest, `schema = "_schema/frontmatter.yaml"`, `schema = "_schema/none.yaml"`, 1), "is not a file of tree/", files()},
		{"folder escapes", strings.Replace(manifest, `path = "permanent"`, `path = "../permanent"`, 1), "not a clean relative path", files()},
		{"folder twice", manifest + "\n[[folder]]\npath = \"raw\"\npurpose = \"again\"\n", "listed twice", files()},
		{"unknown key", strings.Replace(manifest, "format = 1", "format = 1\nbuild = [\"make\"]", 1), "unknown key build", files()},
		{"another format", strings.Replace(manifest, "format = 1", "format = 2", 1), "format 2", files()},
		{"bad name", strings.Replace(manifest, `name = "kb-zettelkasten"`, `name = "KB Zettel"`, 1), "lowercase letters", files()},
		{"bad policy", strings.Replace(manifest, `embedding_policy = "always"`, `embedding_policy = "sometimes"`, 1), "embedding_policy", files()},
		{"manifest over its limit", manifest + "#" + strings.Repeat("a", MaxManifestSize) + "\n", "is over", files()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Read(typeDir(t, c.mf, c.fs))
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Read = %v; want ErrInvalid naming %q", err, c.want)
			}
		})
	}
}

// A symlink in tree/ is refused: copying it could read outside the repo.
func TestReadRefusesASymlink(t *testing.T) {
	dir := typeDir(t, manifest, files())
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, Tree, "permanent", "link.md")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, err := Read(dir); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Read = %v; want the symlink refused", err)
	}
}

// A manifest at exactly its limit is read; one byte over is refused, never read as a valid prefix.
func TestTheManifestLimitIsExact(t *testing.T) {
	pad := MaxManifestSize - len(manifest) - 2
	at := manifest + "#" + strings.Repeat("a", pad) + "\n"
	if len(at) != MaxManifestSize {
		t.Fatalf("the fixture is %d bytes", len(at))
	}
	if _, err := Read(typeDir(t, at, files())); err != nil {
		t.Fatalf("a manifest of exactly %d bytes: %v", MaxManifestSize, err)
	}
	over := manifest + "#" + strings.Repeat("a", pad+1) + "\n"
	if _, err := Read(typeDir(t, over, files())); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a manifest one byte over: %v", err)
	}
}

func TestReadNeedsBothParts(t *testing.T) {
	dir := t.TempDir()
	if _, err := Read(dir); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "no kbtype.toml") {
		t.Fatalf("no manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, Manifest), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "no tree/") {
		t.Fatalf("no tree: %v", err)
	}
}

func TestPlan(t *testing.T) {
	ty, err := Read(typeDir(t, manifest, files()))
	if err != nil {
		t.Fatal(err)
	}
	v := Vars{Date: "2026-10-19", AutoDocVersion: "v0.2.0", URL: "https://example.com/kb", Commit: "0123456789abcdef0123456789abcdef01234567"}
	ops := []byte("---\ncreated: {{date}}\nautodoc_version: {{autodoc_version}}\n---\nuse {{date}} in a template\n")
	plan := ty.Plan(v, ops)
	if len(plan) != len(files())+1 {
		t.Fatalf("%d writes", len(plan))
	}
	byRel := map[string]Write{}
	pos := map[string]int{}
	for i, w := range plan {
		byRel[w.Rel], pos[w.Rel] = w, i
	}
	if pos["permanent/ABOUT.md"] > pos["permanent/a-note.md"] || pos["_schema/_ABOUT.md"] > pos["_schema/frontmatter.yaml"] {
		t.Fatalf("a folder's descriptor is not first in it: %v", pos)
	}
	if last := plan[len(plan)-1]; last.Rel != Operations || !last.Managed {
		t.Fatalf("the last write is %+v; want the managed %s", last, Operations)
	}
	ops2 := string(byRel[Operations].Text)
	if !strings.Contains(ops2, "created: 2026-10-19\n") || !strings.Contains(ops2, "autodoc_version: v0.2.0") || !strings.Contains(ops2, "use {{date}} in a template") {
		t.Fatalf("KB_OPERATIONS.md rendered as %q", ops2)
	}
	about := string(byRel["ABOUT.md"].Text)
	for _, want := range []string{`kb_type: "kb-zettelkasten"`, `kb_type_url: "https://example.com/kb"`, `kb_type_commit: "0123456789abcdef0123456789abcdef01234567"`} {
		if !strings.Contains(about, want) {
			t.Errorf("the root ABOUT.md lacks %s: %q", want, about)
		}
	}
	if got := string(byRel["AGENTS.md"].Text); !strings.Contains(got, "created: 2026-10-19") || !strings.Contains(got, "Agents of kb-zettelkasten") {
		t.Errorf("AGENTS.md rendered as %q", got)
	}
	// a template's Obsidian variables pass through
	if got := string(byRel["permanent/a-note.md"].Text); !strings.Contains(got, "created: {{date}}") || !strings.Contains(got, `"{{title}}"`) {
		t.Errorf("an Obsidian variable was rendered: %q", got)
	}
	if byRel["raw/ABOUT.md"].Immutable != "raw" || byRel["permanent/ABOUT.md"].Immutable != "" {
		t.Errorf("immutable marks: raw %q, permanent %q", byRel["raw/ABOUT.md"].Immutable, byRel["permanent/ABOUT.md"].Immutable)
	}
	if got := ty.Plan(v, nil); len(got) != len(files()) {
		t.Errorf("a plan without operations has %d writes", len(got))
	}
}

func TestDeclaredVersionAndOlder(t *testing.T) {
	for text, want := range map[string]string{
		"---\ntype: kb\nautodoc_version: 0.1.20\n---\n":   "0.1.20",
		"---\nautodoc_version: \"v0.2.0\"\nrevision: 3\n": "v0.2.0",
		"# autodoc_version: 0.1.9\nversion: 2\n":          "0.1.9",
		"---\ntype: kb\n---\n":                            "",
	} {
		if got := DeclaredVersion([]byte(text)); got != want {
			t.Errorf("DeclaredVersion(%q) = %q, want %q", text, got, want)
		}
	}
	for _, c := range []struct {
		have, want string
		older, ok  bool
	}{
		{"0.1.20", "v0.2.0", true, true},
		{"v0.2.0", "0.2.0", false, true},
		{"0.10.0", "0.9.9", false, true},
		{"0.2.0", "0.2.1-dev", true, true},
		{"", "0.2.0", false, false},
		{"latest", "0.2.0", false, false},
		{"0.2.0", "dev", false, false},
	} {
		if older, ok := Older(c.have, c.want); older != c.older || ok != c.ok {
			t.Errorf("Older(%q, %q) = %v, %v; want %v, %v", c.have, c.want, older, ok, c.older, c.ok)
		}
	}
}

func TestDescriptor(t *testing.T) {
	for d, want := range map[string]string{"raw": "raw/ABOUT.md", "_templates": "_templates/_ABOUT.md", "projects/_x": "projects/_x/_ABOUT.md"} {
		if got := Descriptor(d); got != want {
			t.Errorf("Descriptor(%q) = %q, want %q", d, got, want)
		}
	}
}
