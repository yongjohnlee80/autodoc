package rpc

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/hostprobe"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/golib/search/embed"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// setupEmbeddings is the wire's embeddings with a setup: the host it reports, the models installed,
// a sample's time a text, and the pull verbs' answers; it records the texts each sample was given.
type setupEmbeddings struct {
	wireEmbeddings
	mu        sync.Mutex
	host      hostprobe.Host
	installed map[string]embed.Model
	perText   time.Duration
	sampled   [][]string
	pullErr   error
}

func (e *setupEmbeddings) Probe(context.Context, string) hostprobe.Host {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.host
}

func (e *setupEmbeddings) Installed(_ context.Context, _, model string, _ int) (embed.Model, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.installed[model]
	return m, ok, nil
}

func (e *setupEmbeddings) Sample(_ context.Context, _, _ string, _ int, texts []string) (time.Duration, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sampled = append(e.sampled, texts)
	return e.perText, nil
}

func (e *setupEmbeddings) Pull(string, string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return "pull-x-1", e.pullErr
}
func (e *setupEmbeddings) PullStatus(job string) (PullState, error) {
	if job != "pull-x-1" {
		return PullState{}, ErrNoPull
	}
	return PullState{Job: job, Model: "m", State: "running", Completed: 10, Total: 100}, nil
}
func (e *setupEmbeddings) CancelPull(job string) (PullState, error) {
	if job != "pull-x-1" {
		return PullState{}, ErrNoPull
	}
	return PullState{Job: job, Model: "m", State: "cancelled"}, nil
}

const gib = int64(1) << 30

// recommended is the catalog's recommended card name and its fit, "" for none.
func recommended(t *testing.T, cli *golibrpc.Client) (name, fit string) {
	t.Helper()
	for _, c := range call(t, cli, "embedding.catalog").([]any) {
		m := c.(map[string]any)
		if m["recommended"] == true {
			if name != "" {
				t.Fatalf("two recommended: %s and %s", name, m["name"])
			}
			name, fit = m["name"].(string), m["fit"].(string)
		}
	}
	return name, fit
}

// The catalog marks where each card fits the probed host, and recommends by AutoDoc's policy: the
// best fit on a GPU, the lightest multilingual one on a CPU, and none when nothing fits or nothing is
// known.
func TestTheCatalogRecommendsForTheHost(t *testing.T) {
	emb := &setupEmbeddings{}
	r := serveWith(t, WithEmbeddings(emb))
	cli := r.dial(true)
	for _, c := range []struct {
		name      string
		host      hostprobe.Host
		want, fit string
	}{
		{"a 32 GiB NVIDIA GPU", hostprobe.Host{GPU: hostprobe.GPU{Vendor: hostprobe.VendorNVIDIA, VRAM: 32 * gib}, RAM: 64 * gib},
			"qwen3-embedding:4b", string(embed.FitGPU)},
		{"no GPU, 8 GiB of RAM", hostprobe.Host{RAM: 8 * gib}, "qwen3-embedding:0.6b", string(embed.FitCPU)},
		{"nothing known", hostprobe.Host{}, "", ""},
	} {
		emb.mu.Lock()
		emb.host = c.host
		emb.mu.Unlock()
		if name, fit := recommended(t, cli); name != c.want || fit != c.fit {
			t.Errorf("%s: recommended %q (%s); want %q (%s)", c.name, name, fit, c.want, c.fit)
		}
	}
}

// Before the model is installed the estimate is provisional: every distinct alive text, no
// fingerprint, no time. Installed, it counts the texts pending under the real fingerprint and times
// a sample of at most sampleTexts of them. A model with no card that isn't installed, and a
// workspace that isn't served, are refused.
func TestTheEstimateIsProvisionalUntilTheModelIsInstalled(t *testing.T) {
	emb := &setupEmbeddings{installed: map[string]embed.Model{}, perText: 50 * time.Millisecond}
	r := serveWith(t, WithEmbeddings(emb))
	cli := r.dial(true)
	for i := range 3 {
		call(t, cli, "doc.write", "kb", fmt.Sprintf("n%d.md", i), []byte(fmt.Sprintf("# N%d\n\nsection %d\n", i, i)), "")
	}
	eventually(t, "three notes", func() bool { return call(t, cli, "index.status", "kb").(map[string]any)["docs"] == int64(3) })
	kb, _ := r.srv.workspaces.Get("kb")
	ix := kb.Index()
	texts, err := ix.Texts(context.Background())
	if err != nil || texts == 0 {
		t.Fatalf("the workspace's texts: %d, %v", texts, err)
	}
	one := func(model string) map[string]any {
		t.Helper()
		return call(t, cli, "embedding.estimate", model, []any{"kb"}).(map[string]any)
	}

	got := one("qwen3-embedding:0.6b")
	w := got["workspaces"].([]any)[0].(map[string]any)
	f32, bits, key := index.VectorBytes(1024, embed.Model{Provider: "ollama", Name: "qwen3-embedding:0.6b", Digest: placeholderDigest, Dims: 1024}.Fingerprint())
	if got["basis"] != basisProvisional || got["fp"] != nil || w["texts"] != texts || w["logical_bytes"] != texts*(f32+bits+key) || w["seconds"] != nil {
		t.Fatalf("not installed: %+v; want provisional, %d texts", got, texts)
	}

	m := embed.Model{Provider: "ollama", Name: "qwen3-embedding:0.6b", Digest: "sha256:x", Dims: 1024}
	emb.mu.Lock()
	emb.installed[m.Name] = m
	emb.mu.Unlock()
	got = one(m.Name)
	w = got["workspaces"].([]any)[0].(map[string]any)
	want := (50 * time.Millisecond).Seconds() * float64(texts)
	if got["basis"] != basisInstalled || got["fp"] != m.Fingerprint() || w["texts"] != texts || w["seconds"] != want {
		t.Fatalf("installed: %+v; want installed at %s, %d texts, %v s", got, m.Fingerprint(), texts, want)
	}
	emb.mu.Lock()
	n, size := len(emb.sampled), len(emb.sampled[len(emb.sampled)-1])
	emb.mu.Unlock()
	if n != 1 || size == 0 || size > sampleTexts {
		t.Fatalf("%d samples, the last of %d texts; want one of 1 to %d", n, size, sampleTexts)
	}

	if _, err := cli.Call(context.Background(), "embedding.estimate", "no-such-model", []any{"kb"}); code(err) != golibrpc.CodeInvalidParams {
		t.Errorf("a model with no card, not installed: %v", err)
	}
	if _, err := cli.Call(context.Background(), "embedding.estimate", m.Name, []any{"nope"}); code(err) != CodeNoSuchWorkspace {
		t.Errorf("a workspace not served: %v", err)
	}
}

// The pull verbs answer the job, its state, and their errors as codes: another pull running is a
// conflict, and a job that is not the current one is not found.
func TestThePullVerbsAnswerTheJobAndTheirErrors(t *testing.T) {
	emb := &setupEmbeddings{}
	r := serveWith(t, WithEmbeddings(emb))
	cli := r.dial(true)
	if got := call(t, cli, "embedding.pull", "m").(map[string]any); got["job"] != "pull-x-1" {
		t.Fatalf("pull: %+v", got)
	}
	if got := call(t, cli, "embedding.pull_status", "pull-x-1").(map[string]any); got["state"] != "running" || got["total"] != int64(100) {
		t.Fatalf("status: %+v", got)
	}
	if _, err := cli.Call(context.Background(), "embedding.pull_status", "pull-old"); code(err) != CodeNotFound {
		t.Errorf("a stale job's status: %v", err)
	}
	if _, err := cli.Call(context.Background(), "embedding.pull_cancel", "pull-old"); code(err) != CodeNotFound {
		t.Errorf("a stale cancel: %v", err)
	}
	emb.mu.Lock()
	emb.pullErr = ErrPulling
	emb.mu.Unlock()
	if _, err := cli.Call(context.Background(), "embedding.pull", "m"); code(err) != CodeConflict {
		t.Errorf("a pull while another runs: %v", err)
	}
}
