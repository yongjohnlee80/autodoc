package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// configFrom: --config's file first, then $AUTODOC_CONFIG's, an empty one being none, then the
// default ("").
func TestTheConfigIsTheFlagsThenTheEnvironmentsThenTheDefault(t *testing.T) {
	for _, c := range []struct {
		flag string
		env  procenv.Env
		want string
	}{
		{"/a.toml", procenv.Env{"AUTODOC_CONFIG=/b.toml"}, "/a.toml"},
		{"", procenv.Env{"AUTODOC_CONFIG=/b.toml"}, "/b.toml"},
		{"", procenv.Env{"AUTODOC_CONFIG="}, ""},
		{"", procenv.Env{}, ""},
		{"", procenv.Env{"AUTODOC_CONFIG=rel.toml"}, "rel.toml"},
	} {
		if got := configFrom(c.flag, c.env); got != c.want {
			t.Errorf("--config %q, env %q: %q, want %q", c.flag, c.env, got, c.want)
		}
	}
}

// endpointConfig writes a config in a new folder of its own whose socket is named name, and answers
// the config's path and that socket's address.
func endpointConfig(t *testing.T, name string) (cfg, sock string) {
	t.Helper()
	dir := short(t)
	sock = ipc.ScratchAddr(dir, name)
	return writeConfig(t, sock, filepath.Join(dir, "state"), ""), sock
}

// Every mode reads the config the same way, --config's, else $AUTODOC_CONFIG's, else the default:
// --print-endpoint prints the socket of the one it read.
func TestEveryModeReadsTheConfigFromTheEnvironmentWhenNoFlagNamesOne(t *testing.T) {
	flagCfg, flagSock := endpointConfig(t, "flag.sock")
	envCfg, envSock := endpointConfig(t, "env.sock")
	home := t.TempDir()
	defCfg, defSock := endpointConfig(t, "default.sock")
	b, err := os.ReadFile(defCfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "autodoc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "autodoc", "config.toml"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"XDG_CONFIG_HOME=" + home, "APPDATA=" + home}
	for _, c := range []struct {
		name string
		env  []string
		args []string
		want string
	}{
		{"the flag over the environment", []string{"AUTODOC_CONFIG=" + envCfg}, []string{"--config", flagCfg, "--print-endpoint"}, flagSock},
		{"the environment", []string{"AUTODOC_CONFIG=" + envCfg}, []string{"--print-endpoint"}, envSock},
		{"an empty environment is none", []string{"AUTODOC_CONFIG="}, []string{"--print-endpoint"}, defSock},
	} {
		code, out := runMain(t, append(append([]string{}, base...), c.env...), c.args...)
		if code != 0 || !strings.Contains(strings.SplitN(out, "\n", 2)[0], c.want) {
			t.Errorf("%s: exit %d, %q; want the endpoint %s", c.name, code, out, c.want)
		}
	}
}

// A relative $AUTODOC_CONFIG is the working directory's, as a relative --config is.
func TestARelativeConfigFromTheEnvironmentIsTheWorkingDirectorys(t *testing.T) {
	cfg, sock := endpointConfig(t, "rel.sock")
	t.Chdir(filepath.Dir(cfg))
	var out bytes.Buffer
	path := configFrom("", procenv.Env{"AUTODOC_CONFIG=" + filepath.Base(cfg)})
	if err := runPrintEndpoint(context.Background(), path, false, &out); err != nil || !strings.Contains(out.String(), sock) {
		t.Fatalf("%q from %s: %v, %q; want the endpoint %s", path, filepath.Dir(cfg), err, out.String(), sock)
	}
}
