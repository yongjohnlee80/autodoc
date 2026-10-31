//go:build server

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
// unit's stop timeout outlasts the API's drain; compose publishes the plain-HTTP API on the host's
// loopback only.
func TestTheDeploymentExamplesHoldTrue(t *testing.T) {
	for _, env := range []string{"AUTODOC_SERVER_DSN", "WEB_APP_TOKEN", "AUTODOC_OPS_TOKEN"} {
		t.Setenv(env, strings.Repeat("x", 40)+env)
	}
	cfg, err := config.Load("../deploy/server.toml")
	if err != nil {
		t.Fatalf("deploy/server.toml: %v", err)
	}
	if cfg.HTTP == nil || !cfg.HTTP.Proxy || len(cfg.HTTP.Operators) != 1 || cfg.Log.MetricsInterval != time.Minute {
		t.Errorf("deploy/server.toml loads as %+v", cfg)
	}
	// the unit's listens on loopback; the container's differs only there, on its own network
	if cfg.HTTP.Listen != "127.0.0.1:8730" {
		t.Errorf("deploy/server.toml listens on %q, want the host's loopback", cfg.HTTP.Listen)
	}
	host, err := os.ReadFile("../deploy/server.toml")
	if err != nil {
		t.Fatal(err)
	}
	cont, err := os.ReadFile("../deploy/container/server.toml")
	if err != nil {
		t.Fatal(err)
	}
	if ccfg, err := config.Load("../deploy/container/server.toml"); err != nil || ccfg.HTTP == nil || ccfg.HTTP.Listen != "0.0.0.0:8730" {
		t.Errorf("deploy/container/server.toml: %v; want it listening on the container's network", err)
	}
	if a, b := tomlKeys(host), tomlKeys(cont); a != b {
		t.Errorf("the two server.toml differ beyond listen:\n%s\n---\n%s", a, b)
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
	// the image runs --server, which only a build with the server tag has
	if !regexp.MustCompile(`(?m)^RUN .*go build -tags server `).Match(docker) {
		t.Error("the Dockerfile builds without the server tag: its image would refuse --server")
	}
	// the example's API is plain HTTP behind a proxy (proxy = true): its published port is the
	// host's loopback only, never every interface
	compose, err := os.ReadFile("../deploy/container/compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`POSTGRES_PASSWORD: [^$\s]`).Match(compose) {
		t.Error("compose.yml writes the database's password: it is .env's")
	}
	ports := regexp.MustCompile(`ports: \[([^\]]*)\]`).FindAllSubmatch(compose, -1)
	if len(ports) == 0 {
		t.Fatal("compose.yml publishes no port: the check would see nothing")
	}
	for _, p := range ports {
		for _, port := range strings.Split(string(p[1]), ",") {
			port = strings.Trim(strings.TrimSpace(port), `"`)
			if !strings.HasPrefix(port, "127.0.0.1:") {
				t.Errorf("compose.yml publishes %q beyond the host's loopback", port)
			}
		}
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

// tomlKeys is a server.toml's settings, its comments and its listen line left out: what the two
// examples must hold alike.
func tomlKeys(b []byte) string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "listen") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
