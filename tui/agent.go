package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/autodoc"
)

// THE AGENT — an AI agent's CLI in a terminal of its own, floating over the page (ADR 1791213400):
// SPC ~, View › Agent. The user keeps profiles, each a name and the command that starts the agent
// (claude, codex --model o4, agy, opencode, …), and marks one the default: the key opens it. One
// agent runs at a time; it keeps running while hidden. The agent works in AutoDoc's own folder for
// the workspace (Options.AgentDir), never the workspace's: there AutoDoc writes AGENTS.md and
// CLAUDE.md, telling it the workspace and how to call AutoDoc, and its environment names them too.
// The command is the user's, as written; {root} and {workspace} in it are filled in.

// The agent's preferences: the profiles, as JSON, and the default's name.
const (
	prefAgentProfiles = "tui.agent.profiles"
	prefAgentDefault  = "tui.agent.default"
)

// agentProfile is one way to start an agent.
type agentProfile struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// agentProfilesOf reads the profiles preference; one without a name or a command is dropped.
func agentProfilesOf(s string) []agentProfile {
	var all []agentProfile
	if json.Unmarshal([]byte(s), &all) != nil {
		return nil
	}
	out := all[:0]
	for _, p := range all {
		if strings.TrimSpace(p.Name) != "" && strings.TrimSpace(p.Command) != "" {
			out = append(out, p)
		}
	}
	return out
}

func agentProfilesJSON(ps []agentProfile) string {
	if len(ps) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ps)
	return string(b)
}

// agentProfile is the profile named name, ok false when there is none.
func (p prefs) agentProfile(name string) (agentProfile, bool) {
	for _, a := range p.agents {
		if a.Name == name {
			return a, true
		}
	}
	return agentProfile{}, false
}

// toggleAgent is SPC ~: the agent running shown or hidden; none running, the default started;
// no default, the profiles, which say what to do.
func (h *Host) toggleAgent() {
	if h.panelOpen["agent"] {
		h.keep(h.p.Call("agent", "close"))
		return
	}
	if h.agentRunning != "" {
		h.keep(h.p.Call("agent", "open"))
		h.keep(h.p.Call("agentView", "forceActiveFocus"))
		return
	}
	p, ok := h.prefs.agentProfile(h.prefs.agentDefault)
	if !ok {
		h.openAgentProfiles()
		return
	}
	h.startAgent(p)
}

// startAgent starts p in the agent's terminal and shows it. A command whose program is not
// installed is refused before anything starts.
func (h *Host) startAgent(p agentProfile) {
	ws, ok := h.activeWorkspaceInfo()
	if !ok {
		h.say("the agent works in a workspace: open one first")
		return
	}
	command := fillAgentCommand(p.Command, ws)
	if first := strings.Fields(command); len(first) == 0 {
		h.say("the profile " + p.Name + " has no command")
		return
	} else if _, err := exec.LookPath(first[0]); err != nil {
		h.notify(p.Name + ": " + first[0] + " is not installed (not on PATH)")
		return
	}
	dir, err := h.writeAgentGuide(ws)
	if err != nil {
		h.notify("the agent's folder could not be written: " + err.Error())
		return
	}
	path, args, err := agentArgv(command, h.agentEnv(ws))
	if err != nil {
		h.notify("the agent could not start: " + err.Error())
		return
	}
	if c, ok := h.p.Find("agentView"); ok {
		if t, ok := c.(interface {
			SetCommand(string, ...string)
			SetDir(string)
		}); ok {
			t.SetCommand(path, args...)
			t.SetDir(dir)
		}
	}
	h.set("App.agentTitle", "agent · "+p.Name)
	h.keep(h.p.Call("agent", "open"))
	if err := h.p.Call("agentView", "start"); err != nil {
		h.notify("the agent could not start (" + err.Error() + ")")
		return
	}
	h.agentRunning = p.Name
	h.keep(h.p.Call("agentView", "forceActiveFocus"))
}

// stopAgent ends the agent running, if one is.
func (h *Host) stopAgent() {
	if h.agentRunning == "" {
		return
	}
	h.keep(h.p.Call("agentView", "stop"))
	h.agentRunning = ""
}

// agentExited is the agent's program ending: the next SPC ~ starts the default again.
func (h *Host) agentExited(code int) {
	name := h.agentRunning
	h.agentRunning = ""
	if name != "" {
		h.notify(fmt.Sprintf("the agent %s exited (%d): SPC ~ starts the default again", name, code))
	}
}

// fillAgentCommand fills {root} and {workspace} in a profile's command, each quoted for the shell.
func fillAgentCommand(command string, ws wsInfo) string {
	return strings.NewReplacer("{root}", shellQuote(ws.root), "{workspace}", shellQuote(ws.name)).Replace(command)
}

// shellQuote is s as one shell word.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// agentArgv is how the terminal runs command: env, setting AutoDoc's variables, running the shell
// on the command as the user wrote it, so its quoting and arguments are the shell's.
func agentArgv(command string, env []string) (string, []string, error) {
	envPath, err := exec.LookPath("env")
	if err != nil {
		return "", nil, err
	}
	shell := "/bin/sh"
	args := append(append([]string{}, env...), shell, "-c", command)
	return envPath, args, nil
}

// agentEnv are the variables the agent is started with: the workspace, its root, the daemon the
// TUI is attached to, and the autodoc binary, so `autodoc --call` reaches the same build.
func (h *Host) agentEnv(ws wsInfo) []string {
	bin, _ := os.Executable()
	return []string{
		"AUTODOC_WORKSPACE=" + ws.name,
		"AUTODOC_ROOT=" + ws.root,
		"AUTODOC_SOCKET=" + h.session.addr,
		"AUTODOC_BIN=" + bin,
	}
}

// agentDir is the folder the agent works in for a workspace: AutoDoc's, never the workspace's.
func (h *Host) agentFolder(ws wsInfo) string {
	base := h.agentDir
	if base == "" {
		base = filepath.Join(os.TempDir(), "autodoc-agent")
	}
	return filepath.Join(base, safeName(ws.name))
}

// safeName is a workspace's name as a folder's.
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, s)
}

// writeAgentGuide writes AGENTS.md, and CLAUDE.md as the same text, into the agent's folder for ws:
// most agent CLIs read one of the two from where they start. It returns the folder.
func (h *Host) writeAgentGuide(ws wsInfo) (string, error) {
	dir := h.agentFolder(ws)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	guide := agentGuide(ws, "")
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		tmp := filepath.Join(dir, "."+name+".tmp")
		if err := os.WriteFile(tmp, []byte(guide), 0o600); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// agentGuide is what the agent is told: the workspace, what to help with, and how to call AutoDoc
// (its own AGENTS.md).
func agentGuide(ws wsInfo, bin string) string {
	if bin == "" {
		bin, _ = os.Executable()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# You are working in AutoDoc\n\n")
	fmt.Fprintf(&b, "You were started from AutoDoc, a search engine and editor over the user's files. ")
	fmt.Fprintf(&b, "This folder is AutoDoc's, not the user's: the user's files are in the workspace below.\n\n")
	fmt.Fprintf(&b, "## The workspace\n\n")
	fmt.Fprintf(&b, "- name: `%s` (also `$AUTODOC_WORKSPACE`)\n", ws.name)
	fmt.Fprintf(&b, "- root: `%s` (also `$AUTODOC_ROOT`): the user's files, by path under it\n", ws.root)
	if len(ws.include) > 0 {
		fmt.Fprintf(&b, "- indexed: `%s`", strings.Join(ws.include, "`, `"))
		if len(ws.exclude) > 0 {
			fmt.Fprintf(&b, ", except `%s`", strings.Join(ws.exclude, "`, `"))
		}
		b.WriteString("\n")
	}
	if ws.schema.path != "" {
		fmt.Fprintf(&b, "- frontmatter schema: `%s`\n", ws.schema.path)
	}
	if len(ws.textExtensions) > 0 {
		fmt.Fprintf(&b, "- also read as text: `%s`\n", strings.Join(ws.textExtensions, "`, `"))
	}
	fmt.Fprintf(&b, "\n## Calling AutoDoc\n\nThe `autodoc` below is `%s` (also `$AUTODOC_BIN`), the build the user runs, ", bin)
	b.WriteString("attached to the same daemon. Search before you answer, and cite the paths you read.\n\n")
	b.WriteString("## What to help with\n\n")
	b.WriteString("- Finding documents and answering from them: `search.query`, then `doc.read`.\n")
	b.WriteString("- Editing the user's files under the root, when asked. AutoDoc follows the edits, as it follows any editor's.\n")
	b.WriteString("- Keeping search precise: `.view` files and the frontmatter schema, so a term means one thing and is not diluted across unrelated files.\n")
	b.WriteString("- Explaining AutoDoc's state: `index.status`, `workspace.list`, `sys.capabilities`.\n\n")
	b.WriteString("---\n\n")
	b.WriteString(autodoc.AgentsGuide)
	return b.String()
}
