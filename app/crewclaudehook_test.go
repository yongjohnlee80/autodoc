package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestClaudesHooksReportItsPermissionPrompts: `crew attention --claude-hook`, run by Claude's hooks
// with their input (as live-probed on Claude Code 2.1.280), keeps the session's permission
// attention raised while any prompt shows, saying what the newest asks: a prompt's own PostToolUse
// closes it, another tool's (a parallel one) doesn't, and so does its PostToolUseFailure when the
// approved tool fails; two identical prompting calls are two to close; a prompt whose answer widened
// its tool's input (AskUserQuestion) is closed by that tool's PostToolUse, while any other tool's
// wider input (a parallel call) closes nothing; the next UserPromptSubmit
// (after a denial) or Stop closes them all; an idle Notification changes nothing. It prints nothing
// and exits 0.
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
	expect := func(when string, want ...string) {
		t.Helper()
		if got := attention(); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("%s: attention %q, want %q", when, got, want)
		}
	}
	bash := `"tool_name":"Bash","tool_input":{"command":"touch probe-file","description":"Create a probe file"}`
	read := `"tool_name":"Read","tool_input":{"file_path":"/etc/hostname"}`
	// the input as PostToolUse gives it: the same tool and input, its keys in another order, and more
	bashDone := `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"description":"Create a probe file","command":"touch probe-file"},"tool_response":{}}`
	readDone := `{"hook_event_name":"PostToolUse",` + read + `,"tool_response":{}}`
	notified := `{"hook_event_name":"Notification","message":"Claude needs your permission","notification_type":"permission_prompt"}`
	perm := crew.AttentionPermission + " "

	// B1: two prompts queued; the first approved, a parallel tool done: the Bash prompt still shows
	hook(`{"hook_event_name":"PermissionRequest",` + read + `}`)
	hook(notified)
	hook(`{"hook_event_name":"PermissionRequest",` + bash + `}`)
	expect("two prompts", perm+"Bash: touch probe-file")
	hook(readDone)
	hook(`{"hook_event_name":"PostToolUse","tool_name":"Grep","tool_input":{"pattern":"x"}}`) // a parallel tool, unprompted
	expect("the Read approved, a parallel Grep done", perm+"Bash: touch probe-file")
	hook(`{"hook_event_name":"Notification","message":"Claude is waiting for your input","notification_type":"idle_prompt"}`)
	expect("an idle Notification", perm+"Bash: touch probe-file")
	hook(bashDone)
	expect("the Bash approved")

	// denied: nothing comes until the next message, which is past it
	hook(`{"hook_event_name":"PermissionRequest",` + bash + `}`)
	expect("asked again", perm+"Bash: touch probe-file")
	hook(`{"hook_event_name":"UserPromptSubmit","prompt":"do something else"}`)
	expect("denied, then the next message")

	// Stop is past every prompt; a Notification with no PermissionRequest stands for one until then
	hook(notified)
	expect("a permission Notification alone", perm+"Claude needs your permission")
	hook(readDone) // not its tool: still open
	expect("another tool done", perm+"Claude needs your permission")
	hook(`{"hook_event_name":"Stop"}`)
	expect("the turn's end")

	// two identical calls that both prompt are two open prompts: the first done leaves the second
	hook(`{"hook_event_name":"PermissionRequest",` + bash + `}`)
	hook(`{"hook_event_name":"PermissionRequest",` + bash + `}`)
	hook(bashDone)
	expect("one of two identical prompts done", perm+"Bash: touch probe-file")
	hook(bashDone)
	expect("both done")

	// a tool approved that fails closes its prompt by PostToolUseFailure (it has no PostToolUse)
	hook(`{"hook_event_name":"PermissionRequest",` + bash + `}`)
	hook(`{"hook_event_name":"PostToolUseFailure",` + bash + `,"error":"mkdir: cannot create directory","is_interrupt":false}`)
	expect("an approved tool that failed")

	// a prompt that collects an answer widens its tool's input: AskUserQuestion's PostToolUse carries
	// its questions with the answers and annotations added, and still closes it at once, not at Stop
	ask := `"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which?","header":"Pick","multiSelect":false,"options":[{"label":"A","description":"a"},{"label":"B","description":"b"}]}]}`
	asked := `{"hook_event_name":"PermissionRequest",` + ask + `}`
	answered := `{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":{"answers":{"Which?":"A"},"annotations":{},` +
		`"questions":[{"question":"Which?","header":"Pick","multiSelect":false,"options":[{"label":"A","description":"a"},{"label":"B","description":"b"}]}]},"tool_response":{}}`
	hook(asked)
	expect("a question asked", perm+"AskUserQuestion")
	hook(answered)
	expect("the question answered")

	// widened, but only ever added to: a value the prompt changed, or another call of the same tool
	// in parallel, closes nothing; the oldest of two widened prompts of one tool closes first
	hook(`{"hook_event_name":"PermissionRequest",` + bash + `}`)
	hook(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"touch other-file","description":"Create a probe file"},"tool_response":{}}`)
	hook(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls","description":"List"},"tool_response":{}}`)
	expect("a changed command, and a parallel Bash", perm+"Bash: touch probe-file")
	// only a tool that collects an answer widens its input: a parallel Bash with the same command and
	// description plus a timeout is another call, and closes nothing (the prompt still shows, and a
	// window must not press Enter into it)
	hook(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"touch probe-file","description":"Create a probe file","timeout":5000},"tool_response":{}}`)
	expect("a parallel Bash holding the asked input and more", perm+"Bash: touch probe-file")
	hook(bashDone)
	expect("its own done")
	hook(asked)
	hook(`{"hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Second?"}]}}`)
	hook(answered)
	expect("the first question answered", perm+"AskUserQuestion")
	hook(`{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Second?"}],"answers":{"Second?":"yes"}},"tool_response":{}}`)
	expect("both answered")
}

// TestAClaudeHookRetriesWhatTheDaemonWasntTold: a raise or a clear that failed is never taken for
// done: the next hook tells the daemon again. With nothing to change, a hook calls nothing.
func TestAClaudeHookRetriesWhatTheDaemonWasntTold(t *testing.T) {
	d := newCrewDaemon(t)
	path := d.agent("cl", "i1", "store-1")
	var calls []string
	fail := false
	call := func(ctx context.Context, c sessionContext, method string, params []any) (any, error) {
		calls = append(calls, fmt.Sprint(params[1]))
		if fail {
			return nil, errors.New("the daemon is restarting")
		}
		return crewCall(ctx, c, method, params)
	}
	run := func(input string) error {
		return claudeHook(d.ctx, strings.NewReader(input), path, call)
	}
	for _, e := range []string{`{"hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{}}`, `{"hook_event_name":"Stop"}`, `{"hook_event_name":"SessionStart"}`} {
		if err := run(e); err != nil || len(calls) != 0 {
			t.Fatalf("%s with nothing raised: %v, calls %q", e, err, calls)
		}
	}
	asked := `{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"make"}}`
	fail = true
	if err := run(asked); err == nil {
		t.Fatal("a raise the daemon refused was taken for done")
	}
	fail = false
	if err := run(`{"hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"/x"}}`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "raise,raise" {
		t.Fatalf("after a failed raise, the next hook (another tool's) called %q, want the raise again", calls)
	}
	calls = nil
	fail = true
	if err := run(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"make"}}`); err == nil {
		t.Fatal("a clear the daemon refused was taken for done")
	}
	fail = false
	if err := run(`{"hook_event_name":"Stop"}`); err != nil {
		t.Fatal(err)
	}
	if err := run(`{"hook_event_name":"Stop"}`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "clear,clear" {
		t.Fatalf("after a failed clear: calls %q, want it retried once, then nothing", calls)
	}
	var out, errOut bytes.Buffer
	if code := crewMain(d.ctx, []string{"crew", "attention", "--claude-hook"}, strings.NewReader("not json"), &out, &errOut); code != 0 ||
		out.Len() != 0 || !strings.Contains(errOut.String(), "the hook's input") {
		t.Fatalf("input that isn't a hook's: exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
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
