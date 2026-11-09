package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
)

// autodocDirsOf is a workspace answer's autodoc_dirs as "dir relation path" lines.
func autodocDirsOf(w map[string]any) []string {
	var out []string
	l, _ := w["autodoc_dirs"].([]any)
	for _, e := range l {
		m, _ := e.(map[string]any)
		out = append(out, fmt.Sprintf("%v %v %v", m["dir"], m["relation"], m["path"]))
	}
	return out
}

// A workspace whose root holds AutoDoc's own folders, or lies inside one, is still added and
// served, its answer naming each folder and warning what a gated script could reach there;
// workspace.list names them too. A root that touches none has neither.
// --check-workspaces reports such workspaces: exit 1 with one, 0 with none, 2 with no daemon.
func TestARootHoldingAutoDocsFoldersIsAddedWithAWarningAndReported(t *testing.T) {
	dir := short(t)
	state, sock := filepath.Join(dir, "state"), ipc.ScratchAddr(dir, "s.sock")
	cfg := writeConfig(t, sock, state, "")
	d := start(t, cfg, sock)
	cli := dial(t, sock)
	canonState, err := filepath.EvalSymlinks(state)
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(state, "notes")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}

	added := call(t, cli, "workspace.add", "home", dir).(map[string]any)
	want := []string{"state holds " + canonState, "data holds " + canonState}
	if added["state"] != "ready" || fmt.Sprint(autodocDirsOf(added)) != fmt.Sprint(want) {
		t.Errorf("workspace.add of a root holding the state and data folders: %+v, want ready with %v", added, want)
	}
	warning, _ := added["warning"].(string)
	for _, s := range []string{"state", "data", canonState, "protected"} {
		if !strings.Contains(warning, s) {
			t.Errorf("the warning lacks %q: %q", s, warning)
		}
	}
	in := call(t, cli, "workspace.add", "inner", inside).(map[string]any)
	if got, want := autodocDirsOf(in), []string{"state inside " + canonState, "data inside " + canonState}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("workspace.add of a root inside the state folder: %v, want %v", got, want)
	}
	clean := call(t, cli, "workspace.add", "clean", t.TempDir()).(map[string]any)
	if _, ok := clean["autodoc_dirs"]; ok || clean["warning"] != nil {
		t.Errorf("a root touching none of them: %+v, want no autodoc_dirs and no warning", clean)
	}
	listed := map[string][]string{}
	for _, w := range call(t, cli, "workspace.list").([]any) {
		m := w.(map[string]any)
		listed[m["name"].(string)] = autodocDirsOf(m)
		if m["warning"] != nil {
			t.Errorf("workspace.list's %v carries a warning: the add's alone does", m["name"])
		}
	}
	if fmt.Sprint(listed["home"]) != fmt.Sprint(want) || len(listed["inner"]) != 2 || len(listed["clean"]) != 0 {
		t.Errorf("workspace.list: %v", listed)
	}

	// the report is stdout's alone; stderr says why the exit is what it is
	check := func() (int, string, string) {
		var out, errs bytes.Buffer
		code := run(context.Background(), []string{"--config", cfg, "--check-workspaces"}, Options{Version: "v-test"}, &out, &errs)
		return code, out.String(), errs.String()
	}
	code, out, errs := check()
	var report struct {
		OK         bool `json:"ok"`
		Workspaces []struct {
			Name        string           `json:"name"`
			AutodocDirs []map[string]any `json:"autodoc_dirs"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal([]byte(out), &report); code != 1 || err != nil || report.OK || len(report.Workspaces) != 2 {
		t.Fatalf("--check-workspaces with two flagged: exit %d, %q (%v; stderr %q); want 1 and both", code, out, err, errs)
	}
	call(t, cli, "workspace.remove", "home")
	call(t, cli, "workspace.remove", "inner")
	if code, out, errs := check(); code != 0 || !strings.Contains(out, `"ok": true`) {
		t.Errorf("--check-workspaces with none flagged: exit %d, %q (stderr %q); want 0", code, out, errs)
	}
	d.stop()
	if code, out, errs := check(); code != 2 {
		t.Errorf("--check-workspaces with no daemon: exit %d, %q (stderr %q); want 2", code, out, errs)
	}
}
