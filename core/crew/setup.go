package crew

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

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
}

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
	Notices []string          `json:"notices"` // what the host should tell the user, once
}

// SetupFor is a crew agent's setup for in (docs/decisions/crew.md#three-modules-and-one-protocol),
// the one source every host asks:
//   - Claude Code: the settings ClaudeSettings builds for the Claude Code on in.Env's PATH, which
//     names only the hook events it knows; with none found, ClaudeFloor's, prompts unreported, and a
//     notice. A Neovim host gets them as --settings; a window writes them into the agent's folder
//     itself, so it gets no Argv.
//   - OpenCode: AutoDoc's plugin, written into in.DataDir (AutoDoc's own folder, so setup writes
//     it), and the OPENCODE_CONFIG_CONTENT that loads it, in.Env's own value extended.
//   - the other kinds: nothing yet.
//
// cache is ClaudeOf's cache of what each binary knows ("" for none).
func SetupFor(in SetupInput, cache string) (Setup, error) {
	s := Setup{Argv: []string{}, Env: map[string]string{}, Files: []SetupFile{}, Notices: []string{}}
	switch in.Kind {
	case KindClaude:
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
		if in.Host == HostNvim {
			b, err := json.Marshal(settings)
			if err != nil {
				return Setup{}, err
			}
			s.Argv = append(s.Argv, "--settings", string(b))
		}
	case KindOpenCode:
		path, err := WriteOpenCodePlugin(filepath.Join(in.DataDir, "opencode"))
		if err != nil {
			return Setup{}, fmt.Errorf("writing AutoDoc's OpenCode plugin: %w", err)
		}
		v, err := OpenCodeConfigContent(in.Env["OPENCODE_CONFIG_CONTENT"], path)
		if err != nil {
			return Setup{}, err
		}
		s.Env["OPENCODE_CONFIG_CONTENT"] = v
	}
	return s, nil
}
