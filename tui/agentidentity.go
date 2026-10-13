package tui

import (
	"fmt"
	"maps"
	"strings"
	"unicode"
)

// migrateAgentDefault examines presence in the raw store, before readRows can
// turn absence and an explicit empty string into the same default. Once the
// canonical key exists, the legacy mirror never participates in resolution.
func migrateAgentDefault(values map[string]any) (map[string]any, bool) {
	if _, present := values[prefAgentDefaultCodename]; present {
		return values, false
	}
	name, _ := values[prefAgentDefault].(string)
	raw, _ := values[prefAgentProfiles].(string)
	code := ""
	for _, p := range agentProfilesOf(raw) {
		if p.Name == name {
			code = p.Codename
			break
		}
	}
	values = maps.Clone(values)
	if values == nil {
		values = map[string]any{}
	}
	values[prefAgentDefaultCodename] = code
	return values, true
}

// agentCodename is a legacy profile name's default identity. Separators become
// one hyphen; uniqueAgentCodenames allocates stable migration suffixes.
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

// Preserve already persisted identities, then allocate missing/duplicate ones
// in stored order. A first list save persists the result so later edits do not
// move suffixes. Reserving explicit identities prevents a legacy name stealing one.
func uniqueAgentCodenames(profiles []agentProfile) []agentProfile {
	owners := map[string]int{}
	used := map[string]bool{}
	for i, p := range profiles {
		if validAgentCodename(p.Codename) && !used[p.Codename] {
			owners[p.Codename], used[p.Codename] = i, true
		}
	}
	for i := range profiles {
		p := &profiles[i]
		if owner, exists := owners[p.Codename]; exists && owner == i {
			continue
		}
		base := p.Codename
		if !validAgentCodename(base) {
			base = agentCodename(p.Name)
		}
		if base == "" {
			base = "agent"
		}
		code := base
		for n := 2; used[code]; n++ {
			code = fmt.Sprintf("%s-%d", base, n)
		}
		p.Codename, used[code] = code, true
	}
	return profiles
}

// profileProblem protects direct start requests and live reservations. Stored
// profiles already project validated, unique codenames through migration.
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
	if instance := a.instances[p.Codename]; instance != nil {
		for _, saved := range a.profiles() {
			if saved.Codename == p.Codename {
				return ""
			}
		}
		return "codename held by a running agent"
	}
	return ""
}
