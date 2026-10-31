package tui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc"
	"github.com/yongjohnlee80/autodoc/core/crew"
)

// agents owns profile forms and the running agent's identity. Its single panel
// remains one contribution even when the later agents model gains more tabs.
type agents struct {
	c                      agentHost
	agentDir, agentRunning string
	agentRunningLabel      string // label captured at start; agentRunning itself is the codename
	agentConfig            string
	crew                   *crewHost  // the window's crew scope; nil in a program without one
	inbox                  *crewInbox // the human's crew inbox: which agents wait on a prompt; nil without one
	// promptsReported replaces crew.ReportsPrompts (a cell's: a kind whose adapter reports its
	// permission prompts, so the messaging base may press Enter); nil in production
	promptsReported func(kind string) bool
	// spawn starts an agent's program on a pseudo-terminal (runAgent): pty.Start, as a terminal
	// tab's own start is
	spawn        func(pty.Cmd) (widget.TerminalProcess, error)
	agentBinPath string
	// pluginEnv replaces opencodeEnv's run of the agent's autodoc (a cell's); nil in production
	pluginEnv func() (string, error)
	// pluginAnswer is the process's one run of it (warmPluginEnv), on the loop
	pluginAnswer *pluginEnvAnswer
	// codexHere replaces registerCodex's runs of the agent's autodoc (a cell's); nil in production
	codexHere  func(args ...string) error
	editorPath func() string // the TUI's editor socket, "" without one (editorsocket.go)
	// editorTokens are the agents' secrets on the editor socket, by codename: made at an agent's
	// first start in this run, and what the socket knows an agent by (editorsocket.go)
	editorTokens        map[string]string
	editingAgent        string
	editingAgentProfile *agentProfile // exact record shown in the form; stale edits are refused
	// the setup card (agentsetup.go): the CLIs it lists
	setupFound     []agentCLI
	setupCLIs      *tuidecl.ListModel
	switchingAgent string
	agentRows      *tuidecl.ListModel
	instances      map[string]*agentInstance
	instanceOrder  []string
	selectedAgent  string
	deliveries     map[*agentDelivery]bool
	stopping       bool
}

func newAgents(c agentHost, dir, config string) *agents {
	return &agents{c: c, agentDir: dir, agentConfig: config, instances: map[string]*agentInstance{}, deliveries: map[*agentDelivery]bool{},
		agentRows: tuidecl.NewListModel("key", "default", "name", "codename", "command"),
		spawn:     func(c pty.Cmd) (widget.TerminalProcess, error) { return pty.Start(c) }}
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
		"App.agentSetupCLIs": a.setupList(), "App.agentSetupIndex": 0, "App.agentSetupName": "", "App.agentSetupNone": false,
		"App.agentSetupSome": false, "App.agentSetupReject": "&No",
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
		builtin("agent.setup", "set up an agent", "App.openAgentSetup()", a.openAgentSetup),
	}
}

func agentPrefRows(a *agents) []PrefSpec {
	edge := choicePref(prefAgentEdge, defaultAgentEdge, agentEdges, func(p *prefs) *string { return &p.agentEdge }).
		shows(agentPreferenceState)
	profiles := ValuePref(prefAgentProfiles, "[]", nil)
	def := ValuePref(prefAgentDefault, "", nil)
	identity := ValuePref(prefAgentDefaultCodename, "", nil)
	setup := ValuePref(prefAgentSetup, "", nil) // the setup's answer (agentsetup.go)
	if a != nil {
		edge = edge.then(func(prefs) { a.c.DockPanel("agent") })
		profiles = profiles.then(func(prefs) { a.listAgents() })
		def = def.then(func(prefs) { a.listAgents() })
		identity = identity.then(func(prefs) { a.listAgents() })
	}
	return []PrefSpec{edge, profiles, def, identity, setup}
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
		"App.stopAllAgents":        none(a.stopAllAgents),
		"App.openAgentProfiles":    none(a.openAgentProfiles),
		"App.startAddAgent":        none(a.startAddAgent),
		"App.startEditAgent":       oneNumber("a row", a.startEditAgent),
		"App.saveAgent":            twoStrings("a name and a command", a.saveAgent),
		"App.saveAgentProfile":     threeStrings("a name, command and codename", a.saveAgentProfile),
		"App.removeAgent":          oneNumber("a row", a.removeAgent),
		"App.makeAgentDefault":     oneNumber("a row", a.makeAgentDefault),
		"App.openAgentSetup":       none(a.openAgentSetup),
		"App.agentSetupPicked":     numberAndString("a row and the name", a.agentSetupPicked),
		"App.agentSetupConfirm":    numberAndString("a row and the name", a.agentSetupConfirm),
		"App.agentSetupDecline":    none(a.agentSetupDecline),
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
	s.Closed = func(c PanelHost) { c.Set("App.agentShown", false); c.Keep(c.RestoreKeyboardHome()) }
	return []PanelSpec{s}
}
func (a *agents) PreferencesChanged() {
	for name, value := range agentState(a.c.Prefs()) {
		a.c.Set(name, value)
	}
	a.syncAgentDialog()
}

// THE AGENT — an AI agent's CLI in a terminal of its own, floating over the page
// (docs/decisions/crew.md#an-agent-terminal-inside-autodoc): SPC g, Go › Agent. The user keeps
// profiles, each a name and the command that starts the agent (claude, codex --model o4, agy,
// opencode, …), and marks one the default: the key opens it. Each running agent has a tab of its
// own (agenttabs.go); they keep running while hidden. The agent works in AutoDoc's own folder for
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
		h.c.Notify(h.c.Trf("autodoc.notice.agent.the_agent_could_not_start", err.Error()))
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

// agentKind is the crew kind of the CLI command runs (claude, codex, agy, opencode, …): the name of
// the program its first simple command runs, its words read as the shell reads them, past any
// NAME=value assignments and the wrappers env, exec, command, nohup and mise exec|x … --; "agent"
// when there is none. A wrapper not named here is the kind itself, never a vendor's guessed.
func agentKind(command string) string {
	w := shellWords(command)
	for len(w) > 0 {
		f := w[0].text
		switch {
		case w[0].assignment():
			w = w[1:]
		case f == "exec" || f == "command" || f == "nohup":
			w = skipOptions(w[1:], "-a")
		case filepath.Base(f) == "env":
			w = skipEnvOptions(w[1:])
		case filepath.Base(f) == "mise" && len(w) > 1 && (w[1].text == "exec" || w[1].text == "x"):
			i := slices.IndexFunc(w, func(x shellWord) bool { return x.text == "--" })
			if i < 0 {
				return "mise" // its tools run their own default program: none of them guessed
			}
			w = w[i+1:]
		default:
			if name := filepath.Base(f); name != "" && name != "." && name != "/" {
				return name
			}
			return "agent"
		}
	}
	return "agent"
}

// skipOptions is w past its leading options and a "--"; an option in valued takes the next word.
func skipOptions(w []shellWord, valued ...string) []shellWord {
	for len(w) > 0 && strings.HasPrefix(w[0].text, "-") {
		f := w[0].text
		w = w[1:]
		if f == "--" {
			break
		}
		if slices.Contains(valued, f) && len(w) > 0 {
			w = w[1:]
		}
	}
	return w
}

// skipEnvOptions is w, env's arguments, past its options: -S's string is split into the words it
// runs, as env splits it.
func skipEnvOptions(w []shellWord) []shellWord {
	for len(w) > 0 && strings.HasPrefix(w[0].text, "-") {
		f := w[0].text
		switch {
		case f == "--":
			return w[1:]
		case f == "-S" || f == "--split-string":
			if len(w) < 2 {
				return nil
			}
			return append(shellWords(w[1].text), w[2:]...)
		case strings.HasPrefix(f, "--split-string="):
			return append(shellWords(strings.TrimPrefix(f, "--split-string=")), w[1:]...)
		case strings.HasPrefix(f, "-S"):
			return append(shellWords(f[2:]), w[1:]...)
		case f == "-u" || f == "-C" || f == "--unset" || f == "--chdir":
			if len(w) < 2 {
				return nil
			}
			w = w[2:]
		default: // -i, -0, -v, -, -uNAME, -CDIR, --unset=NAME, --chdir=DIR, …
			w = w[1:]
		}
	}
	return w
}

// shellWord is one word of a command as the shell splits it, its quotes and escapes removed; bare
// is how many of its leading bytes stood unquoted.
type shellWord struct {
	text string
	bare int
}

// assignment reports whether the word is a NAME=value assignment: its NAME= unquoted, as the shell
// requires, whatever quoting its value has.
func (w shellWord) assignment() bool {
	i := strings.IndexByte(w.text, '=')
	if i <= 0 || i >= w.bare {
		return false
	}
	for j, c := range w.text[:i] {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// shellWords splits command into words as a POSIX shell does before expanding them: blanks between
// words, '…' literal, "…" with \ escaping $ ` " \ and a newline, and \ outside quotes escaping the
// next byte. Expansions are left as written, and an unterminated quote runs to the end; nothing is
// evaluated.
func shellWords(command string) []shellWord {
	var out []shellWord
	var b strings.Builder
	in, bare := false, -1
	quoted := func() { // a quote or an escape: the word's bare prefix ends here
		in = true
		if bare < 0 {
			bare = b.Len()
		}
	}
	end := func() {
		if !in {
			return
		}
		if bare < 0 {
			bare = b.Len()
		}
		out = append(out, shellWord{text: b.String(), bare: bare})
		b.Reset()
		in, bare = false, -1
	}
	for i := 0; i < len(command); i++ {
		switch c := command[i]; c {
		case ' ', '\t', '\n':
			end()
		case '\'':
			quoted()
			j := strings.IndexByte(command[i+1:], '\'')
			if j < 0 {
				j = len(command) - i - 1
			}
			b.WriteString(command[i+1 : i+1+j])
			i += j + 1
		case '"':
			quoted()
			for i++; i < len(command) && command[i] != '"'; i++ {
				if command[i] == '\\' && i+1 < len(command) && strings.IndexByte("$`\"\\\n", command[i+1]) >= 0 {
					i++
					if command[i] == '\n' {
						continue
					}
				}
				b.WriteByte(command[i])
			}
		case '\\':
			quoted()
			if i+1 < len(command) {
				i++
				if command[i] != '\n' {
					b.WriteByte(command[i])
				}
			}
		default:
			in = true
			b.WriteByte(c)
		}
	}
	end()
	return out
}

// agentArgv is how the terminal runs command: env, dropping the identity another agent host gave
// the TUI (spawnerIdentity) and setting AutoDoc's variables, running the shell on the command as
// the user wrote it, so its quoting and arguments are the shell's.
func agentArgv(command string, env []string) (string, []string, error) {
	envPath, err := exec.LookPath("env")
	if err != nil {
		return "", nil, err
	}
	shell := "/bin/sh"
	var args []string
	for _, name := range spawnerIdentity(os.Environ()) {
		args = append(args, "-u", name)
	}
	args = append(append(args, env...), shell, "-c", command)
	return envPath, args, nil
}

// withEnvArg is agentArgv's args with kv set for the program as well: among its variables, before
// the shell that runs the command (the args end with the shell, -c and the command).
func withEnvArg(args []string, kv string) []string {
	i := max(len(args)-3, 0)
	return slices.Concat(args[:i:i], []string{kv}, args[i:])
}

// agentEnv are the variables the agent codename is started with, in its folder dir: the workspace,
// its root, the daemon the TUI is attached to, the build and the config it runs on, and a PATH whose
// first folder holds the autodoc that runs them (agentWrapper), so `autodoc --call` reaches the TUI's
// store, not the default config's, and is found when no autodoc is installed; and, with the editor
// socket, where the page is (AUTODOC_EDITOR) and who the agent is there (AUTODOC_AGENT).
func (h *agents) agentEnv(ws wsInfo, dir, codename string) []string {
	env := []string{
		"AUTODOC_WORKSPACE=" + ws.name,
		"AUTODOC_ROOT=" + ws.root,
		"AUTODOC_SOCKET=" + h.c.DaemonAddress(),
		"AUTODOC_BIN=" + h.agentBin(),
		"AUTODOC_CONFIG=" + h.agentConfig,
		"PATH=" + filepath.Join(dir, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	// the page in this TUI's editor, for `autodoc --editor`
	// (docs/decisions/crew.md#the-editor-socket)
	if h.editorPath != nil {
		if path := h.editorPath(); path != "" {
			env = append(env, "AUTODOC_EDITOR="+path, "AUTODOC_AGENT="+codename, "AUTODOC_EDITOR_TOKEN="+h.editorToken(codename))
		}
	}
	return env
}

// editorToken is codename's secret on the editor socket: random, made once a run.
func (h *agents) editorToken(codename string) string {
	if h.editorTokens == nil {
		h.editorTokens = map[string]string{}
	}
	if t, ok := h.editorTokens[codename]; ok {
		return t
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand: never fails on the platforms AutoDoc runs on
	t := hex.EncodeToString(b)
	h.editorTokens[codename] = t
	return t
}

// agentOfToken is the codename whose editor token t is; "" for none.
func (h *agents) agentOfToken(t string) string {
	for codename, tok := range h.editorTokens {
		if t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(tok)) == 1 {
			return codename
		}
	}
	return ""
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

// agentFolder is the folder codename's agent works in for ws: AutoDoc's, never the workspace's. While
// the window holds a crew scope it is the agent's own, under the scope, the same across that agent's
// sessions so its CLI keeps one project folder; else the workspace's, shared by its profiles.
func (h *agents) agentFolder(ws wsInfo, codename string) string {
	dir := filepath.Join(agentBase(h.agentDir), safeName(ws.name))
	if h.crew == nil {
		return dir
	}
	if scope, _, ok := h.crew.held(); ok {
		return filepath.Join(dir, safeName(scope), safeName(codename))
	}
	return dir
}

// agentBase is the root of the agents' folders: the configured one, else a folder in the temp dir.
func agentBase(dir string) string {
	if dir == "" {
		return filepath.Join(os.TempDir(), "autodoc-agent")
	}
	return dir
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

// writeAgentGuide writes AGENTS.md, and CLAUDE.md as the same text, into codename's folder for ws
// (agentFolder): most agent CLIs read one of the two from where they start; bin/autodoc,
// agentWrapper; and the workspace notice hook into .claude/settings.json (agenthook.go). A crew
// agent's guide (crewKind its CLI's kind, "" for an agent outside a crew scope) holds the crew and
// mail procedure for that CLI, so it registers how it is woken. It returns the folder.
func (h *agents) writeAgentGuide(ws wsInfo, codename, crewKind string) (string, error) {
	dir := h.agentFolder(ws, codename)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
		return "", err
	}
	guide := agentGuide(ws, h.agentBin(), h.agentConfig, filepath.Join(dir, "bin", "autodoc"), crewKind)
	for _, f := range []struct {
		name, text string
		mode       os.FileMode
	}{{"AGENTS.md", guide, 0o600}, {"CLAUDE.md", guide, 0o600}, {filepath.Join("bin", "autodoc"), agentWrapper(h.agentBin(), h.agentConfig), 0o700}} {
		if err := writeAtomic(filepath.Join(dir, f.name), []byte(f.text), f.mode); err != nil {
			return "", err
		}
	}
	// the workspace notice, for a Claude agent (agenthook.go): without it the agent still starts, and
	// asks editor.workspace by the guide's rule
	h.c.Keep(mergeClaudeHook(filepath.Join(dir, ".claude", "settings.json"), noticeCommand(dir)))
	return dir, nil
}

// writeAtomic writes path as text, mode mode, through a temporary file beside it: a reader sees the
// old file or the new one, never part of one.
func writeAtomic(path string, text []byte, mode os.FileMode) error {
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, text, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // an old temporary file keeps its mode through WriteFile
		return err
	}
	return os.Rename(tmp, path)
}

// agentGuide is what the agent is told: the workspace, what to help with, and how to call AutoDoc
// (its own AGENTS.md): as wrapper, the build bin on the TUI's config. A crew agent (crewKind its
// CLI's kind) is also told the crew and mail procedure for its CLI (crew.GuideOf): who it is, how it
// registers to be woken, and how it handles mail.
func agentGuide(ws wsInfo, bin, config, wrapper, crewKind string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# You are working in AutoDoc\n\n")
	fmt.Fprintf(&b, "You were started from AutoDoc, a search engine and editor over the user's files. ")
	fmt.Fprintf(&b, "This folder is AutoDoc's, not the user's: the user's files are in the workspace below.\n\n")
	// a crew agent's mail comes first, before anything it might read past: an agent told how only
	// deep in the reference spent its first turn finding out
	if crewKind != "" {
		b.WriteString("## Mail and your crew\n\n")
		b.WriteString(crew.GuideOf(crewKind).Markdown)
		b.WriteString("\n")
	}
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
	b.WriteString(editorGuide)
	b.WriteString("---\n\n")
	b.WriteString(autodoc.AgentsGuide)
	return b.String()
}

// editorGuide is the agents' guide to the page in the editor (editorsocket.go,
// docs/decisions/crew.md#the-editor-socket).
const editorGuide = "## The page the user is writing\n\n" +
	"`autodoc --editor <verb> '[params]'` reaches the page open in the AutoDoc that started you, its unsaved text " +
	"included (`$AUTODOC_EDITOR`; it knows you by `$AUTODOC_EDITOR_TOKEN`, which `autodoc --editor` passes). Positions are 1-based lines and columns (a column is a character).\n\n" +
	"- `editor.document`: path, kind, modified, revision, cursor, selection; `editor.buffers`: every open buffer.\n" +
	"- `editor.workspace`: the workspace you were started in (`spawn_workspace`), the one the editor is in now " +
	"(`workspace_in_use`), and, when the editor has moved since you last asked, the `switch` from the one you last " +
	"saw, with a `notice` line. It needs no permission.\n" +
	"- `editor.read '[from_line, to_line]'`, `editor.find '[\"term\", {\"ignore_case\": true}]'`.\n" +
	"- `editor.insert '[line, col, \"text\", revision]'`, `editor.replace '[l1, c1, l2, c2, \"text\", revision]'`, " +
	"`editor.delete '[l1, c1, l2, c2, revision]'`: pass the revision you read, so an edit of a page the user has " +
	"changed since is refused rather than misplaced. Each edit is one undo step for the user.\n" +
	"- `editor.commands`: every command, its `:` name and aliases, its arguments and what it does, Lua and plugin " +
	"commands included. It needs no permission. Check it before you run a command, rather than guess one.\n" +
	"- `editor.run '[\"command line\"]'`: a `:` command, as the user would type it, such as `:open <path>`.\n" +
	"- `editor.open '[\"path\"]'`: open a file, its path absolute or relative to the root of the workspace the " +
	"editor is in (`workspace_in_use`). It needs the `commands` permission, as `editor.run` does: the first call " +
	"asks the user, and is refused with `needs commands` until they allow it. It answers once the open is on its " +
	"way, not once the file is open: the user may be asked first (unsaved changes), so read `editor.document` " +
	"until it shows the file before you act on it.\n" +
	"- `editor.add_command '[\"name\", \"lua source\"]'`: offer the user a Lua command (docs/lua); it is added " +
	"to their lua/ folder only if they say yes.\n\n" +
	"The user can switch the editor to another workspace while you work. You stay in yours: `$AUTODOC_WORKSPACE` and " +
	"`$AUTODOC_ROOT` don't change. Before you act on each message from the user, run `autodoc --editor editor.workspace`, " +
	"and when it has a `switch`, tell the user what changed (a Claude Code session in this folder is told by a hook " +
	"instead: its `[AutoDoc]` line arrives with the message). When `editor.document` says `foreign_workspace`, its path " +
	"is relative to that workspace's root (`editor.workspace` names it), not `$AUTODOC_ROOT`: resolve it there, or not " +
	"at all. Keep your task unless the user asks you to move, and tell them which workspace you are working in.\n\n" +
	"The first call of each kind asks the user to allow it: until they do, it is refused with `needs <capability>`. " +
	"Say so to the user, and try again once they have answered.\n\n"
