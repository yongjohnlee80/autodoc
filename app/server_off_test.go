//go:build !server

package app

import (
	"strings"
	"testing"
)

// A build without the server tag refuses --server, its --check and its --health, before it reads
// any configuration, and says which build has it.
func TestAnUntaggedBuildRefusesTheServer(t *testing.T) {
	for _, args := range [][]string{
		{"--server", "server.toml"},
		{"--server", "--check", "server.toml"},
		{"--server", "--health", "server.toml"},
		{"--server"},
	} {
		code, out := runMain(t, nil, args...)
		if code != 1 || !strings.Contains(out, errNoServer.Error()) {
			t.Errorf("%v: exit %d, %q; want 1 and the no-server refusal", args, code, out)
		}
	}
}
