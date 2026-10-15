package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/themes"
)

func TestThemeAuthoringNeedsNoDaemon(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "not-created.toml")
	file := filepath.Join(dir, "themes", "warm_paper.qml")
	code, out := runMain(t, nil, "--config", config, "--new-theme", "warm_paper", "--from", "sepia")
	if code != 0 || !strings.Contains(out, file) {
		t.Fatalf("starter: %d %s", code, out)
	}
	if code, out = runMain(t, nil, "--check-theme", file); code != 0 || !strings.Contains(out, "valid Theme") {
		t.Fatalf("checker: %d %s", code, out)
	}
	if code, out = runMain(t, nil, "--config", config, "--new-theme", "warm_paper"); code == 0 {
		t.Fatalf("overwrote existing theme: %s", out)
	}
	if code, out = runMain(t, nil, "--config", config, "--new-theme", "dark"); code == 0 || !strings.Contains(out, "cannot be shadowed") {
		t.Fatalf("shadow: %d %s", code, out)
	}
	if code, out = runMain(t, nil, "--theme-reference"); code != 0 {
		t.Fatalf("reference: %d %s", code, out)
	}
	for _, role := range themes.Schema() {
		if !strings.Contains(out, "`"+role.Key+"`") {
			t.Errorf("reference missing %s", role.Key)
		}
	}
	if err := os.WriteFile(file, []byte(`Theme { app { window: "bad" } }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out = runMain(t, nil, "--check-theme", file); code == 0 || !strings.Contains(out, "app.window") || !strings.Contains(out, "app.backdrop") || !strings.Contains(out, file+":") {
		t.Fatalf("bad checker: %d %s", code, out)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatalf("headless commands loaded/created config: %v", err)
	}
}
