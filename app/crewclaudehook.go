package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// Claude Code's permission prompts, reported (docs/decisions/crew.md#the-human-inbox): the hooks
// AutoDoc writes for a crew Claude agent run `autodoc crew attention --claude-hook`, which reads
// the hook's input and keeps the session's permission attention raised while Claude shows a
// permission prompt, and clears it once none shows. As live-probed on Claude Code 2.1.280:
//
//	a prompt shown      PermissionRequest (tool_name, tool_input; no tool_use_id), and Notification
//	                    (notification_type permission_prompt); several queue, one per tool use
//	approved            that tool's PostToolUse (the same tool_name and tool_input), or its
//	                    PostToolUseFailure when it fails, then Stop once the turn ends; another
//	                    tool's PostToolUse may come while a prompt shows
//	denied              nothing until the user's next message (UserPromptSubmit)
//
// So each prompt shown is an open prompt, named by its tool and input, counted (two identical calls
// that both prompt are two); its own PostToolUse or PostToolUseFailure closes one,
// and UserPromptSubmit and Stop, past every prompt, close them all. The attention is raised while any
// is open. Another Notification (idle, elicitation) proves nothing about a prompt, so it changes
// nothing.
//
// What the daemon was last told is kept beside the open prompts, written only once it was told:
// a raise or clear that failed (the daemon restarting, the bound) is retried by the next hook, never
// taken for done. Until then the prompt is unreported, which no hook can help: there is no event
// to retry it on.
//
// It prints nothing, ever: a PermissionRequest hook's output can decide the prompt, and a
// UserPromptSubmit hook's joins the agent's context. It always exits 0, so a hook never stops
// Claude: a failure is only written to stderr.

// claudeHookInput is what a hook reads of Claude Code's input.
type claudeHookInput struct {
	Event     string          `json:"hook_event_name"`
	Type      string          `json:"notification_type"`
	Message   string          `json:"message"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// claudeHookBound bounds one hook's run: its read of the input, its wait for the folder's lock, and
// its call to the daemon.
const claudeHookBound = 5 * time.Second

// claudePromptsDir is the folder of a session's open prompts, in the agent's folder (as the agy
// bridge's files are kept, never in the contexts' folder): one file per open prompt, named by
// promptKey and holding what it asks, and claudeReported, what the daemon was last told.
func claudePromptsDir(dir, session string) string {
	return filepath.Join(dir, "claude-prompts-"+session)
}

// claudeReported names the file of what the daemon was last told: "raised" or "cleared"; absent,
// nothing yet.
const claudeReported = "reported"

// claudeNotified is the open prompt a permission Notification stands for, when no PermissionRequest
// named one (a Claude that sends none): only UserPromptSubmit or Stop close it.
const claudeNotified = "notified"

// promptKey names an open prompt by its tool and input, as both its PermissionRequest and its
// PostToolUse carry them: the input's JSON compacted with its keys in order, so the two match.
func promptKey(tool string, input json.RawMessage) string {
	var v any
	canon := input
	if json.Unmarshal(input, &v) == nil {
		if b, err := json.Marshal(v); err == nil { // maps marshal with their keys sorted
			canon = b
		}
	}
	h := sha256.Sum256(append([]byte(tool+"\x00"), canon...))
	return "p-" + hex.EncodeToString(h[:12])
}

// crewClaudeHookMain runs `autodoc crew attention --claude-hook`: Claude's hook input on stdin.
func crewClaudeHookMain(stdin io.Reader, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), claudeHookBound)
	defer cancel()
	if err := claudeHook(ctx, stdin, os.Getenv("AUTODOC_CREW_CONTEXT"), crewCall); err != nil {
		fmt.Fprintln(stderr, "autodoc: the permission prompt's attention:", err)
	}
	return 0
}

// claudeHook acts on one hook's input, as the session the context names (envFile, else the
// working directory's), calling the daemon through call. Hooks of one session run one at a time
// (a lock in its prompts' folder): parallel tools' hooks come at once.
func claudeHook(ctx context.Context, stdin io.Reader, envFile string, call func(context.Context, sessionContext, string, []any) (any, error)) error {
	var in claudeHookInput
	if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&in); err != nil {
		return fmt.Errorf("the hook's input: %w", err)
	}
	switch in.Event {
	case "PermissionRequest", "Notification", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit", "Stop":
	default:
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	c, err := findSessionContext(envFile, cwd)
	if err != nil {
		return err
	}
	dir := claudePromptsDir(bridgeDir(envFile, cwd), c.Session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := lockFolder(ctx, filepath.Join(dir, "lock"))
	if err != nil {
		return err
	}
	defer unlock()

	asks := ""
	switch {
	case in.Event == "PermissionRequest":
		asks = claudeAsks(in)
		if err := countPrompt(filepath.Join(dir, promptKey(in.ToolName, in.ToolInput)), asks, 1); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(dir, claudeNotified)) // its Notification stood for this one
	case in.Event == "Notification" && in.Type == "permission_prompt":
		open, err := openPrompts(dir)
		if err != nil {
			return err
		}
		if len(open) == 0 { // its PermissionRequest names it, when there is one
			asks = claudeAsks(in)
			if err := countPrompt(filepath.Join(dir, claudeNotified), asks, 1); err != nil {
				return err
			}
		}
	case in.Event == "PostToolUse", in.Event == "PostToolUseFailure": // the tool ran: approved, done or failed
		if err := countPrompt(filepath.Join(dir, promptKey(in.ToolName, in.ToolInput)), "", -1); err != nil {
			return err
		}
	case in.Event == "UserPromptSubmit", in.Event == "Stop":
		open, err := openPrompts(dir)
		if err != nil {
			return err
		}
		for _, name := range open {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return reportPrompts(ctx, c, dir, asks, call)
}

// reportPrompts tells the daemon what dir's open prompts are, when that's not what it was last told
// (or a new prompt says something new): raised, saying what the newest asks, while any is open;
// cleared once none is. What it was told is written only once it was.
func reportPrompts(ctx context.Context, c sessionContext, dir, asks string, call func(context.Context, sessionContext, string, []any) (any, error)) error {
	open, err := openPrompts(dir)
	if err != nil {
		return err
	}
	told, err := os.ReadFile(filepath.Join(dir, claudeReported))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	want := "cleared"
	if len(open) > 0 {
		want = "raised"
	}
	if string(told) == want && asks == "" {
		return nil
	}
	if string(told) == "" && want == "cleared" {
		return nil // never raised: nothing to clear
	}
	if want == "raised" {
		if asks == "" { // a retry: what an open prompt asks
			p, err := readPrompt(filepath.Join(dir, open[len(open)-1]))
			if err != nil {
				return err
			}
			asks = p.Asks
		}
		if _, err := call(ctx, c, "crew.attention", []any{c.Credential, "raise", crew.AttentionPermission, asks}); err != nil {
			_ = os.Remove(filepath.Join(dir, claudeReported)) // unknown now: the next hook raises again
			return err
		}
	} else if _, err := call(ctx, c, "crew.attention", []any{c.Credential, "clear"}); err != nil {
		_ = os.WriteFile(filepath.Join(dir, claudeReported), []byte("raised"), 0o600) // still raised, as far as anyone knows
		return err
	}
	return os.WriteFile(filepath.Join(dir, claudeReported), []byte(want), 0o600)
}

// openPrompt is one open prompt's file: how many of its tool use are asking (two identical calls
// that both prompt are two), and what it asks.
type openPrompt struct {
	N    int    `json:"n"`
	Asks string `json:"asks"`
}

func readPrompt(path string) (openPrompt, error) {
	var p openPrompt
	b, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(b, &p)
}

// countPrompt adds by (1: one more asking, -1: one closed) to the open prompt at path, asking asks
// when given; at 0 it is closed (its file gone). Closing one that isn't open changes nothing.
func countPrompt(path, asks string, by int) error {
	p, err := readPrompt(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		p = openPrompt{} // unreadable: as if new
	}
	p.N += by
	if asks != "" {
		p.Asks = asks
	}
	if p.N <= 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// openPrompts are dir's open prompts, by file name, oldest first.
func openPrompts(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type open struct {
		name string
		at   time.Time
	}
	var all []open
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, "p-") || n == claudeNotified {
			if fi, err := e.Info(); err == nil {
				all = append(all, open{n, fi.ModTime()})
			}
		}
	}
	slices.SortStableFunc(all, func(a, b open) int { return a.at.Compare(b.at) })
	names := make([]string, len(all))
	for i, o := range all {
		names[i] = o.name
	}
	return names, nil
}

// claudeAsksMax is the most of what a prompt asks the attention carries: a toast's line.
const claudeAsksMax = 300

// claudeAsks is what the prompt asks, as the attention's text: the tool and its command, file or
// first input, or Claude's own message.
func claudeAsks(in claudeHookInput) string {
	what := in.ToolName
	var input map[string]any
	if json.Unmarshal(in.ToolInput, &input) == nil {
		for _, k := range []string{"command", "file_path", "url", "pattern", "path"} {
			if v, ok := input[k].(string); ok && v != "" {
				what += ": " + v
				break
			}
		}
	}
	if what == "" {
		what = in.Message
	}
	if what == "" {
		what = "a permission"
	}
	what = strings.Join(strings.Fields(what), " ")
	if len(what) > claudeAsksMax {
		what = what[:claudeAsksMax]
	}
	return what
}
