package tui

import (
	"fmt"
	"strings"
	"unicode"
)

// agentCodename is a legacy profile name's stable default identity. Separators
// become one hyphen; a collision is reported, never repaired with a suffix.
func agentCodename(name string) string {
	var b strings.Builder
	separator := false
	for _, r := range strings.TrimSpace(name) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			separator = b.Len() > 0
			continue
		}
		if separator {
			b.WriteByte('-')
			separator = false
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func validAgentCodename(name string) bool {
	return name != "" && name == agentCodename(name)
}

// profileProblem is why a saved profile cannot be a delivery/start target.
// Invalid legacy profiles remain in the editor until explicitly repaired.
func (a *agents) profileProblem(p agentProfile) string {
	if !validAgentCodename(p.Codename) {
		return "codename needs lower-case letters or digits, separated by hyphens"
	}
	matches := 0
	for _, other := range a.profiles() {
		if other.Codename != p.Codename {
			continue
		}
		matches++
		if matches > 1 || other.Name != p.Name {
			return fmt.Sprintf("codename taken by %s", other.Name)
		}
	}
	if a.agentRunning != "" && a.agentRunningCodename == p.Codename {
		for _, saved := range a.profiles() {
			if saved.Codename == p.Codename {
				return ""
			}
		}
		return "codename held by a running agent"
	}
	return ""
}
