package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

// tomlString is s as a TOML basic string, whatever s holds: a backslash (a Windows path), a quote,
// an apostrophe. It is JSON's string, whose escapes are all TOML's, with the one character JSON
// leaves raw and TOML refuses (U+007F) escaped.
func tomlString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\x7f", `\u007F`)
}
