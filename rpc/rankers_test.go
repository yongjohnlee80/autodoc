package rpc

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"

	"github.com/yongjohnlee80/golib/search/rank"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// fakeRankers records what the verbs hand it, and answers with err when it is set.
type fakeRankers struct {
	err      error
	supplied string
	got      []any
}

func (f *fakeRankers) note(v ...any) error { f.got = append(f.got, v); return f.err }

func (f *fakeRankers) ListRankers(context.Context) (RankerList, error) {
	return RankerList{Rankers: []store.RankerInfo{{ID: 7, Name: "tei", Kind: store.KindTEI, BaseURL: "http://h:1", HasKey: true}},
		Active: "tei", Window: 25, Err: "", Supplied: f.supplied}, f.err
}
func (f *fakeRankers) AddRanker(_ context.Context, sp store.RankerSpec) (store.RankerInfo, error) {
	return store.RankerInfo{Name: sp.Name, Kind: sp.Kind, BaseURL: sp.BaseURL, Model: sp.Model, HasKey: sp.Key != nil && *sp.Key != ""},
		f.note("add", sp.Name, sp.Key != nil)
}
func (f *fakeRankers) UpdateRanker(_ context.Context, name string, sp store.RankerSpec) error {
	return f.note("update", name, sp.Name, sp.Key != nil)
}
func (f *fakeRankers) RemoveRanker(_ context.Context, name string) error {
	return f.note("remove", name)
}
func (f *fakeRankers) Use(_ context.Context, name string) error { return f.note("use", name) }
func (f *fakeRankers) SetWindow(_ context.Context, n int) error { return f.note("window", n) }
func (f *fakeRankers) Models(_ context.Context, name string, sp store.RankerSpec) ([]string, error) {
	return []string{"BAAI/bge-reranker-v2-m3"}, f.note("models", name, sp.Kind)
}
func (f *fakeRankers) Usage(_ context.Context, name string, days int) ([]store.Usage, error) {
	return []store.Usage{{Day: "2026-10-05", Requests: 3, Texts: 60}}, f.note("usage", name, days)
}
func (f *fakeRankers) Log(_ context.Context, name string, limit int) ([]store.LogEntry, error) {
	return []store.LogEntry{{At: 1, Texts: 20, Millis: 900, Outcome: "ok"}}, f.note("log", name, limit)
}
func (f *fakeRankers) Supplied() (string, bool) { return f.supplied, f.supplied != "" }

func rankersServer(t *testing.T, f *fakeRankers) *golibrpc.Client {
	t.Helper()
	sock := shortSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := New(Fixed(), "v-test", WithListener(ln), WithRankers(f))
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	r := &rig{t: t, sock: sock}
	return r.dial(true)
}

// TestRankerVerbs: each verb reaches the daemon's rankers with what the client said, a key goes in
// and never comes out, and the answers are as documented.
func TestRankerVerbs(t *testing.T) {
	f := &fakeRankers{}
	cli := rankersServer(t, f)
	list := call(t, cli, "ranker.list").(map[string]any)
	want := map[string]any{"rankers": []any{map[string]any{"name": "tei", "kind": "tei", "base_url": "http://h:1", "model": "", "has_key": true}},
		"active": "tei", "window": int64(25), "error": "", "supplied": ""}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("ranker.list = %v", list)
	}
	added := call(t, cli, "ranker.add", map[string]any{"name": "c", "kind": "rerank-api", "base_url": "https://api", "model": "rerank-v3", "key": "sk-secret"})
	if m := added.(map[string]any); m["has_key"] != true || len(m) != 5 {
		t.Errorf("ranker.add = %v: want has_key and no key", m)
	}
	call(t, cli, "ranker.update", "c", map[string]any{"name": "c2", "kind": "rerank-api", "base_url": "https://api", "model": "m"})
	call(t, cli, "ranker.remove", "c2")
	call(t, cli, "ranker.use", "tei")
	call(t, cli, "ranker.window", 30)
	if got := call(t, cli, "ranker.models", "tei"); !reflect.DeepEqual(got, []any{"BAAI/bge-reranker-v2-m3"}) {
		t.Errorf("ranker.models = %v", got)
	}
	call(t, cli, "ranker.models", map[string]any{"kind": "tei", "base_url": "http://h:2"})
	if got := call(t, cli, "ranker.usage", "tei", 7).([]any); len(got) != 1 || got[0].(map[string]any)["requests"] != int64(3) {
		t.Errorf("ranker.usage = %v", got)
	}
	if got := call(t, cli, "ranker.log", "tei", 10).([]any); len(got) != 1 || got[0].(map[string]any)["outcome"] != "ok" {
		t.Errorf("ranker.log = %v", got)
	}
	wantGot := []any{
		[]any{"add", "c", true}, []any{"update", "c", "c2", false}, []any{"remove", "c2"}, []any{"use", "tei"},
		[]any{"window", 30}, []any{"models", "tei", ""}, []any{"models", "", "tei"}, []any{"usage", "tei", 7}, []any{"log", "tei", 10},
	}
	if !reflect.DeepEqual(f.got, wantGot) {
		t.Errorf("the rankers were asked %v\nwant %v", f.got, wantGot)
	}
	for _, bad := range [][]any{
		{"ranker.add", "not a map"},
		{"ranker.add", map[string]any{"name": 1}},
		{"ranker.add", map[string]any{"name": "x", "key": 1}},
		{"ranker.window", "thirty"},
		{"ranker.use"},
	} {
		if _, err := cli.Call(context.Background(), bad[0].(string), bad[1:]...); code(err) != golibrpc.CodeInvalidParams {
			t.Errorf("%v: %v, want invalid params", bad, err)
		}
	}
}

// TestRankerRefusals: the daemon's refusals reach the client as their codes and constant messages,
// and a server keeping no rankers says so; its capabilities supply none.
func TestRankerRefusals(t *testing.T) {
	f := &fakeRankers{supplied: "slm"}
	cli := rankersServer(t, f)
	if got := call(t, cli, "sys.capabilities").(map[string]any)["ranker"]; !reflect.DeepEqual(got, map[string]any{"supplied": true, "model": "slm"}) {
		t.Errorf("capabilities' ranker = %v", got)
	}
	for _, tc := range []struct {
		err  error
		code int64
		msg  string
	}{
		{ErrSuppliedRanker, golibrpc.CodeInvalidParams, "this build supplies its ranker"},
		{ErrWindow, golibrpc.CodeInvalidParams, "a ranker's window is 10 to 100 candidates"},
		{store.ErrNoRanker, CodeNotFound, "no such ranker"},
		{store.ErrRankerTaken, CodeConflict, "another ranker has this name"},
		{errors.Join(rank.ErrNotARanker, errors.New("TEI at http://10.0.0.9 serves secret/model")), CodeProviderRefused, "the server's model is not a re-ranker"},
	} {
		f.err = tc.err
		_, err := cli.Call(context.Background(), "ranker.use", "x")
		if c, msg := wireError(err); c != tc.code || msg != tc.msg {
			t.Errorf("%v: %d %q, want %d %q", tc.err, c, msg, tc.code, tc.msg)
		}
	}
	none := serve(t).dial(true)
	for _, verb := range [][]any{{"ranker.list"}, {"ranker.use", "x"}, {"ranker.window", 20}} {
		if _, err := none.Call(context.Background(), verb[0].(string), verb[1:]...); code(err) != CodeUnsupported {
			t.Errorf("%s without Rankers: %v, want unsupported", verb[0], err)
		}
	}
	if got := call(t, none, "sys.capabilities").(map[string]any)["ranker"]; !reflect.DeepEqual(got, map[string]any{"supplied": false}) {
		t.Errorf("capabilities' ranker without Rankers = %v", got)
	}
}

// TestSearchCarriesTheRanking: a ranked hit carries its score, 0 included, and an unranked one
// none; the result's rank is off when nothing ranked it.
func TestSearchCarriesTheRanking(t *testing.T) {
	zero, one := 0.0, 1.0
	ranked := resultMap(index.Result{Hits: []index.Hit{{Path: "a.md", RankScore: &one}, {Path: "b.md", RankScore: &zero}},
		Rank: index.RankState{State: "ready", Model: "bge"}})
	hits := ranked["hits"].([]any)
	if s, ok := hits[1].(map[string]any)["rank_score"]; !ok || s != 0.0 {
		t.Errorf("a hit ranked 0: rank_score %v, %v", s, ok)
	}
	if !reflect.DeepEqual(ranked["rank"], map[string]any{"state": "ready", "model": "bge", "error": ""}) {
		t.Errorf("rank = %v", ranked["rank"])
	}
	plain := resultMap(index.Result{Hits: []index.Hit{{Path: "a.md"}}})
	if _, ok := plain["hits"].([]any)[0].(map[string]any)["rank_score"]; ok {
		t.Error("an unranked hit carries a rank_score")
	}
	if !reflect.DeepEqual(plain["rank"], map[string]any{"state": "off", "model": "", "error": ""}) {
		t.Errorf("unranked rank = %v", plain["rank"])
	}
}
