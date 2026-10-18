package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --check-kbtype reads a KB type as AutoDoc stages one and lists its folders; a type that breaks a
// rule exits 1 naming it; nothing in the type runs.
func TestCheckKBType(t *testing.T) {
	dir := t.TempDir()
	for rel, text := range map[string]string{
		"kbtype.toml":            "format = 1\nname = \"para.kb\"\ntitle = \"PARA\"\n\n[[folder]]\npath = \"projects\"\npurpose = \"goals with a deadline\"\n\n[[folder]]\npath = \"raw\"\npurpose = \"sources\"\nimmutable = true\n",
		"tree/AGENTS.md":         "# agents\n",
		"tree/ABOUT.md":          "# about\n",
		"tree/projects/ABOUT.md": "# projects/\n",
		"tree/raw/ABOUT.md":      "# raw/\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out := runMain(t, nil, "--check-kbtype", dir)
	for _, want := range []string{"para.kb (PARA): 2 folders", "projects/  goals with a deadline", "raw/  sources  (immutable)"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("a valid type: exit %d, %q; want exit 0 saying %q", code, out, want)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "tree", "KB_OPERATIONS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runMain(t, nil, "--check-kbtype", dir); code != 1 || !strings.Contains(out, "AutoDoc's own") {
		t.Errorf("a type holding KB_OPERATIONS.md: exit %d, %q; want exit 1 naming the rule", code, out)
	}
	if code, out := runMain(t, nil, "--check-kbtype", t.TempDir()); code != 1 || !strings.Contains(out, "no kbtype.toml") {
		t.Errorf("an empty folder: exit %d, %q", code, out)
	}
}
