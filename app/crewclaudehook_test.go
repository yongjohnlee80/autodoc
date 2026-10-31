package app

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestClaudesHooksReportItsPermissionPrompts: `crew attention --claude-hook`, run by Claude's hooks
// with their input (as live-probed on Claude Code 2.1.280), raises the session's permission
// attention when a prompt shows, saying what it asks, and clears it once it is behind it: approved
// (PostToolUse, Stop), denied (the next UserPromptSubmit), or idle (another Notification). It
// prints nothing and exits 0 whatever happens; with nothing raised, an event calls nothing.
func TestClaudesHooksReportItsPermissionPrompts(t *testing.T) {
	d := newCrewDaemon(t)
	path := d.agent("cl", "i1", "store-1")
	t.Setenv("AUTODOC_CREW_CONTEXT", path)
	hook := func(input string) {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := crewMain(d.ctx, []string{"crew", "attention", "--claude-hook"}, strings.NewReader(input), &out, &errOut); code != 0 || out.Len() != 0 {
			t.Fatalf("the hook on %s: exit %d, stdout %q, stderr %q", input, code, out.String(), errOut.String())
		}
		if errOut.Len() != 0 {
			t.Fatalf("the hook on %s failed: %s", input, errOut.String())
		}
	}
	attention := func() []string {
		t.Helper()
		w, err := d.deps.inbox.Watch(d.ctx, 0, 0, "")
		if err != nil || w.Snapshot == nil {
			t.Fatalf("the inbox: %v", err)
		}
		var out []string
		for _, a := range w.Snapshot.Attention {
			out = append(out, a.Kind+" "+a.Text)
		}
		return out
	}
	asked := `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"touch probe-file","description":"Create a probe file"}}`
	notified := `{"hook_event_name":"Notification","message":"Claude needs your permission","notification_type":"permission_prompt"}`
	for _, then := range []struct{ name, event string }{
		{"approved", `{"hook_event_name":"PostToolUse","tool_name":"Bash"}`},
		{"the turn ends", `{"hook_event_name":"Stop"}`},
		{"denied, then the next message", `{"hook_event_name":"UserPromptSubmit","prompt":"do something else"}`},
		{"idle", `{"hook_event_name":"Notification","message":"Claude is waiting for your input","notification_type":"idle_prompt"}`},
	} {
		hook(asked)
		hook(notified) // the same prompt's Notification: it raised already, saying more
		if got := attention(); len(got) != 1 || got[0] != crew.AttentionPermission+" Bash: touch probe-file" {
			t.Fatalf("a prompt shown: attention %q, want the permission and what it asks", got)
		}
		hook(then.event)
		if got := attention(); len(got) != 0 {
			t.Fatalf("%s: attention %q, want it cleared", then.name, got)
		}
	}
	// a permission Notification alone raises it too, with Claude's own message
	hook(notified)
	if got := attention(); len(got) != 1 || got[0] != crew.AttentionPermission+" Claude needs your permission" {
		t.Fatalf("a permission Notification alone: %q", got)
	}
	hook(`{"hook_event_name":"PostToolUse"}`)

	// with nothing raised, an event calls nothing; input that isn't a hook's is said on stderr alone
	calls := 0
	count := func(context.Context, sessionContext, string, []any) (any, error) { calls++; return nil, nil }
	for _, e := range []string{`{"hook_event_name":"PostToolUse"}`, `{"hook_event_name":"Stop"}`, `{"hook_event_name":"SessionStart"}`} {
		if err := claudeHook(d.ctx, strings.NewReader(e), path, count); err != nil || calls != 0 {
			t.Fatalf("%s with nothing raised: %v, %d calls", e, err, calls)
		}
	}
	var out, errOut bytes.Buffer
	if code := crewMain(d.ctx, []string{"crew", "attention", "--claude-hook"}, strings.NewReader("not json"), &out, &errOut); code != 0 ||
		out.Len() != 0 || !strings.Contains(errOut.String(), "the hook's input") {
		t.Fatalf("input that isn't a hook's: exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(claudePromptFile(agentFolder(path), "x")); err == nil {
		t.Fatal("a marker for no session")
	}
}

// TestClaudeAsksSaysWhatThePromptAsks: the attention's text is the tool and its command, file or
// first input, else Claude's message, on one line and at most claudeAsksMax bytes.
func TestClaudeAsksSaysWhatThePromptAsks(t *testing.T) {
	for want, in := range map[string]claudeHookInput{
		"Bash: ls -la":           {ToolName: "Bash", ToolInput: []byte(`{"command":"ls\n -la"}`)},
		"Edit: /a/b.go":          {ToolName: "Edit", ToolInput: []byte(`{"file_path":"/a/b.go","old_string":"x"}`)},
		"WebFetch: https://x.io": {ToolName: "WebFetch", ToolInput: []byte(`{"url":"https://x.io"}`)},
		"Task":                   {ToolName: "Task", ToolInput: []byte(`{"prompt":"go"}`)},
		"Claude needs you":       {Message: "Claude needs you"},
		"a permission":           {},
	} {
		if got := claudeAsks(in); got != want {
			t.Errorf("claudeAsks(%+v) = %q, want %q", in, got, want)
		}
	}
	long := claudeAsks(claudeHookInput{ToolName: "Bash", ToolInput: []byte(`{"command":"` + strings.Repeat("x", 1000) + `"}`)})
	if len(long) != claudeAsksMax {
		t.Fatalf("a long command: %d bytes, want %d", len(long), claudeAsksMax)
	}
}
