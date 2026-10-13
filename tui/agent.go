package tui

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc"
)

// agents owns profile forms and the running agent's identity. Its single panel
// remains one contribution even when the later agents model gains more tabs.
type agents struct {
	c                      agentHost
	agentDir, agentRunning string
	agentRunningLabel      string // label captured at start; agentRunning itself is the codename
	agentConfig            string
	agentBinPath           string
	editingAgent           string
	editingAgentProfile    *agentProfile // exact record shown in the form; stale edits are refused
	switchingAgent         string
	agentRows              *tuidecl.ListModel
	instances              map[string]*agentInstance
	instanceOrder          []string
	selectedAgent          string
	deliveries             map[*agentDelivery]bool
	stopping               bool
}

func newAgents(c agentHost, dir, config string) *agents {
	return &agents{c: c, agentDir: dir, agentConfig: config, instances: map[string]*agentInstance{}, deliveries: map[*agentDelivery]bool{},
		agentRows: tuidecl.NewListModel("key", "default", "name", "codename", "command")}
}
func (a *agents) BindPanelHost(c PanelHost) error {
	host, ok := c.(agentHost)
	if !ok {
		return fmt.Errorf("agents panel requires terminal configuration services")
	}
	a.c = host
	return nil
}
func (*agents) Name() string { return "agents" }
func (a *agents) State() map[string]any {
	st := map[string]any{
		"App.agentTitle":          "agent",
		"App.agentShown":          false,
		"App.agentEdges":          choices(agentEdgeLabels...),
		"App.agentSizes":          choices(percentLabels(termSizes)...),
		"App.agentLengths":        choices(percentLabels(termLengths)...),
		"App.agentProfiles":       a.agentRows,
		"App.agentProfilesStatus": "",
		"App.agentFormTitle":      "",
		"App.agentFormError":      "",
		"App.agentName":           "",
		"App.agentCodename":       "",
		"App.agentCommand":        "",
		"App.agentSwitchQuestion": "",
	}
	return st
}
func (a *agents) Prefs() []PrefSpec { return agentPrefRows(a) }

func (a *agents) Catalog() []Entry {
	return []Entry{
		builtin("agent.toggle", "AI agent", "App.toggleAgent()", a.toggleAgent),
		builtin("prefs.agent_profiles", "agent profiles", "App.openAgentProfiles()", a.openAgentProfiles),
	}
}

func agentPrefRows(a *agents) []PrefSpec {
	edge := choicePref(prefAgentEdge, defaultAgentEdge, agentEdges, func(p *prefs) *string { return &p.agentEdge }).
		shows(agentPreferenceState)
	profiles := ValuePref(prefAgentProfiles, "[]", nil)
	def := ValuePref(prefAgentDefault, "", nil)
	identity := ValuePref(prefAgentDefaultCodename, "", nil)
	if a != nil {
		edge = edge.then(func(prefs) { a.c.DockPanel("agent") })
		profiles = profiles.then(func(prefs) { a.listAgents() })
		def = def.then(func(prefs) { a.listAgents() })
		identity = identity.then(func(prefs) { a.listAgents() })
	}
	return []PrefSpec{edge, profiles, def, identity}
}

func agentPreferenceState(p prefs) map[string]any {
	st := agentState(p)
	size, length := p.panelGeometry("agent", p.agentEdge)
	maps.Copy(st, map[string]any{
		"App.agentEdgeIndex":   indexOf(agentEdges, p.agentEdge),
		"App.agentSizeIndex":   nearest(termSizes, size),
		"App.agentLengthIndex": nearest(termLengths, length),
	})
	return st
}

func (a *agents) profiles() []agentProfile {
	return agentProfilesOf(a.c.Prefs().Value(prefAgentProfiles))
}
func (a *agents) defaultProfile() string {
	return a.c.Prefs().Value(prefAgentDefaultCodename)
}
func (a *agents) Commands() map[string]Command {
	return map[string]Command{
		"App.toggleAgent":          none(a.toggleAgent),
		"App.openAgentProfiles":    none(a.openAgentProfiles),
		"App.startAddAgent":        none(a.startAddAgent),
		"App.startEditAgent":       oneNumber("a row", a.startEditAgent),
		"App.saveAgent":            twoStrings("a name and a command", a.saveAgent),
		"App.saveAgentProfile":     threeStrings("a name, command and codename", a.saveAgentProfile),
		"App.removeAgent":          oneNumber("a row", a.removeAgent),
		"App.makeAgentDefault":     oneNumber("a row", a.makeAgentDefault),
		"App.useAgent":             oneNumber("a row", a.useAgent),
		"App.switchAgentConfirmed": none(a.switchAgentConfirmed),
		"App.agentTabsChanged":     none(a.agentTabsChanged),
		"App.setAgentEdge":         oneNumber("a row", a.setAgentEdge),
		"App.setAgentSize":         oneNumber("a row", a.setAgentSize),
		"App.setAgentLength":       oneNumber("a row", a.setAgentLength),
	}
}
func (a *agents) Panels() []PanelSpec {
	s := agentPanelSpec()
	s.Open = func(PanelHost) { a.openAgentPanel() }
	s.Opened = func(c PanelHost) { c.Set("App.agentShown", true) }
	s.Closed = func(c PanelHost) { c.Set("App.agentShown", false); c.Keep(c.RestoreKeyboardPane("editor")) }
	return []PanelSpec{s}
}
func (a *agents) PreferencesChanged() {
	for name, value := range agentState(a.c.Prefs()) {
		a.c.Set(name, value)
	}
	a.syncAgentDialog()
}

// THE AGENT — an AI agent's CLI in a terminal of its own, floating over the page (ADR 1791213400):
// SPC g, Go › Agent. The user keeps profiles, each a name and the command that starts the agent
// (claude, codex --model o4, agy, opencode, …), and marks one the default: the key opens it. One
// agent runs at a time; it keeps running while hidden. The agent works in AutoDoc's own folder for
// the workspace (Options.AgentDir), never the workspace's: there AutoDoc writes AGENTS.md and
// CLAUDE.md, telling it the workspace and how to call AutoDoc, and its environment names them too.
// The command is the user's, as written; {root} and {workspace} in it are filled in.

// Profiles are JSON; the canonical default is a codename, with a legacy name mirror.
const (
	prefAgentProfiles        = "tui.agent.profiles"
	prefAgentDefault         = "tui.agent.default"
	prefAgentDefaultCodename = "tui.agent.default.codename"
)

// agentProfile is one way to start an agent.
type agentProfile struct {
	Name     string `json:"name"`
	Command  string `json:"command"`
	Codename string `json:"codename,omitempty"`
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
	return uniqueAgentCodenames(out)
}

func agentProfilesJSON(ps []agentProfile) string {
	if len(ps) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ps)
	return string(b)
}

// profile resolves the stable codename, never the display label.
func (a *agents) profile(codename string) (agentProfile, bool) {
	for _, a := range a.profiles() {
		if a.Codename == codename {
			return a, true
		}
	}
	return agentProfile{}, false
}

// toggleAgent is SPC g: the agent running shown or hidden; none running, the default started;
// no default, the profiles, which say what to do.
func (h *agents) toggleAgent() {
	h.c.TogglePanel("agent")
}

// openAgentPanel shows the running agent, or starts the default profile. With no
// default, it opens the profiles dialog instead of an empty terminal.
func (h *agents) openAgentPanel() {
	h.syncInstances()
	if instance := h.instances[h.selectedAgent]; instance != nil && instance.running {
		h.c.OpenDrawer("agent")
		h.c.Keep(h.selectInstance(instance))
		return
	}
	for _, instance := range h.instances {
		if instance.running {
			h.c.OpenDrawer("agent")
			h.c.Keep(h.selectInstance(instance))
			return
		}
	}
	p, ok := h.profile(h.defaultProfile())
	if !ok {
		h.openAgentProfiles()
		return
	}
	h.startAgent(p)
}

// startAgent starts p in the agent's terminal and shows it. The shell runs the command as written,
// so it decides what the command is: one it cannot find exits 127, which agentExited explains.
func (h *agents) startAgent(p agentProfile) {
	if p.Codename == "" {
		p.Codename = agentCodename(p.Name)
	}
	if _, err := h.startInstance(p); err != nil {
		h.c.Notify("the agent could not start: " + err.Error())
	}
}

// stopAgent releases the running identity only after the terminal accepts stop.
func (h *agents) stopAgent() bool {
	if h.selectedAgent == "" {
		return true
	}
	if err := h.removeInstance(h.selectedAgent); err != nil {
		h.c.Keep(err)
		return false
	}
	return true
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

// agentEnv are the variables the agent is started with, in its folder dir: the workspace, its root,
// the daemon the TUI is attached to, the build and the config it runs on, and a PATH whose first
// folder holds the autodoc that runs them (agentWrapper), so `autodoc --call` reaches the TUI's
// store, not the default config's, and is found when no autodoc is installed.
func (h *agents) agentEnv(ws wsInfo, dir string) []string {
	return []string{
		"AUTODOC_WORKSPACE=" + ws.name,
		"AUTODOC_ROOT=" + ws.root,
		"AUTODOC_SOCKET=" + h.c.DaemonAddress(),
		"AUTODOC_BIN=" + h.agentBin(),
		"AUTODOC_CONFIG=" + h.agentConfig,
		"PATH=" + filepath.Join(dir, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
}

// agentBin is the autodoc binary the agent calls: this one, unless a test says otherwise.
func (h *agents) agentBin() string {
	if h.agentBinPath != "" {
		return h.agentBinPath
	}
	bin, _ := os.Executable()
	return bin
}

// agentWrapper is the agent's autodoc: this build, on the TUI's config.
func agentWrapper(bin, config string) string {
	cmd := "exec " + shellQuote(bin)
	if config != "" {
		cmd += " --config " + shellQuote(config)
	}
	return "#!/bin/sh\n# AutoDoc as the TUI that started this agent runs it: its build, its config.\n" + cmd + ` "$@"` + "\n"
}

// agentDir is the folder the agent works in for a workspace: AutoDoc's, never the workspace's.
func (h *agents) agentFolder(ws wsInfo) string {
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
// most agent CLIs read one of the two from where they start; and bin/autodoc, agentWrapper. It
// returns the folder.
func (h *agents) writeAgentGuide(ws wsInfo) (string, error) {
	dir := h.agentFolder(ws)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
		return "", err
	}
	guide := agentGuide(ws, h.agentBin(), h.agentConfig, filepath.Join(dir, "bin", "autodoc"))
	for _, f := range []struct {
		name, text string
		mode       os.FileMode
	}{{"AGENTS.md", guide, 0o600}, {"CLAUDE.md", guide, 0o600}, {filepath.Join("bin", "autodoc"), agentWrapper(h.agentBin(), h.agentConfig), 0o700}} {
		path := filepath.Join(dir, f.name)
		tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
		if err := os.WriteFile(tmp, []byte(f.text), f.mode); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, path); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// agentGuide is what the agent is told: the workspace, what to help with, and how to call AutoDoc
// (its own AGENTS.md): as wrapper, the build bin on the TUI's config.
func agentGuide(ws wsInfo, bin, config, wrapper string) string {
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
	fmt.Fprintf(&b, "\n## Calling AutoDoc\n\n`autodoc` on your PATH is `%s`: it runs `%s` (`$AUTODOC_BIN`), the build the user runs", wrapper, bin)
	if config != "" {
		fmt.Fprintf(&b, ", with `--config %s` (`$AUTODOC_CONFIG`)", config)
	}
	b.WriteString(", so it reaches the same store and daemon as the TUI that started you. Call it as `autodoc`, as the ")
	b.WriteString("examples below do, never another autodoc. Search before you answer, and cite the paths you read.\n\n")
	b.WriteString("## What to help with\n\n")
	b.WriteString("- Finding documents and answering from them: `search.query`, then `doc.read`.\n")
	b.WriteString("- Editing the user's files under the root, when asked. AutoDoc follows the edits, as it follows any editor's.\n")
	b.WriteString("- Keeping search precise: `.view` files and the frontmatter schema, so a term means one thing and is not diluted across unrelated files.\n")
	b.WriteString("- Explaining AutoDoc's state: `index.status`, `workspace.list`, `sys.capabilities`.\n\n")
	b.WriteString("---\n\n")
	b.WriteString(autodoc.AgentsGuide)
	return b.String()
}
