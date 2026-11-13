package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// profilesJSON is the agent profiles as the preference holds them: a JSON list, so a profile's
// paths are escaped as JSON escapes them (a Windows path's backslashes), never pasted in as text.
func profilesJSON(t *testing.T, profiles ...map[string]any) string {
	t.Helper()
	b, err := json.Marshal(profiles)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// pathList is dirs as a PATH holds them on this OS: os.PathListSeparator between.
func pathList(dirs ...string) string { return strings.Join(dirs, string(os.PathListSeparator)) }
