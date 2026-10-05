package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/rank"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// longFirst is a build's ranker scoring a text containing "long" 1 and any other 0, failing while
// down is set.
type longFirst struct{ down atomic.Bool }

func (l *longFirst) Rank(_ context.Context, _ string, texts []string) ([]float64, error) {
	if l.down.Load() {
		return nil, fmt.Errorf("%w: the model at /opt/secret/slm is not loaded", rank.ErrUnreachable)
	}
	out := make([]float64, len(texts))
	for i, x := range texts {
		if strings.Contains(x, "long") {
			out[i] = 1
		}
	}
	return out, nil
}

func (*longFirst) Model() rank.Model {
	return rank.Model{Provider: "build", Name: "slm-ranker", MaxBatch: 50}
}

// TestMainRefusesABadRank: a window outside the bounds, or a window with no ranker to rank it,
// stops every mode at entry.
func TestMainRefusesABadRank(t *testing.T) {
	for name, r := range map[string]Rank{
		"a window under the bounds": {Ranker: &longFirst{}, Window: rank.MinWindow - 1},
		"a window over the bounds":  {Ranker: &longFirst{}, Window: rank.MaxWindow + 1},
		"a window with no ranker":   {Window: 20},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), []string{"--version"}, Options{Version: "v1", Rank: r}, &stdout, &stderr); code != 1 ||
			!strings.Contains(stderr.String(), "Options.Rank.Window") {
			t.Errorf("%s: exit %d, stderr %q; want 1 naming Options.Rank.Window", name, code, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--version"}, Options{Version: "v1", Rank: Rank{Ranker: &longFirst{}, Window: 20}}, &stdout, &stderr); code != 0 {
		t.Errorf("a build's ranker at 20: exit %d, %s", code, stderr.String())
	}
}

// rankedDaemon serves a workspace of two notes, both found by "kestrel", recall putting
// short.md first, as a build whose ranking is r.
func rankedDaemon(t *testing.T, r Rank) *golibrpc.Client {
	t.Helper()
	dir := short(t)
	root := t.TempDir()
	for p, s := range map[string]string{
		"short.md": "# Short\n\nkestrel\n",
		"long.md":  "# Long\n\nkestrel among many other words in a longer chunk of text\n",
	} {
		if err := os.WriteFile(filepath.Join(root, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sock := filepath.Join(dir, "a.sock")
	b := testBuild
	b.rank = r
	startAs(t, writeConfig(t, sock, filepath.Join(dir, "state"), "", "kb="+root), sock, b)
	cli := dial(t, sock)
	eventually(t, "both notes indexed", func() bool {
		st := call(t, cli, "index.status", "kb").(map[string]any)
		return st["docs"] == int64(2) && st["pending_jobs"] == int64(0)
	})
	return cli
}

func hitPaths(res map[string]any) []string {
	var out []string
	for _, h := range res["hits"].([]any) {
		out = append(out, h.(map[string]any)["path"].(string))
	}
	return out
}

// TestABuildsRankerRanksEverySearch: a build's ranker orders every worded search, each hit with
// its score (0 is a score) and the result naming the model; the stored selection cannot be changed
// under it, and the capabilities say the build supplies it.
func TestABuildsRankerRanksEverySearch(t *testing.T) {
	cli := rankedDaemon(t, Rank{Ranker: &longFirst{}})
	res := call(t, cli, "search.query", "kb", "kestrel").(map[string]any)
	if got := hitPaths(res); len(got) != 2 || got[0] != "long.md" {
		t.Fatalf("ranked: %v, want long.md first", got)
	}
	if rk := res["rank"].(map[string]any); rk["state"] != "ready" || rk["model"] != "slm-ranker" {
		t.Errorf("rank %v", rk)
	}
	for _, h := range res["hits"].([]any) {
		h := h.(map[string]any)
		want := 0.0
		if h["path"] == "long.md" {
			want = 1
		}
		if s, ok := h["rank_score"].(float64); !ok || s != want {
			t.Errorf("%s: rank_score %v, want %v", h["path"], h["rank_score"], want)
		}
	}
	var re *golibrpc.Error
	if _, err := cli.Call(context.Background(), "ranker.use", ""); !errors.As(err, &re) || re.Message != "this build supplies its ranker" {
		t.Errorf("ranker.use under a build's ranker: %v", err)
	}
	caps := call(t, cli, "sys.capabilities").(map[string]any)
	if rk, _ := caps["ranker"].(map[string]any); rk["supplied"] != true || rk["model"] != "slm-ranker" {
		t.Errorf("capabilities' ranker: %v", caps["ranker"])
	}
	list := call(t, cli, "ranker.list").(map[string]any)
	if list["supplied"] != "slm-ranker" {
		t.Errorf("ranker.list: %v", list)
	}
}

// TestRankedOrNothingRefusesWithItsCode: under Required, a search the build's ranker cannot rank is
// refused with CodeRankUnavailable and a constant message, never the ranker's own text, and no
// hits; a search it can rank is answered.
func TestRankedOrNothingRefusesWithItsCode(t *testing.T) {
	r := &longFirst{}
	cli := rankedDaemon(t, Rank{Ranker: r, Required: true})
	if res := call(t, cli, "search.query", "kb", "kestrel").(map[string]any); hitPaths(res)[0] != "long.md" {
		t.Fatalf("ranked: %v", hitPaths(res))
	}
	r.down.Store(true)
	res, err := cli.Call(context.Background(), "search.query", "kb", "kestrel")
	var re *golibrpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeRankUnavailable || res != nil {
		t.Fatalf("a search the ranker cannot rank: %v, %v; want CodeRankUnavailable and nothing", res, err)
	}
	if strings.Contains(re.Message, "secret") || strings.Contains(re.Message, "slm") {
		t.Errorf("the refusal carries the ranker's text: %q", re.Message)
	}
	// without Required, the same failure answers in recall order, its state error
	r2 := &longFirst{}
	r2.down.Store(true)
	cli2 := rankedDaemon(t, Rank{Ranker: r2})
	res2 := call(t, cli2, "search.query", "kb", "kestrel").(map[string]any)
	if got := hitPaths(res2); len(got) != 2 || got[0] != "short.md" {
		t.Errorf("recall order: %v", got)
	}
	if rk := res2["rank"].(map[string]any); rk["state"] != "error" {
		t.Errorf("rank %v", rk)
	}
}

// TestABuildsTextsAreWhatItsRankerReads: a build's texts replace the chunks' for the ranker: here
// they name short.md "long", and it ranks first.
func TestABuildsTextsAreWhatItsRankerReads(t *testing.T) {
	texts := func(_ context.Context, hits []search.Hit) ([]string, error) {
		out := make([]string, len(hits))
		for i, h := range hits {
			if h.Path == "short.md" {
				out[i] = "long"
			}
		}
		return out, nil
	}
	cli := rankedDaemon(t, Rank{Ranker: &longFirst{}, Texts: texts})
	if got := hitPaths(call(t, cli, "search.query", "kb", "kestrel").(map[string]any)); len(got) != 2 || got[0] != "short.md" {
		t.Errorf("ranked by the build's texts: %v, want short.md first", got)
	}
}
