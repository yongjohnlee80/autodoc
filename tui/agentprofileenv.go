package tui

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A profile's env (docs/decisions/crew.md#a-profiles-environment) is set on its agent's process over
// AutoDoc's environment: Sephiroth runs `claude` with CLAUDE_CONFIG_DIR on another account, with no
// shell alias, so its kind is still claude and it gets the full crew setup. Its values expand ~ and
// $VAR at the spawn, never through a shell. AutoDoc's own variables stay AutoDoc's: an AUTODOC_* key
// is refused (and dropped from a profile written by hand), so is OPENCODE_CONFIG_CONTENT, which
// AutoDoc sets for a crew OpenCode agent; a PATH is the agent's PATH after AutoDoc's own folder.

// envKeyPattern is a variable's name as a shell takes one.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var (
	errEnvKeyReserved = errors.New("is AutoDoc's own")
	errEnvKeyInvalid  = errors.New("is not a variable's name")
	errEnvKeyTwice    = errors.New("is set twice")
)

// envError is why the form's env field is refused: the word or key it names, and which of the
// errEnvKey* it is, so the form says it in the catalog's language (envRefusal).
type envError struct {
	word string
	why  error
}

func (e *envError) Error() string { return e.word + " " + e.why.Error() }
func (e *envError) Unwrap() error { return e.why }

// profileEnvKey is why key can't be in a profile's env, nil when it can.
func profileEnvKey(key string) error {
	switch {
	case !envKeyPattern.MatchString(key):
		return errEnvKeyInvalid
	case strings.HasPrefix(key, "AUTODOC_"), key == "OPENCODE_CONFIG_CONTENT":
		return errEnvKeyReserved
	}
	return nil
}

// keptProfileEnv is env without the keys a profile can't set, nil when none is left: what a
// profile read from the preference keeps.
func keptProfileEnv(env map[string]string) map[string]string {
	var out map[string]string
	for k, v := range env {
		if profileEnvKey(k) == nil {
			if out == nil {
				out = map[string]string{}
			}
			out[k] = v
		}
	}
	return out
}

// parseProfileEnv reads the form's env field: NAME=value words, quoted as a shell's (shellWords),
// nothing expanded. Blank is none. A word that isn't NAME=value, a key a profile can't set, or a
// key twice is refused, naming it.
func parseProfileEnv(text string) (map[string]string, error) {
	var env map[string]string
	for _, w := range shellWords(text) {
		key, value, ok := strings.Cut(w.text, "=")
		if !ok || key == "" {
			return nil, &envError{w.text, errEnvKeyInvalid}
		}
		if err := profileEnvKey(key); err != nil {
			return nil, &envError{key, err}
		}
		if _, twice := env[key]; twice {
			return nil, &envError{key, errEnvKeyTwice}
		}
		if env == nil {
			env = map[string]string{}
		}
		env[key] = value
	}
	return env, nil
}

// formatProfileEnv is env as the form's field shows it, parseProfileEnv's inverse: its keys in
// order, each value quoted when it needs to be.
func formatProfileEnv(env map[string]string) string {
	words := make([]string, 0, len(env))
	for _, k := range slices.Sorted(maps.Keys(env)) {
		v := env[k]
		if v == "" || strings.ContainsAny(v, " \t\n'\"\\") {
			v = shellQuote(v)
		}
		words = append(words, k+"="+v)
	}
	return strings.Join(words, " ")
}

// expandProfileValue is v at the spawn: a leading ~ is the home folder, and $NAME or ${NAME} is
// AutoDoc's own variable (empty when unset), with no shell and nothing else evaluated.
func expandProfileValue(v string) string {
	if home, err := os.UserHomeDir(); err == nil && (v == "~" || strings.HasPrefix(v, "~/")) {
		v = filepath.Join(home, v[1:])
	}
	return os.Expand(v, func(name string) string {
		if name == "HOME" {
			if home, err := os.UserHomeDir(); err == nil {
				return home
			}
		}
		return os.Getenv(name)
	})
}

// envList is p's env as the spawn sets it, NAME=value expanded, in key order, without PATH (which
// agentPath folds into AutoDoc's own) and without a key a profile can't set.
func (p agentProfile) envList() []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(p.Env)) {
		if k != "PATH" && profileEnvKey(k) == nil {
			out = append(out, k+"="+expandProfileValue(p.Env[k]))
		}
	}
	return out
}

// agentPath is the PATH p's programs are found on, before AutoDoc's own folder: its env's PATH,
// expanded, or the TUI's.
func (p agentProfile) agentPath() string {
	if v, ok := p.Env["PATH"]; ok {
		return expandProfileValue(v)
	}
	return os.Getenv("PATH")
}

// environ is base with p's env over it (envList, and agentPath for PATH): what a program AutoDoc
// runs for p's agent, beside its terminal, sees.
func (p agentProfile) environ(base []string) []string {
	set := append(p.envList(), "PATH="+p.agentPath())
	out := slices.DeleteFunc(slices.Clone(base), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(set, func(s string) bool { return strings.HasPrefix(s, name+"=") })
	})
	return append(out, set...)
}

// same is whether p and q are one profile: every field, its env too.
func (p agentProfile) same(q agentProfile) bool {
	return p.Name == q.Name && p.Codename == q.Codename && p.sameLaunch(q)
}

// sameLaunch is whether p and q start the same process: its command, its kind (which its setup
// follows) and its env.
func (p agentProfile) sameLaunch(q agentProfile) bool {
	return p.Command == q.Command && p.Kind == q.Kind && maps.Equal(p.Env, q.Env)
}
