package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/enterprise/config"
)

// The deployment examples (deploy/) stay true: their server.toml loads, with its secrets read from
// the environment; the commands the unit and the image run are flags the binary takes (each run
// against a configuration that does not exist ends at loading it, never at its flags); and the
// unit's stop timeout outlasts the API's drain.
func TestTheDeploymentExamplesHoldTrue(t *testing.T) {
	for _, env := range []string{"AUTODOC_SERVER_DSN", "LM_WEB_TOKEN", "AUTODOC_OPS_TOKEN"} {
		t.Setenv(env, strings.Repeat("x", 40)+env)
	}
	cfg, err := config.Load("../deploy/server.toml")
	if err != nil {
		t.Fatalf("deploy/server.toml: %v", err)
	}
	if cfg.HTTP == nil || !cfg.HTTP.Proxy || len(cfg.HTTP.Operators) != 1 || cfg.Log.MetricsInterval != time.Minute {
		t.Errorf("deploy/server.toml loads as %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join("../deploy", "views", "track.view")); err != nil {
		t.Errorf("the example's view: %v", err)
	}

	unit, err := os.ReadFile("../deploy/systemd/autodoc-server.service")
	if err != nil {
		t.Fatal(err)
	}
	var commands [][]string
	var stopTimeout time.Duration
	sc := bufio.NewScanner(bytes.NewReader(unit))
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "ExecStart="); ok {
			commands = append(commands, strings.Fields(v)[1:])
		}
		if v, ok := strings.CutPrefix(line, "TimeoutStopSec="); ok {
			if stopTimeout, err = time.ParseDuration(v + "s"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if stopTimeout <= shutdownGrace {
		t.Errorf("the unit's TimeoutStopSec %v does not outlast the API's drain %v", stopTimeout, shutdownGrace)
	}
	docker, err := os.ReadFile("../deploy/container/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`(?:ENTRYPOINT|CMD) (\[[^\]]*\])`).FindAllSubmatch(docker, -1) {
		var argv []string
		if err := json.Unmarshal(m[1], &argv); err != nil {
			t.Fatalf("%s: %v", m[1], err)
		}
		commands = append(commands, argv[1:])
	}
	if len(commands) != 3 {
		t.Fatalf("found %d commands in the unit and the Dockerfile, want 3: %q", len(commands), commands)
	}
	missing := filepath.Join(t.TempDir(), "absent.toml")
	for _, argv := range commands {
		args := append([]string(nil), argv...)
		args[len(args)-1] = missing // never the real path: nothing must start
		var out, errOut bytes.Buffer
		code := run(context.Background(), args, testOptions, &out, &errOut)
		if code != 1 || strings.Contains(errOut.String(), "flag provided but not defined") || !strings.Contains(errOut.String(), "absent.toml") {
			t.Errorf("%q: exit %d: %s", argv, code, errOut.String())
		}
	}
}
