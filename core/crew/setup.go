package crew

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/yongjohnlee80/golib/errs"
)

// SetupSince, in an After step's argument, is the agent's start, as Unix seconds.
const SetupSince = "{since}"

// codexRegisterWait is how long a Codex agent's step looks for its thread before it raises the
// session's attention.
const codexRegisterWait = "60s"

// The hosts a setup is for.
const (
	HostWindow = "window" // an AutoDoc window: it writes Claude's settings into the agent's folder
	HostNvim   = "nvim"   // Neovim: it writes nothing into the project, so Claude's go on its command line
)

// SetupInput is what a host knows when it starts a crew agent: its kind, the host it is, the
// command the agent's hooks and plugins run AutoDoc with, and the profile's own environment.
type SetupInput struct {
	Kind    string // crew's kind (KindClaude, KindOpenCode, …)
	Program string // the CLI the agent's command runs, a name looked up on Env's PATH or a path; "" for the kind's own
	Host    string // HostWindow or HostNvim
	Autodoc string // the command, as a shell reads it, that the hooks run AutoDoc with (a host's wrapper)
	// Env is the environment the agent starts with, the profile's own included: read (its PATH finds
	// the CLI; its OPENCODE_CONFIG_CONTENT is extended), never answered back, since its values may
	// be an account's paths.
	Env     map[string]string
	DataDir string // AutoDoc's data folder, for the config the host runs under: AutoDoc's own files go there
	Dir     string // the agent's folder: where a CLI found on PATH is looked up from
	// AgentDir is the folder its host owns for the agent, where FolderFiles go: a window's agent
	// folder (its cwd), or auto-agents' per-agent folder for Neovim (which passes it as --add-dir),
	// never the user's project.
	AgentDir string
	// Servers are the MCP servers the daemon offers the session (none from a daemon without them): a
	// kind that takes MCP servers delivers each its own way.
	Servers []MCPServer
}

// MCPServer is an MCP server a crew session's CLI is given (its tools the crew's own, such as
// `autodoc crew mcp`): its name, its argv, AutoDoc's own env for it (never the profile's), and the
// tools it serves, from which each kind spells its allow rules (agy mcp(<name>/<tool>), Claude
// mcp__<name>__<tool>).
type MCPServer struct {
	Name    string            `json:"name"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env"`
	Tools   []string          `json:"tools"`
}

// FolderFile is a file a host writes into the agent's own folder (SetupInput.AgentDir): a CLI's
// folder-scoped configuration, never a user-level file and never the user's project. Its Path is
// local (filepath.IsLocal: relative, no "..", nothing outside the folder): SetupFor refuses any
// other. A host writes it with mode 0600, since it may name the session's context.
type FolderFile struct {
	Path    string `json:"path"` // relative to SetupInput.AgentDir
	Content string `json:"content"`
}

// ErrFolderFile is a setup whose folder file would land outside the agent's own folder.
var ErrFolderFile = errs.Sentinel(errs.ErrInvalidArgument, "crew: a folder file's path must stay inside the agent's folder")

// SetupFile is a user-level file a kind needs, outside AutoDoc's folders: reported with its state,
// and installed only by its own command, at the user's say.
type SetupFile struct {
	Path    string `json:"path"`
	State   string `json:"state"`   // installed | stale | missing, by its content
	Install string `json:"install"` // the command that installs or updates it
}

// Setup is everything a host adds to a crew agent's start so that its session comes up reachable
// with nobody acting (docs/decisions/crew.md#wake-acceptance-not-tool-approvals): nothing in it is
// an approval policy, a permission mode, or an allow or deny rule.
type Setup struct {
	Argv    []string          `json:"argv"`    // appended to the CLI's command
	Env     map[string]string `json:"env"`     // AutoDoc's own variables, merged after the profile's
	Files   []SetupFile       `json:"files"`   // user-level files the kind needs, reported, never written here
	Prompts bool              `json:"prompts"` // the setup reports the session's permission prompts: crew.register's prompts
	// Allow is the guide's commands as the kind's CLI allows them (GuideCommands), already in Argv or
	// Env: a window writing Claude's settings itself merges them into the folder's.
	Allow []string `json:"allow"`
	// FolderFiles are written by the host into SetupInput.AgentDir before the CLI starts.
	FolderFiles []FolderFile `json:"folder_files"`
	// After is what the host runs once the agent's CLI has started: each one autodoc's arguments, run
	// detached by the agent's own autodoc in its environment (its session's context), with
	// SetupSince in an argument replaced by the start's Unix time in seconds.
	After   [][]string `json:"after"`
	Notices []string   `json:"notices"` // what the host should tell the user, once
}

// SetupFor is a crew agent's setup for in (docs/decisions/crew.md#three-modules-and-one-protocol),
// the one source every host asks:
//   - Claude Code: the settings ClaudeSettings builds for the Claude Code on in.Env's PATH, which
//     names only the hook events it knows; with none found, ClaudeFloor's, prompts unreported, and a
//     notice. A Neovim host gets them as --settings; a window writes them into the agent's folder
//     itself, so it gets no Argv.
//   - OpenCode: AutoDoc's plugin, written into in.DataDir (AutoDoc's own folder, so setup writes
//     it), and the OPENCODE_CONFIG_CONTENT that loads it, in.Env's own value extended.
//   - Codex: one step after its start, the agent's autodoc registering the folder's thread, looking
//     until it is found (crew endpoint --codex-here --wait).
//   - Antigravity (agy): its mail as in.Servers' tools (agySetup).
//   - the other kinds: nothing yet.
//
// Each kind's is its kindSetups entry; a new kind, or a kind taking in.Servers, changes only its own.
// cache is ClaudeOf's cache of what each binary knows ("" for none).
func SetupFor(in SetupInput, cache string) (Setup, error) {
	s := Setup{Argv: []string{}, Env: map[string]string{}, Files: []SetupFile{}, Notices: []string{}, After: [][]string{},
		Allow: []string{}, FolderFiles: []FolderFile{}}
	if f := kindSetups[in.Kind]; f != nil {
		if err := f(in, cache, &s); err != nil {
			return Setup{}, err
		}
	}
	for _, f := range s.FolderFiles {
		if !filepath.IsLocal(f.Path) || in.AgentDir == "" {
			return Setup{}, fmt.Errorf("%w: %q", ErrFolderFile, f.Path)
		}
	}
	return s, nil
}

// kindSetup adds kind's setup to s: its argv, env, files and allowance, and, once it takes them, the
// MCP servers in.Servers names, delivered its own way (Claude's --mcp-config or the folder's
// .mcp.json, OpenCode's mcp key, Antigravity's folder .agents/mcp_config.json found by --add-dir).
type kindSetup func(in SetupInput, cache string, s *Setup) error

// kindSetups are the kinds with a setup: a kind is added here, never by rewriting SetupFor.
var kindSetups = map[string]kindSetup{
	KindClaude:   claudeSetup,
	KindCodex:    codexSetup,
	KindOpenCode: opencodeSetup,
	KindAgy:      agySetup,
}

// AgyBridgeCommand is the one shell command an agy crew agent runs, its guide's wake step and its
// launch prompt: so the one its session's rules allow, exactly.
const AgyBridgeCommand = "autodoc crew bridge --agy"

// AgyLaunchPrompt is an agy agent's first turn: the bridge alone, since agy gives a session its
// workspace's MCP tools only from its second turn (docs/decisions/crew.md#the-crew-mcp-server).
const AgyLaunchPrompt = "Run exactly this shell command once, and nothing else: " + AgyBridgeCommand

// agyMCPConfig is where agy reads a workspace's MCP servers, relative to the folder: in its cwd or
// in an --add-dir folder, never a parent folder.
const agyMCPConfig = ".agents/mcp_config.json"

// agySetup gives an Antigravity agent its crew mail as in.Servers' tools
// (docs/decisions/crew.md#the-crew-mcp-server): their config in the agent's folder (FolderFiles,
// .agents/mcp_config.json, which agy finds in its cwd or, from Neovim, the --add-dir folder); and
// on its command line, for this session alone, its rules (each tool by its exact mcp(<server>/<tool>),
// and the bridge's exact command), the agent's folder trusted, and the launch prompt that runs the
// bridge. agy writes nothing of this into the user's settings. Its bridge reports its prompts.
// Without servers or a folder its mail can't be set up: it still starts, with a notice.
func agySetup(in SetupInput, _ string, s *Setup) error {
	if len(in.Servers) == 0 || in.AgentDir == "" {
		s.Notices = append(s.Notices, "Antigravity's crew mail needs the crew MCP server and the agent's own folder, which this host didn't give: the agent starts without its mail")
		return nil
	}
	servers := map[string]any{}
	allow := []string{"command(" + AgyBridgeCommand + ")"}
	for _, sv := range in.Servers {
		if len(sv.Command) == 0 {
			return fmt.Errorf("crew: MCP server %q has no command", sv.Name)
		}
		server := map[string]any{"command": sv.Command[0], "args": sv.Command[1:]}
		if len(sv.Env) > 0 { // none: it inherits the agent's, its session's context included
			server["env"] = sv.Env
		}
		servers[sv.Name] = server
		for _, t := range sv.Tools {
			allow = append(allow, "mcp("+sv.Name+"/"+t+")")
		}
	}
	cfg, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return err
	}
	// the folders agy works in are trusted for this session alone, so it asks no trust question: the
	// agent's folder, and from Neovim its working folder too (the user's project, the agent's own
	// folder added to it); nothing is written to agy's own settings
	trusted := []string{in.AgentDir}
	if in.Host == HostNvim && filepath.IsAbs(in.Dir) && in.Dir != in.AgentDir {
		trusted = append(trusted, in.Dir)
	}
	settings, err := json.Marshal(map[string]any{"permissions": map[string]any{"allow": allow}, "trustedWorkspaces": trusted})
	if err != nil {
		return err
	}
	s.FolderFiles = append(s.FolderFiles, FolderFile{Path: agyMCPConfig, Content: string(cfg)})
	s.Argv = append(s.Argv, "--settings", string(settings))
	if in.Host == HostNvim { // Neovim's agent runs in the user's project: its folder is added, not its cwd
		s.Argv = append(s.Argv, "--add-dir", in.AgentDir)
	}
	s.Argv = append(s.Argv, "-i", AgyLaunchPrompt)
	s.Allow = allow
	s.Prompts = true
	return nil
}

// claudeSetup is crew.ClaudeSettings for the Claude Code on in.Env's PATH, the guide's commands
// allowed: Neovim's --settings; a window writes them into the agent's folder itself.
func claudeSetup(in SetupInput, cache string, s *Setup) error {
	program := in.Program
	if program == "" {
		program = "claude"
	}
	_, k, err := ClaudeOf(program, in.Env["PATH"], in.Dir, cache)
	if err != nil {
		k = ClaudeFloor()
		s.Notices = append(s.Notices, fmt.Sprintf("Claude Code's permission prompts can't be reported: %v", err))
	}
	settings, prompts := ClaudeSettings(in.Autodoc, k)
	s.Prompts = prompts
	s.Allow = ClaudeAllow()
	if in.Host == HostNvim {
		settings["permissions"] = map[string]any{"allow": s.Allow}
		b, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		s.Argv = append(s.Argv, "--settings", string(b))
	}
	return nil
}

// codexSetup is one step after the start: Codex starts its thread on its app-server without
// telling anyone, so the agent's autodoc looks for the folder's thread until it is found, then
// registers it (crew endpoint --codex-here --wait).
func codexSetup(_ SetupInput, _ string, s *Setup) error {
	s.After = append(s.After, []string{"crew", "endpoint", "--codex-here", "--since", SetupSince, "--wait", codexRegisterWait})
	return nil
}

// opencodeSetup is AutoDoc's plugin, written into in.DataDir, and the OPENCODE_CONFIG_CONTENT that
// loads it, the guide's commands allowed.
func opencodeSetup(in SetupInput, _ string, s *Setup) error {
	path, err := WriteOpenCodePlugin(filepath.Join(in.DataDir, "opencode"))
	if err != nil {
		return fmt.Errorf("writing AutoDoc's OpenCode plugin: %w", err)
	}
	v, err := OpenCodeConfigContent(in.Env["OPENCODE_CONFIG_CONTENT"], path)
	if err != nil {
		return err
	}
	if openCodeAllowProven {
		if v, err = OpenCodeWithAllow(v); err != nil {
			return err
		}
		for rule := range OpenCodeAllow() {
			s.Allow = append(s.Allow, rule)
		}
		slices.Sort(s.Allow)
	}
	s.Env["OPENCODE_CONFIG_CONTENT"] = v
	return nil
}
