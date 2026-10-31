package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// Claude Code's permission prompts, reported (docs/decisions/crew.md#the-human-inbox): the hooks
// AutoDoc writes for a crew Claude agent run `autodoc crew attention --claude-hook`, which reads
// the hook's input and raises the session's permission attention when Claude shows a permission
// prompt, and clears it once the prompt is behind it. As live-probed on Claude Code 2.1.280:
//
//	a prompt shown      PermissionRequest (tool_name, tool_input), and Notification
//	                    (notification_type permission_prompt)
//	approved            PostToolUse, then Stop
//	denied              nothing until the user's next message (UserPromptSubmit), or an idle
//	                    Notification
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

// claudeHookBound bounds one hook's run: its read of the input and its call to the daemon.
const claudeHookBound = 5 * time.Second

// claudePromptFile is the marker a raised permission attention leaves in the agent's folder (as the
// agy bridge's files are kept): a later event clears only what a prompt raised, and calls the
// daemon only then.
func claudePromptFile(dir, session string) string {
	return filepath.Join(dir, "claude-prompt-"+session)
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
// working directory's), calling the daemon through call.
func claudeHook(ctx context.Context, stdin io.Reader, envFile string, call func(context.Context, sessionContext, string, []any) (any, error)) error {
	var in claudeHookInput
	if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&in); err != nil {
		return fmt.Errorf("the hook's input: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	c, err := findSessionContext(envFile, cwd)
	if err != nil {
		return err
	}
	marker := claudePromptFile(bridgeDir(envFile, cwd), c.Session)
	prompt := in.Event == "PermissionRequest" || (in.Event == "Notification" && in.Type == "permission_prompt")
	if prompt {
		if in.Event == "Notification" {
			if _, err := os.Stat(marker); err == nil {
				return nil // its PermissionRequest raised it already, saying more
			}
		}
		if _, err := call(ctx, c, "crew.attention", []any{c.Credential, "raise", crew.AttentionPermission, claudeAsks(in)}); err != nil {
			return err
		}
		return os.WriteFile(marker, nil, 0o600)
	}
	switch in.Event {
	case "PostToolUse", "UserPromptSubmit", "Stop", "Notification":
	default:
		return nil
	}
	if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
		return nil // nothing raised: no call
	}
	if _, err := call(ctx, c, "crew.attention", []any{c.Credential, "clear"}); err != nil {
		return err
	}
	return os.Remove(marker)
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
