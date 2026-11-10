package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// THE WORKSPACE NOTICE HOOK (docs/decisions/crew.md#the-workspace-notice-hook) — a Claude agent
// hears of a workspace switch with the user's next message, never through its terminal: the agent's
// folder's .claude/settings.json runs `autodoc --workspace-notice` on UserPromptSubmit, and Claude
// Code adds what it prints, the switch's one line or nothing, to that message's context. The folder
// is the agent's (agentFolder); only Claude Code reads the file. mergeClaudeSettings writes
// AutoDoc's other hooks and settings there the same way.

// noticeTail is what the workspace notice hook runs after its program.
const noticeTail = " --workspace-notice"

// noticeCommand is the hook's command: the build by its absolute path (bin), never a bare autodoc a
// PATH could make another, its config from $AUTODOC_CONFIG in the agent's environment, so the TUI's
// build and config answer.
func noticeCommand(bin string) string {
	return shellQuote(bin) + noticeTail
}

// mergeClaudeHook makes path's hooks.UserPromptSubmit run command once (mergeClaudeSettings).
func mergeClaudeHook(path, command string) error {
	return mergeClaudeSettings(path, claudeMerge{events: map[string]string{"UserPromptSubmit": command}, ours: []string{noticeTail}})
}

// claudeMerge is what mergeClaudeSettings writes: each hook event's command, the top-level keys to
// set, and what was AutoDoc's before: commands taken out of every event first (an event left with
// no hook goes), each named by its tail after its program (ourCommand), so AutoDoc's of any build,
// wherever it was, are taken out; and keys whose value is unset's taken out unless set again.
type claudeMerge struct {
	events map[string]string
	set    map[string]any
	ours   []string
	unset  map[string]any
	// allow is AutoDoc's permissions.allow rules (crew.ClaudeAllow): AutoDoc's of any build taken out
	// first (crew.IsClaudeAllow), every rule the user wrote kept; nil leaves permissions alone
	allow []string
}

// mergeClaudeSettings makes each of path's hooks.<event> run its command in m.events once, and sets
// each key in m.set at the top level, keeping every other key, hook and matcher the user wrote
// there, numbers as written, and taking out m's old ones. A file that isn't a JSON object, or whose
// hooks aren't the shape Claude Code reads, is left as it is, and the error says so.
func mergeClaudeSettings(path string, m claudeMerge) error {
	events, set := m.events, m.set
	settings := map[string]any{}
	switch b, err := os.ReadFile(path); {
	case err == nil:
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		// one object and nothing after it: a second value or trailing text would be lost on rewrite
		if err := d.Decode(&settings); err != nil || settings == nil || d.Decode(new(json.RawMessage)) != io.EOF {
			return fmt.Errorf("%s is not one JSON object: left as it is, without AutoDoc's hooks", path)
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok && settings["hooks"] != nil {
		return fmt.Errorf("%s: hooks is not an object: left as it is, without AutoDoc's hooks", path)
	}
	if hooks == nil {
		hooks = map[string]any{}
	}
	for event, v := range hooks {
		groups, ok := v.([]any)
		if !ok || len(m.ours) == 0 {
			continue
		}
		kept := []any{}
		for _, g := range groups {
			if g = withoutHooks(g, func(c string) bool { return ourCommand(c, m.ours) }); g == nil {
				continue
			}
			if g != nil {
				kept = append(kept, g)
			}
		}
		if len(kept) < len(groups) && len(kept) == 0 {
			delete(hooks, event) // only ours were there: an event the CLI may not know is not left named
		} else {
			hooks[event] = kept
		}
	}
	for k, v := range m.unset {
		if _, again := set[k]; !again && settings[k] == v {
			delete(settings, k)
		}
	}
	for event, command := range events {
		groups, ok := hooks[event].([]any)
		if !ok && hooks[event] != nil {
			return fmt.Errorf("%s: hooks.%s is not a list: left as it is, without AutoDoc's hooks", path, event)
		}
		kept := []any{}
		for _, g := range groups {
			if g = withoutHooks(g, func(c string) bool { return c == command }); g != nil {
				kept = append(kept, g)
			}
		}
		hooks[event] = append(kept, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}})
	}
	if len(hooks) > 0 || settings["hooks"] != nil {
		settings["hooks"] = hooks
	}
	for k, v := range set {
		settings[k] = v
	}
	if m.allow != nil {
		perm, ok := settings["permissions"].(map[string]any)
		if !ok && settings["permissions"] != nil {
			return fmt.Errorf("%s: permissions is not an object: left as it is, without AutoDoc's hooks", path)
		}
		if perm == nil {
			perm = map[string]any{}
		}
		rules, ok := perm["allow"].([]any)
		if !ok && perm["allow"] != nil {
			return fmt.Errorf("%s: permissions.allow is not a list: left as it is, without AutoDoc's hooks", path)
		}
		kept := []any{}
		for _, r := range rules {
			if s, ok := r.(string); ok && crew.IsClaudeAllow(s) {
				continue
			}
			kept = append(kept, r)
		}
		for _, r := range m.allow {
			kept = append(kept, r)
		}
		perm["allow"], settings["permissions"] = kept, perm
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, append(out, '\n'), 0o600)
}

// withoutHooks is the hook group g with every hook whose command drop says taken out: nil only when
// that took out its last hook. A group none was taken from, even one with no hooks, is the user's,
// kept as it is, and so is a group of another shape.
func withoutHooks(g any, drop func(command string) bool) any {
	group, ok := g.(map[string]any)
	if !ok {
		return g
	}
	list, ok := group["hooks"].([]any)
	if !ok {
		return g
	}
	var left []any
	for _, hk := range list {
		if m, ok := hk.(map[string]any); ok {
			if command, _ := m["command"].(string); drop(command) {
				continue
			}
		}
		left = append(left, hk)
	}
	if len(left) == len(list) {
		return g // none of ours: the group as the user wrote it
	}
	if len(left) == 0 {
		return nil
	}
	group["hooks"] = left
	return group
}

// writeWakeAcceptance configures only what lets the crew's fixed notice start a turn
// (docs/decisions/crew.md#wake-acceptance-not-tool-approvals), for a crew session's CLI of kind in
// dir: for a Claude Code that knows k, crew's one source of its settings (crew.ClaudeHookCommands,
// crew.ClaudeSetKeys), run by autodoc (the build by its absolute path, quoted): the SessionStart hook that registers its endpoint,
// crossSessionInbound "accept", and, when it can report them, the hooks that report its permission
// prompts (they report; they decide nothing), the events it doesn't know never named, and AutoDoc's
// hooks of an earlier start taken out first. prompts is whether those hooks were written, which
// the session's registration says. For the others nothing yet (Codex's queue starts turns as it
// is). Its one allowance is allow, the setup's: the guide's own commands (crew.ClaudeAllow, exactly
// crew.GuideCommands) for a session on CLI mail, the crew MCP tools' exact rules
// (crew.ClaudeMCPAllow) for one on MCP mail, in this folder's settings alone, AutoDoc's rules of
// either mode taken out first; it never writes approval policies, permission modes, deny rules or
// any other allow rule: a turn a wake starts runs under the profile's own tool policy.
func writeWakeAcceptance(autodoc, dir, kind string, k crew.ClaudeHooks, allow []string) (prompts bool, err error) {
	if kind != crew.KindClaude {
		return false, nil
	}
	events, prompts := crew.ClaudeHookCommands(autodoc, k)
	set := crew.ClaudeSetKeys(k)
	err = mergeClaudeSettings(filepath.Join(dir, ".claude", "settings.json"), claudeMerge{events: events, set: set,
		ours: crew.ClaudeCommandLines(""), unset: map[string]any{crew.ClaudeCrossSessionInbound: "accept"},
		allow: allow})
	return prompts && err == nil, err
}

// ourCommand reports whether command is one of AutoDoc's own: one program word, bare or quoted as
// shellQuote writes it, then exactly one of tails. The program is any build's, wherever it was (an
// earlier wrapper, a moved or upgraded build), so its old hooks are taken out, never left to run a
// program that is gone.
func ourCommand(command string, tails []string) bool {
	for _, tail := range tails {
		program, ok := strings.CutSuffix(command, tail)
		if !ok || program == "" {
			continue
		}
		if inner, quoted := strings.CutPrefix(program, "'"); quoted {
			if inner, ok = strings.CutSuffix(inner, "'"); ok && shellQuote(strings.ReplaceAll(inner, `'\''`, "'")) == program {
				return true
			}
			continue
		}
		if !strings.ContainsAny(program, " \t'\"") {
			return true
		}
	}
	return false
}
