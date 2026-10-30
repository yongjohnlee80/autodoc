package tei

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// refusal is TEI's own line when its image's compute capability is not the GPU's.
const refusal = "Runtime compute cap 120 is not compatible with compile time compute cap 89"

// gpu is a stubbed machine: the capability nvidia-smi reports, what the GPU container's log holds,
// and whether TEI answers /health.
type gpu struct {
	cap, log string
	healthy  bool
}

// up runs `tei-rerank.sh up gpu` on the stubbed machine g, at most 30 seconds. It answers the exit
// code, stdout, stderr, every docker call the script made (one per line, with the TEI_GPU_TAG it
// exported), and how long it took.
func up(t *testing.T, g gpu) (code int, stdout, stderr, docker string, took time.Duration) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	calls := filepath.Join(dir, "docker.log")
	logFile := filepath.Join(dir, "tei.log")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logFile, []byte(g.log), 0o600); err != nil {
		t.Fatal(err)
	}
	health := "exit 7"
	if g.healthy {
		health = "exit 0"
	}
	stubs := map[string]string{
		"nvidia-smi": `case "$*" in *-L*) echo "GPU 0: stub" ;; *compute_cap*) echo "` + g.cap + `" ;; esac`,
		"docker": `echo "TEI_GPU_TAG=${TEI_GPU_TAG:-} $*" >>"` + calls + `"
case "$*" in *" logs "*) cat "` + logFile + `" ;; esac`,
		"curl":    health,
		"python3": `cat >/dev/null; echo "TEI stub: a reranker"`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/usr/bin/env bash\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "tei-rerank.sh", "up", "gpu")
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TEI_") && !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	start := time.Now()
	err := cmd.Run()
	took = time.Since(start)
	if ctx.Err() != nil {
		t.Fatalf("up gpu was still running after 30s: it waits on a TEI that can never answer\nstderr:\n%s", errOut.String())
	}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	return code, out.String(), errOut.String(), string(b), took
}

// A GPU whose TEI refuses the image (it ran on a GPU other than the one the tag was chosen for):
// up stops at once instead of waiting ten minutes on a container Docker restarts into the same
// refusal, stops it, and names the tag the GPU it ran on needs, saying the GPU is supported.
func TestUpStopsAtTEIsRefusalAndNamesTheTag(t *testing.T) {
	code, _, stderr, docker, took := up(t, gpu{cap: "8.9", log: "ERROR backend: Could not start Candle backend: " + refusal + "\n"})
	if code != 1 {
		t.Errorf("exit %d; want 1", code)
	}
	if took > 15*time.Second {
		t.Errorf("took %s: up waited on a refused TEI", took)
	}
	for _, want := range []string{refusal, "compute capability 12.0", "the GPU is\n  supported", "text-embeddings-inference:120-1.9"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(docker, "TEI_GPU_TAG=89-1.9 ") || !strings.Contains(docker, "up -d --force-recreate tei-gpu") {
		t.Errorf("up did not start the 89-1.9 image nvidia-smi's 8.9 picks:\n%s", docker)
	}
	if !strings.Contains(docker, "--profile gpu down") {
		t.Errorf("the refused container was not stopped:\n%s", docker)
	}
}

// A refusal on a capability TEI builds no image for says to use the CPU.
func TestUpSaysCPUWhenTEIBuildsNoImage(t *testing.T) {
	code, _, stderr, _, _ := up(t, gpu{cap: "7.5", log: "Runtime compute cap 61 is not compatible with compile time compute cap 75\n"})
	if code != 1 || !strings.Contains(stderr, "TEI builds no image for compute capability 6.1: use `up cpu`") {
		t.Errorf("exit %d; stderr:\n%s", code, stderr)
	}
}

// The control: a TEI that answers is waited for as before, on the tag for the GPU's capability.
func TestUpWaitsForAHealthyTEI(t *testing.T) {
	code, stdout, stderr, docker, _ := up(t, gpu{cap: "12.0", healthy: true})
	if code != 0 || !strings.Contains(stdout, "ready after") || !strings.Contains(stdout, "TEI stub: a reranker") {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(docker, "TEI_GPU_TAG=120-1.9 ") {
		t.Errorf("12.0 did not pick 120-1.9:\n%s", docker)
	}
	if strings.Contains(docker, "down") {
		t.Errorf("a healthy TEI was stopped:\n%s", docker)
	}
}

// A capability with no image is refused before anything starts, as before.
func TestUpRefusesAnUnknownCapabilityFirst(t *testing.T) {
	code, _, stderr, docker, _ := up(t, gpu{cap: "6.1"})
	if code != 1 || !strings.Contains(stderr, "no TEI image for compute capability 6.1: use `up cpu`") {
		t.Errorf("exit %d; stderr:\n%s", code, stderr)
	}
	if strings.Contains(docker, " up ") {
		t.Errorf("a container was started:\n%s", docker)
	}
}
