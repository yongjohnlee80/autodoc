package crew_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// fakeClaude writes a "Claude Code" at dir/name that names each of events as the real binary does,
// a quoted literal among other bytes.
func fakeClaude(t *testing.T, dir, name string, events ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("\x7fELF padding SessionStartish PermissionRequested ")
	for _, e := range events {
		b.WriteString(`x="` + e + `";`)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

var everyClaudeEvent = []string{"SessionStart", "crossSessionInbound", "PermissionRequest", "Notification",
	"PostToolUse", "PostToolUseFailure", "UserPromptSubmit", "Stop"}

// TestClaudeSettingsNameOnlyTheEventsItKnows (docs/decisions/crew.md#wake-acceptance-not-tool-approvals):
// a crew Claude agent's settings name only the hook events its Claude Code's binary knows, since an
// unknown one stops the session at a warning: one that knows every prompt hook gets them all, with
// prompts; one lacking PermissionRequest, or any other event prompts can't be reported without,
// gets SessionStart alone, without prompts; one lacking only Notification or PostToolUseFailure
// gets the rest, with prompts. A bare word that merely contains an event's name doesn't count.
func TestClaudeSettingsNameOnlyTheEventsItKnows(t *testing.T) {
	dir := t.TempDir()
	without := func(drop ...string) []string {
		return slices.DeleteFunc(slices.Clone(everyClaudeEvent), func(e string) bool { return slices.Contains(drop, e) })
	}
	for _, c := range []struct {
		name    string
		knows   []string
		events  []string
		prompts bool
	}{
		{"full", everyClaudeEvent, []string{"Notification", "PermissionRequest", "PostToolUse", "PostToolUseFailure", "SessionStart", "Stop", "UserPromptSubmit"}, true},
		{"no-permission-request", without("PermissionRequest"), []string{"SessionStart"}, false},
		{"no-post-tool-use", without("PostToolUse"), []string{"SessionStart"}, false},
		{"no-stop", without("Stop"), []string{"SessionStart"}, false},
		{"no-user-prompt-submit", without("UserPromptSubmit"), []string{"SessionStart"}, false},
		{"no-notification", without("Notification", "PostToolUseFailure"), []string{"PermissionRequest", "PostToolUse", "SessionStart", "Stop", "UserPromptSubmit"}, true},
	} {
		k, err := crew.ClaudeKnows(fakeClaude(t, dir, c.name, c.knows...))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		events, prompts := crew.ClaudeHookCommands("'/a b/autodoc'", k)
		var got []string
		for e, command := range events {
			got = append(got, e)
			want := "'/a b/autodoc' crew attention --claude-hook >/dev/null"
			if e == "SessionStart" {
				want = "'/a b/autodoc' crew endpoint --claude >/dev/null"
			}
			if command != want {
				t.Errorf("%s: %s runs %q, want %q", c.name, e, command, want)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, c.events) || prompts != c.prompts {
			t.Errorf("%s: events %v, prompts %v; want %v, %v", c.name, got, prompts, c.events, c.prompts)
		}
		settings, sp := crew.ClaudeSettings("autodoc", k)
		if hooks, _ := settings["hooks"].(map[string]any); len(hooks) != len(c.events) || sp != prompts || settings["crossSessionInbound"] != "accept" {
			t.Errorf("%s: settings %v, prompts %v", c.name, settings, sp)
		}
	}
	// crossSessionInbound is a setting the binary knows too, or it isn't set
	k, err := crew.ClaudeKnows(fakeClaude(t, dir, "no-inbound", without("crossSessionInbound")...))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := crew.ClaudeSettings("autodoc", k); s["crossSessionInbound"] != nil {
		t.Errorf("a Claude Code without crossSessionInbound gets it set: %v", s)
	}
	// a file that names no SessionStart is no Claude Code: refused, never taken as knowing nothing
	if _, err := crew.ClaudeKnows(fakeClaude(t, dir, "not-claude", "PermissionRequest")); err == nil {
		t.Error("a binary naming no SessionStart was read as a Claude Code")
	}
	// the floor, for one that can't be read: what a wake needs, and no prompts
	if events, prompts := crew.ClaudeHookCommands("autodoc", crew.ClaudeFloor()); len(events) != 1 || prompts {
		t.Errorf("the floor writes %v, prompts %v; want SessionStart alone", events, prompts)
	}
}

// TestWhatAClaudeKnowsIsReadAgainWhenItChanges: ClaudeKnows' answer is kept per binary, by its
// path, size and modification time: the same binary is answered from it, an updated one (another
// size or time) read again, and a link answered as the binary it names.
func TestWhatAClaudeKnowsIsReadAgainWhenItChanges(t *testing.T) {
	dir := t.TempDir()
	path := fakeClaude(t, dir, "claude", "SessionStart")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if k, err := crew.ClaudeKnows(link); err != nil || k.Reports() {
		t.Fatalf("an old Claude Code: %v, %v; want no prompts", k, err)
	}
	fakeClaude(t, dir, "claude", everyClaudeEvent...)
	at := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	if k, err := crew.ClaudeKnows(link); err != nil || !k.Reports() {
		t.Fatalf("the same path updated: %v, %v; want it read again, with prompts", k, err)
	}
}

// TestClaudeOfLooksUpLikeTheShell: a name is the first executable file on PATH, a folder of that
// name and a file that isn't executable skipped; a path is taken as given, relative to the agent's
// folder; a name on no folder of PATH is an error.
func TestClaudeOfLooksUpLikeTheShell(t *testing.T) {
	a, b, c, work := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(a, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "claude"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	want := fakeClaude(t, c, "claude", "SessionStart")
	if got, _, err := crew.ClaudeOf("claude", strings.Join([]string{a, b, c}, string(os.PathListSeparator)), work, ""); err != nil || got != want {
		t.Fatalf("on PATH: %q, %v; want %q", got, err, want)
	}
	rel := fakeClaude(t, work, "my-claude", "SessionStart")
	if got, _, err := crew.ClaudeOf("./my-claude", "", work, ""); err != nil || got != rel {
		t.Fatalf("a relative path: %q, %v; want %q", got, err, rel)
	}
	if _, _, err := crew.ClaudeOf("claude", a, work, ""); err == nil {
		t.Fatal("a name on no folder of PATH was found")
	}
}

// fakeMise writes a mise at dir/mise whose `which claude`, run in work, answers tool.
func fakeMise(t *testing.T, dir, work, tool string) string {
	t.Helper()
	here, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	mise := filepath.Join(dir, "mise")
	script := "#!/bin/sh\n[ \"$1 $2\" = 'which claude' ] && [ \"$(pwd -P)\" = '" + here + "' ] && echo '" + tool + "'\n"
	if err := os.WriteFile(mise, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return mise
}

// TestClaudeOfFollowsAMiseShim: a name whose file links to mise itself (a mise shim) is the tool
// mise runs for it in the agent's folder (`mise which <name>`), not mise.
func TestClaudeOfFollowsAMiseShim(t *testing.T) {
	tools, shims, work := t.TempDir(), t.TempDir(), t.TempDir()
	real := fakeClaude(t, tools, "claude", "SessionStart")
	if err := os.Symlink(fakeMise(t, tools, work, real), filepath.Join(shims, "claude")); err != nil {
		t.Fatal(err)
	}
	if got, _, err := crew.ClaudeOf("claude", shims, work, ""); err != nil || got != real {
		t.Fatalf("a mise shim: %q, %v; want the tool it runs, %q", got, err, real)
	}
}

// TestClaudeOfLooksPastAWrapper: a wrapper script first on PATH (as `exec mise x claude -- claude`
// is) doesn't read as a Claude Code, so the CLI it runs is found: mise's tool for the name when mise
// is on PATH, else the next file on PATH that reads as one. What it knows is the real binary's.
func TestClaudeOfLooksPastAWrapper(t *testing.T) {
	wrappers, tools, later, work := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(wrappers, "claude"), []byte("#!/bin/bash\nexec mise x claude -- claude \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	real := fakeClaude(t, later, "claude", everyClaudeEvent...)
	sep := string(os.PathListSeparator)
	got, k, err := crew.ClaudeOf("claude", wrappers+sep+later, work, "")
	if err != nil || got != real || !k.Reports() {
		t.Fatalf("a wrapper, then the real one on PATH: %q, %v, %v; want %q, with its prompt hooks", got, k, err, real)
	}
	miseTool := fakeClaude(t, tools, "claude", everyClaudeEvent...)
	fakeMise(t, tools, work, miseTool)
	if got, _, err := crew.ClaudeOf("claude", wrappers+sep+later+sep+tools, work, ""); err != nil || got != miseTool {
		t.Fatalf("a wrapper, with mise on PATH: %q, %v; want mise's tool %q before the later file", got, err, miseTool)
	}
	if _, _, err := crew.ClaudeOf("claude", wrappers, work, ""); err == nil || !strings.Contains(err.Error(), filepath.Join(wrappers, "claude")) {
		t.Fatalf("a wrapper alone: %v; want an error naming the file tried", err)
	}
}

// TestALiteralAcrossTwoChunksIsFound: the binary is read a chunk at a time, never whole, and an
// event's literal split by a chunk's end is still found.
func TestALiteralAcrossTwoChunksIsFound(t *testing.T) {
	const chunk = 1 << 20 // claudeScanChunk
	lit := `"PermissionRequest"`
	for _, boundary := range []int{chunk, 2 * chunk} { // the first chunk's end, and one the carry crosses again
		for _, split := range []int{1, len(lit) / 2, len(lit) - 1} { // the literal's bytes before the boundary
			b := bytes.Repeat([]byte{0}, boundary-split)
			b = append(b, lit...)
			b = append(b, bytes.Repeat([]byte{0}, 4096)...)
			for _, e := range everyClaudeEvent {
				if e != "PermissionRequest" {
					b = append(b, `"`+e+`" `...)
				}
			}
			path := filepath.Join(t.TempDir(), "claude")
			if err := os.WriteFile(path, b, 0o755); err != nil {
				t.Fatal(err)
			}
			if k, err := crew.ClaudeKnows(path); err != nil || !k["PermissionRequest"] || !k.Reports() {
				t.Errorf("a literal %d bytes before the chunk end at %d: %v, %v; want it found", split, boundary, k, err)
			}
		}
	}
}

// TestWhatAClaudeKnowsIsKeptForTheNextRun: ClaudeKnowsIn writes its answer into its cache file,
// and a later process asking of the same binary (its path, size and time) takes it from there,
// without reading the binary.
func TestWhatAClaudeKnowsIsKeptForTheNextRun(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "state", "claude-hooks.json")
	first := fakeClaude(t, dir, "first", everyClaudeEvent...)
	if k, err := crew.ClaudeKnowsIn(first, cache); err != nil || !k.Reports() {
		t.Fatalf("the first read: %v, %v", k, err)
	}
	if b, err := os.ReadFile(cache); err != nil || !strings.Contains(string(b), first) {
		t.Fatalf("the cache after a read: %q, %v; want the binary's answer", b, err)
	}
	// another binary, never read in this process: a cache entry for it, saying it knows SessionStart
	// alone, is believed over its content, which knows every hook
	other := fakeClaude(t, dir, "other", everyClaudeEvent...)
	fi, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	entry := fmt.Sprintf(`[{"path":%q,"size":%d,"mtime":%d,"knows":{"SessionStart":true}}]`, other, fi.Size(), fi.ModTime().UnixNano())
	if err := os.WriteFile(cache, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	if k, err := crew.ClaudeKnowsIn(other, cache); err != nil || k.Reports() {
		t.Fatalf("a binary the cache answers: %v, %v; want the cache's answer, not a read", k, err)
	}
}
