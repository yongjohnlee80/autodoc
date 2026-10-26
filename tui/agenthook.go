package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// THE WORKSPACE NOTICE HOOK (ADR 1792988759 r2 §4.3) — a Claude agent hears of a workspace switch
// with the user's next message, never through its terminal: the agent's folder's
// .claude/settings.json runs `autodoc --workspace-notice` on UserPromptSubmit, and Claude Code adds
// what it prints, the switch's one line or nothing, to that message's context. The folder is the
// workspace's, shared by every profile; only Claude Code reads the file.

// noticeCommand is the hook's command: dir's own autodoc (agentWrapper), so the TUI's build and
// config answer.
func noticeCommand(dir string) string {
	return shellQuote(filepath.Join(dir, "bin", "autodoc")) + " --workspace-notice"
}

// mergeClaudeHook makes path's hooks.UserPromptSubmit run command once, keeping every other key,
// hook and matcher the user wrote there, numbers as written. A file that isn't a JSON object, or
// whose hooks aren't the shape Claude Code reads, is left as it is, and the error says so.
func mergeClaudeHook(path, command string) error {
	settings := map[string]any{}
	switch b, err := os.ReadFile(path); {
	case err == nil:
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		// one object and nothing after it: a second value or trailing text would be lost on rewrite
		if err := d.Decode(&settings); err != nil || settings == nil || d.Decode(new(json.RawMessage)) != io.EOF {
			return fmt.Errorf("%s is not one JSON object: left as it is, without the workspace notice hook", path)
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok && settings["hooks"] != nil {
		return fmt.Errorf("%s: hooks is not an object: left as it is, without the workspace notice hook", path)
	}
	if hooks == nil {
		hooks = map[string]any{}
	}
	groups, ok := hooks["UserPromptSubmit"].([]any)
	if !ok && hooks["UserPromptSubmit"] != nil {
		return fmt.Errorf("%s: hooks.UserPromptSubmit is not a list: left as it is, without the workspace notice hook", path)
	}
	kept := []any{}
	for _, g := range groups {
		if g = withoutCommand(g, command); g != nil {
			kept = append(kept, g)
		}
	}
	hooks["UserPromptSubmit"] = append(kept, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}})
	settings["hooks"] = hooks
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, append(out, '\n'), 0o600)
}

// withoutCommand is the hook group g with every hook running command taken out: nil only when that
// took out its last hook. A group none was taken from, even one with no hooks, is the user's, kept
// as it is, and so is a group of another shape.
func withoutCommand(g any, command string) any {
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
		if m, ok := hk.(map[string]any); ok && m["command"] == command {
			continue
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
