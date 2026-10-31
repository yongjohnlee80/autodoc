package crew

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// A crew Claude agent's settings (docs/decisions/crew.md#wake-acceptance-not-tool-approvals): what
// lets the crew's notice start its turn, and the hooks that register its endpoint and report its
// permission prompts. One source writes them (ClaudeHookCommands, ClaudeSetKeys), for an AutoDoc
// window's agent and, through `autodoc crew claude-settings`, for any other host's.
//
// Only the hook events and settings the installed Claude Code knows are written: an event it
// doesn't know, it skips, but it first opens a Settings Warning that waits for a key (live-probed on
// Claude Code 2.1.280), and the session doesn't start, so the agent never registers, until someone
// answers it.

// The hook events a crew Claude agent's settings name.
const (
	ClaudeSessionStart       = "SessionStart"
	ClaudePermissionRequest  = "PermissionRequest"
	ClaudeNotification       = "Notification"
	ClaudePostToolUse        = "PostToolUse"
	ClaudePostToolUseFailure = "PostToolUseFailure"
	ClaudeUserPromptSubmit   = "UserPromptSubmit"
	ClaudeStop               = "Stop"

	// ClaudeCrossSessionInbound is the setting that lets another session's message start a turn.
	ClaudeCrossSessionInbound = "crossSessionInbound"
)

// claudePromptHooks are the events that report a permission prompt (`crew attention
// --claude-hook`): PermissionRequest opens one, a permission Notification stands for one, the
// tool's own PostToolUse or PostToolUseFailure closes it, UserPromptSubmit and Stop close them all.
var claudePromptHooks = []string{ClaudePermissionRequest, ClaudeNotification, ClaudePostToolUse,
	ClaudePostToolUseFailure, ClaudeUserPromptSubmit, ClaudeStop}

// claudeReportsWith are the events without which prompts can't be reported: what opens one, and
// what closes them all. Without Notification a prompt is still opened by its PermissionRequest;
// without PostToolUseFailure a failed tool's prompt stays open until Stop, which errs safe.
var claudeReportsWith = []string{ClaudePermissionRequest, ClaudePostToolUse, ClaudeUserPromptSubmit, ClaudeStop}

// ClaudeHooks is what a Claude Code binary knows of the hook events and of
// ClaudeCrossSessionInbound.
type ClaudeHooks map[string]bool

// Reports is whether these hooks can report the agent's permission prompts.
func (k ClaudeHooks) Reports() bool {
	for _, e := range claudeReportsWith {
		if !k[e] {
			return false
		}
	}
	return true
}

// claudeKnown caches ClaudeKnows by binary: its path, size and modification time. A binary's read
// runs once: a second asker waits for the first's answer. A failed read is not kept.
var (
	claudeKnownMu sync.Mutex
	claudeKnown   = map[claudeBinary]*claudeRead{}
)

type claudeRead struct {
	done  chan struct{}
	hooks ClaudeHooks
	err   error
}

type claudeBinary struct {
	path  string
	size  int64
	mtime int64
}

// ClaudeKnows is the hook events the Claude Code binary at path knows, read from the binary itself:
// it carries each event it knows as a quoted literal ("PermissionRequest"), which the CLI's own
// list of valid events is built from. A version floor would do as well, but which release added an
// event isn't published where a host can read it. The answer is cached by the binary's path, size
// and modification time, so an update is read again. path's links are followed.
func ClaudeKnows(path string) (ClaudeHooks, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	key := claudeBinary{real, fi.Size(), fi.ModTime().UnixNano()}
	claudeKnownMu.Lock()
	r, ok := claudeKnown[key]
	if !ok {
		r = &claudeRead{done: make(chan struct{})}
		claudeKnown[key] = r
	}
	claudeKnownMu.Unlock()
	if ok {
		<-r.done
		return r.hooks, r.err
	}
	r.hooks, r.err = readClaudeHooks(real)
	if r.err != nil {
		r.err = fmt.Errorf("crew: %s: %w", path, r.err)
		claudeKnownMu.Lock()
		delete(claudeKnown, key)
		claudeKnownMu.Unlock()
	}
	close(r.done)
	return r.hooks, r.err
}

// readClaudeHooks reads ClaudeKnows' answer from the binary at path.
func readClaudeHooks(path string) (ClaudeHooks, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	k := ClaudeHooks{}
	for _, e := range append([]string{ClaudeSessionStart, ClaudeCrossSessionInbound}, claudePromptHooks...) {
		k[e] = bytes.Contains(b, []byte(`"`+e+`"`))
	}
	if !k[ClaudeSessionStart] {
		return nil, fmt.Errorf("names no SessionStart hook: not a Claude Code binary")
	}
	return k, nil
}

// ClaudeFloor is what every Claude Code a crew session runs is taken to know when its binary can't
// be read (a wrapper script, a path not found): SessionStart and crossSessionInbound, which a wake
// needs, and no prompt hooks, so its prompts are never taken for reported.
func ClaudeFloor() ClaudeHooks {
	return ClaudeHooks{ClaudeSessionStart: true, ClaudeCrossSessionInbound: true}
}

// ClaudeHookCommands is each hook event's command in a crew Claude agent's settings, for a Claude
// Code that knows k: the SessionStart hook that registers its endpoint and, when k can report
// prompts, the prompt hooks it knows. autodoc is the command line that runs the agent's autodoc
// (quoted as a shell needs it). prompts is whether the prompt hooks are there, which the session's
// registration says (crew.register's prompts).
func ClaudeHookCommands(autodoc string, k ClaudeHooks) (events map[string]string, prompts bool) {
	events = map[string]string{ClaudeSessionStart: autodoc + " crew endpoint --claude >/dev/null"}
	if prompts = k.Reports(); prompts {
		for _, e := range claudePromptHooks {
			if k[e] {
				events[e] = autodoc + " crew attention --claude-hook >/dev/null"
			}
		}
	}
	return events, prompts
}

// ClaudeCommandLines are every command ClaudeHookCommands may write for autodoc, for a host that
// takes its own out of a settings file before writing the ones a Claude Code knows now.
func ClaudeCommandLines(autodoc string) []string {
	all := ClaudeHooks{}
	for _, e := range append([]string{ClaudeSessionStart}, claudePromptHooks...) {
		all[e] = true
	}
	events, _ := ClaudeHookCommands(autodoc, all)
	var out []string
	for _, c := range events {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
}

// ClaudeSetKeys are the top-level settings a crew Claude agent's settings set, for a Claude Code
// that knows k: crossSessionInbound "accept", which lets the crew's notice start a turn, when k
// knows it. Nothing else: never approval policies, permission modes, or allow or deny rules, since a
// turn a wake starts runs under the profile's own tool policy.
func ClaudeSetKeys(k ClaudeHooks) map[string]any {
	if !k[ClaudeCrossSessionInbound] {
		return map[string]any{}
	}
	return map[string]any{ClaudeCrossSessionInbound: "accept"}
}

// ClaudeSettings is a crew Claude agent's settings, as `claude --settings` reads them
// (ClaudeHookCommands, ClaudeSetKeys).
func ClaudeSettings(autodoc string, k ClaudeHooks) (settings map[string]any, prompts bool) {
	events, prompts := ClaudeHookCommands(autodoc, k)
	hooks := map[string]any{}
	for e, command := range events {
		hooks[e] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}}
	}
	settings = ClaudeSetKeys(k)
	settings["hooks"] = hooks
	return settings, prompts
}

// claudeFindTimeout bounds FindClaude's one run of mise, for a shim.
const claudeFindTimeout = 5 * time.Second

// FindClaude is the Claude Code binary that program runs, found as a shell in dir with PATH path
// finds it: a name on path, or a path (relative to dir). A mise shim (a link to mise itself) is
// followed to the tool it runs there (`mise which`). Any other wrapper is answered as itself, which
// ClaudeKnows then can't read as a Claude Code.
func FindClaude(program, path, dir string) (string, error) {
	var found string
	if strings.Contains(program, "/") {
		found = program
		if !filepath.IsAbs(found) {
			found = filepath.Join(dir, found)
		}
	} else {
		for _, d := range filepath.SplitList(path) {
			if d == "" {
				d = "."
			}
			if !filepath.IsAbs(d) {
				d = filepath.Join(dir, d)
			}
			if fi, err := os.Stat(filepath.Join(d, program)); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
				found = filepath.Join(d, program)
				break
			}
		}
		if found == "" {
			return "", fmt.Errorf("crew: %s is not on the agent's PATH", program)
		}
	}
	real, err := filepath.EvalSymlinks(found)
	if err != nil {
		return "", err
	}
	if filepath.Base(real) != "mise" {
		return found, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), claudeFindTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, real, "which", filepath.Base(program))
	cmd.Dir, cmd.Env = dir, append(os.Environ(), "PATH="+path)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("crew: mise which %s: %w", filepath.Base(program), err)
	}
	if which := strings.TrimSpace(string(out)); filepath.IsAbs(which) && !strings.Contains(which, "\n") {
		return which, nil
	}
	return "", fmt.Errorf("crew: mise which %s answered %q", filepath.Base(program), out)
}
