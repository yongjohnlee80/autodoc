package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao/postgres"

	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

// tutorialPath is the tutorial this test runs, from the app package's directory.
const tutorialPath = "../docs/enterprise/tutorial.md"

// block is one fenced code block of the tutorial: its language, its marks (tutorial, expect,
// absent, server, compose, setup) and file= target, its body, and where it starts.
type block struct {
	lang  string
	marks map[string]bool
	file  string
	body  string
	line  int
}

// blocks are the fenced code blocks of md, in order.
func blocks(t *testing.T, md []byte) []block {
	t.Helper()
	var out []block
	var cur *block
	var body strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(md))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		switch {
		case cur == nil && strings.HasPrefix(line, "```"):
			info := strings.Fields(strings.TrimPrefix(line, "```"))
			cur = &block{marks: map[string]bool{}, line: n}
			for i, f := range info {
				switch {
				case i == 0:
					cur.lang = f
				case strings.HasPrefix(f, "file="):
					cur.file = strings.TrimPrefix(f, "file=")
				default:
					cur.marks[f] = true
				}
			}
			body.Reset()
		case cur != nil && line == "```":
			cur.body = body.String()
			out = append(out, *cur)
			cur = nil
		case cur != nil:
			body.WriteString(line + "\n")
		}
	}
	if cur != nil {
		t.Fatalf("%s:%d: a code block is never closed", tutorialPath, cur.line)
	}
	return out
}

// tutorialEmbedding is the [embedding] the test serves the tutorial's server with, for the
// tutorial's Ollama: the test's own OpenAI-compatible endpoint (base) on loopback.
func tutorialEmbedding(base string) string {
	return "[embedding]\nprovider = \"openai\"\nbase_url = \"" + base + "\"\nmodel    = \"fake-model\"\nkey_env  = \"AUTODOC_TEST_EMBED_KEY\"\n\n"
}

// swapEmbedding is cfg with its [embedding] table replaced by tutorialEmbedding(base).
func swapEmbedding(t *testing.T, cfg, base string) string {
	t.Helper()
	i := strings.Index(cfg, "[embedding]\n")
	if i < 0 {
		t.Fatal("the tutorial's server.toml has no [embedding] for the test to replace")
	}
	j := strings.Index(cfg[i+1:], "\n[")
	if j < 0 {
		t.Fatal("the tutorial's [embedding] is server.toml's last table: the test replaces it up to the next")
	}
	return cfg[:i] + tutorialEmbedding(base) + cfg[i+1+j+1:]
}

// The tutorial (docs/enterprise/tutorial.md) runs, as written, block by block: its SQL against a
// fresh PostgreSQL with pgvector, its files written, its commands run by sh with this test binary as
// autodoc, and every command's output holding what its expect block says and nothing its absent
// block says. The test brings its own database in place of the compose and setup steps, its own
// OpenAI-compatible endpoint in place of the tutorial's Ollama, and a free port in place of 8730.
// A block the test does not know what to do with fails it, so the tutorial cannot grow a step that
// is not run.
func TestTheTutorialRuns(t *testing.T) {
	for _, tool := range []string{"sh", "curl", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("AUTODOC_TEST_PG_REQUIRED") != "" {
				t.Fatalf("the tutorial needs %s: %v", tool, err)
			}
			t.Skipf("the tutorial needs %s", tool)
		}
	}
	md, err := os.ReadFile(tutorialPath)
	if err != nil {
		t.Fatal(err)
	}
	dsn := pgtest.ScratchURL(t)
	ctx := context.Background()
	db, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	emb := httptest.NewServer(&fakeEmbeddings{texts: map[string]int{}})
	t.Cleanup(emb.Close)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	at := func(s string) string { return strings.ReplaceAll(s, "127.0.0.1:8730", "127.0.0.1:"+port) }

	dir, bin := t.TempDir(), t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := "#!/bin/sh\nAUTODOC_TEST_MAIN=1 exec '" + exe + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "autodoc"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "AUTODOC_SERVER_DSN="+dsn,
		"LM_WEB_TOKEN="+hex.EncodeToString(tok), "AUTODOC_TEST_EMBED_KEY="+embedKey)
	sh := func(ctx context.Context, script string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "sh", "-c", script)
		cmd.Dir, cmd.Env = dir, env
		return cmd
	}

	var last string // the output of the last command run
	ran := 0
	for _, b := range blocks(t, md) {
		where := tutorialPath + ":" + strconv.Itoa(b.line)
		switch {
		case b.marks["compose"], b.marks["setup"]:
			// the test's own database and environment stand in for these
		case b.file != "":
			body := at(b.body)
			if b.file == "server.toml" {
				body = swapEmbedding(t, body, emb.URL+"/v1")
			}
			path := filepath.Join(dir, b.file)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		case b.lang == "sql" && b.marks["tutorial"]:
			for _, stmt := range regexp.MustCompile(`;\s*\n`).Split(b.body, -1) {
				if strings.TrimSpace(stmt) == "" {
					continue
				}
				if _, err := db.ExecContext(ctx, stmt); err != nil {
					t.Fatalf("%s: %v\n%s", where, err, stmt)
				}
			}
			ran++
		case b.lang == "sh" && b.marks["tutorial"]:
			cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			out, err := sh(cctx, at(b.body)).CombinedOutput()
			cancel()
			last = string(out)
			if err != nil {
				t.Fatalf("%s: %v\n%s", where, err, out)
			}
			ran++
		case b.lang == "sh" && b.marks["server"]:
			var out syncBuffer
			cmd := sh(ctx, "exec "+strings.TrimSpace(at(b.body)))
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Start(); err != nil {
				t.Fatalf("%s: %v", where, err)
			}
			t.Cleanup(func() {
				_ = cmd.Process.Signal(syscall.SIGTERM)
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("the tutorial's server after SIGTERM: %v\n%s", err, out.String())
					}
				case <-time.After(30 * time.Second):
					_ = cmd.Process.Kill()
					t.Errorf("the tutorial's server did not stop on SIGTERM:\n%s", out.String())
				}
				if t.Failed() {
					t.Logf("the tutorial's server said:\n%s", out.String())
				}
			})
			ran++
		case b.lang == "text" && b.marks["expect"]:
			for _, want := range strings.Split(strings.TrimSpace(b.body), "\n") {
				if !strings.Contains(last, want) {
					t.Errorf("%s: the command before does not print %q:\n%s", where, want, last)
				}
			}
		case b.lang == "text" && b.marks["absent"]:
			for _, not := range strings.Split(strings.TrimSpace(b.body), "\n") {
				if strings.Contains(last, not) {
					t.Errorf("%s: the command before prints %q:\n%s", where, not, last)
				}
			}
		default:
			t.Fatalf("%s: a ```%s block the test does not know what to do with: mark it (tutorial, file=, expect, absent, server, compose, setup)", where, b.lang)
		}
	}
	if ran < 8 {
		t.Fatalf("only %d of the tutorial's steps ran: this test would show nothing", ran)
	}
}
