package crew

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
// event isn't published where a host can read it. The known limit: a literal the binary holds for
// another reason reads as an event it knows, and naming that event would bring back the warning;
// SessionStart, which every Claude Code knows, is required, so a file that isn't one is refused.
// The answer is kept by the binary's path, size and modification time, so an update is read again;
// path's links are followed.
func ClaudeKnows(path string) (ClaudeHooks, error) { return ClaudeKnowsIn(path, "") }

// ClaudeKnowsIn is ClaudeKnows keeping its answers in the file cache too, for a process that runs
// once (`autodoc crew claude-settings`): a binary unchanged since an earlier run costs a stat. ""
// keeps them in memory alone. A cache that can't be read or written is read past.
func ClaudeKnowsIn(path, cache string) (ClaudeHooks, error) {
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
	var kept bool
	if r.hooks, kept = cachedClaudeHooks(cache, key); !kept {
		if r.hooks, r.err = readClaudeHooks(real); r.err == nil {
			keepClaudeHooks(cache, key, r.hooks)
		}
	}
	if r.err != nil {
		r.err = fmt.Errorf("crew: %s: %w", path, r.err)
		claudeKnownMu.Lock()
		delete(claudeKnown, key)
		claudeKnownMu.Unlock()
	}
	close(r.done)
	return r.hooks, r.err
}

// claudeScanChunk is how much of a binary readClaudeHooks reads at once: the binary is hundreds of
// megabytes, never read whole.
const claudeScanChunk = 1 << 20

// claudeLiterals are the quoted literals readClaudeHooks looks for, by the event or setting each
// names.
func claudeLiterals() map[string][]byte {
	out := map[string][]byte{}
	for _, e := range append([]string{ClaudeSessionStart, ClaudeCrossSessionInbound}, claudePromptHooks...) {
		out[e] = []byte(`"` + e + `"`)
	}
	return out
}

// readClaudeHooks reads ClaudeKnows' answer from the binary at path, chunk by chunk: each chunk is
// searched with the end of the one before it (a literal's length but one), so a literal across two
// chunks is found, and the read stops once every literal was.
func readClaudeHooks(path string) (ClaudeHooks, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	lits := claudeLiterals()
	longest := 0
	for _, l := range lits {
		longest = max(longest, len(l))
	}
	k := ClaudeHooks{}
	buf := make([]byte, longest-1+claudeScanChunk)
	carry := 0
	for len(k) < len(lits) {
		n, err := io.ReadFull(f, buf[carry:])
		window := buf[:carry+n]
		for e, l := range lits {
			if !k[e] && bytes.Contains(window, l) {
				k[e] = true
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}
		carry = min(longest-1, len(window))
		copy(buf, window[len(window)-carry:])
	}
	for e := range lits {
		k[e] = k[e] // every event answered, known or not
	}
	if !k[ClaudeSessionStart] {
		return nil, fmt.Errorf("names no SessionStart hook: not a Claude Code binary")
	}
	return k, nil
}

// claudeCacheLimit is how many binaries a ClaudeKnowsIn cache file holds.
const claudeCacheLimit = 16

// claudeCached is one binary's answer in a cache file.
type claudeCached struct {
	Path  string          `json:"path"`
	Size  int64           `json:"size"`
	MTime int64           `json:"mtime"`
	Knows map[string]bool `json:"knows"`
}

func readClaudeCache(cache string) []claudeCached {
	var all []claudeCached
	if b, err := os.ReadFile(cache); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	return all
}

// cachedClaudeHooks is key's answer in cache, if it holds one.
func cachedClaudeHooks(cache string, key claudeBinary) (ClaudeHooks, bool) {
	if cache == "" {
		return nil, false
	}
	for _, c := range readClaudeCache(cache) {
		if c.Path == key.path && c.Size == key.size && c.MTime == key.mtime && c.Knows[ClaudeSessionStart] {
			return ClaudeHooks(c.Knows), true
		}
	}
	return nil, false
}

// keepClaudeHooks writes key's answer into cache, in place of the path's earlier one, the oldest
// dropped past claudeCacheLimit, through a temporary file renamed over it.
func keepClaudeHooks(cache string, key claudeBinary, k ClaudeHooks) {
	if cache == "" {
		return
	}
	all := slices.DeleteFunc(readClaudeCache(cache), func(c claudeCached) bool { return c.Path == key.path })
	all = append(all, claudeCached{Path: key.path, Size: key.size, MTime: key.mtime, Knows: k})
	if len(all) > claudeCacheLimit {
		all = all[len(all)-claudeCacheLimit:]
	}
	b, err := json.Marshal(all)
	if err != nil || os.MkdirAll(filepath.Dir(cache), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(cache), ".claude-hooks-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), cache) != nil {
		_ = os.Remove(tmp.Name())
	}
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

// claudeFindTimeout bounds ClaudeOf's one run of mise.
const claudeFindTimeout = 5 * time.Second

// ClaudeOf is the Claude Code binary program runs, and what it knows (ClaudeKnowsIn, cache), found
// as a shell in dir with PATH path finds it: a path (relative to dir) is that file; a name is the
// first file on path that reads as a Claude Code. A mise shim (a link to mise) is the tool mise runs
// for it there (`mise which`). A file that doesn't read as one (a wrapper script) is taken to run the
// same CLI as mise's or a later one on path, which are tried next: mise's tool for the name, then
// the later files on path. The error names every file tried.
func ClaudeOf(program, path, dir, cache string) (string, ClaudeHooks, error) {
	if strings.Contains(program, "/") {
		bin := program
		if !filepath.IsAbs(bin) {
			bin = filepath.Join(dir, bin)
		}
		k, err := ClaudeKnowsIn(bin, cache)
		return bin, k, err
	}
	hits := lookAll(program, path, dir)
	if len(hits) == 0 {
		return "", nil, fmt.Errorf("crew: %s is not on the agent's PATH", program)
	}
	mise := lookAll("mise", path, dir)
	var tried []string
	seen := map[string]bool{}
	try := func(bin string) (ClaudeHooks, bool) {
		real, err := filepath.EvalSymlinks(bin)
		if err != nil || seen[real] {
			return nil, false
		}
		seen[real] = true
		tried = append(tried, bin)
		k, err := ClaudeKnowsIn(bin, cache)
		return k, err == nil
	}
	triedMise := false
	which := func(misebin string) string {
		triedMise = true
		w, err := miseWhich(misebin, program, path, dir)
		if err != nil {
			tried = append(tried, "mise which "+program+" ("+err.Error()+")")
			return ""
		}
		return w
	}
	for i, hit := range hits {
		real, err := filepath.EvalSymlinks(hit)
		if err != nil {
			continue
		}
		bin := hit
		if filepath.Base(real) == "mise" { // a shim: the tool mise runs for it
			if bin = which(real); bin == "" {
				continue
			}
		}
		if k, ok := try(bin); ok {
			return bin, k, nil
		}
		if i == 0 && !triedMise && len(mise) > 0 { // a wrapper first: mise's own, before the later ones
			if w := which(mise[0]); w != "" {
				if k, ok := try(w); ok {
					return w, k, nil
				}
			}
		}
	}
	return "", nil, fmt.Errorf("crew: none of %s reads as a Claude Code", strings.Join(tried, ", "))
}

// lookAll is every executable file named name on path, in order, a relative folder taken from dir.
func lookAll(name, path, dir string) []string {
	var out []string
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			d = "."
		}
		if !filepath.IsAbs(d) {
			d = filepath.Join(dir, d)
		}
		f := filepath.Join(d, name)
		if fi, err := os.Stat(f); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// miseWhich is the tool mise (at bin) runs for program in dir: `mise which`, bounded.
func miseWhich(bin, program, path, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeFindTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "which", program)
	cmd.Dir, cmd.Env = dir, append(os.Environ(), "PATH="+path)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	if w := strings.TrimSpace(string(out)); filepath.IsAbs(w) && !strings.Contains(w, "\n") {
		return w, nil
	}
	return "", fmt.Errorf("answered %q", out)
}
