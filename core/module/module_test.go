package module_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// where is a param that decodes itself: a name, or {root = "…"}.
type where struct{ Name, Root string }

func (w *where) DecodeParam(v any) error {
	switch x := v.(type) {
	case string:
		w.Name = x
		return nil
	case map[string]any:
		if r, ok := x["root"].(string); ok {
			w.Root = r
			return nil
		}
	}
	return errors.New("not a where")
}
func (*where) ParamType() string { return "name | {root}" }

type getParams struct {
	At   where  `param:"at" doc:"where the store is"`
	ID   string `param:"id"`
	Opts struct {
		Limit int  `json:"limit"`
		Deep  bool `json:"deep"`
	} `param:"opts,optional"`
}

type task struct {
	ID    string   `json:"id"`
	Tags  []string `json:"tags,omitempty"`
	Note  string   `json:"note,omitempty"`
	Skip  string   `json:"-"`
	Count int64    `json:"count"`
}

func getVerb(got *getParams) module.Verb {
	return module.Handle(module.Contract{Verb: "x.get", Since: 20},
		func(_ context.Context, p getParams) (task, error) {
			*got = p
			return task{ID: p.ID, Count: int64(p.Opts.Limit)}, nil
		})
}

func TestHandleDerivesTheParamsFromTheType(t *testing.T) {
	var got getParams
	v := getVerb(&got)
	want := []module.Param{
		{Name: "at", Type: "name | {root}", Doc: "where the store is"},
		{Name: "id", Type: "string"},
		{Name: "opts", Type: "map", Optional: true},
	}
	if !reflect.DeepEqual(v.Params, want) {
		t.Fatalf("params %+v, want %+v", v.Params, want)
	}
	if v.Required() != 2 {
		t.Errorf("required %d, want 2", v.Required())
	}
}

func TestHandleDecodesEachParam(t *testing.T) {
	var got getParams
	v := getVerb(&got)
	out, err := v.Call(context.Background(), []any{map[string]any{"root": "/p"}, "t1", map[string]any{"limit": int64(3), "deep": true}})
	if err != nil {
		t.Fatal(err)
	}
	if got.At.Root != "/p" || got.ID != "t1" || got.Opts.Limit != 3 || !got.Opts.Deep {
		t.Errorf("decoded %+v", got)
	}
	if out.(task).Count != 3 {
		t.Errorf("result %+v", out)
	}
	// the optional param left out is its zero value
	if _, err := v.Call(context.Background(), []any{"kb", "t2"}); err != nil || got.At.Name != "kb" || got.Opts.Limit != 0 {
		t.Errorf("without opts: %v %+v", err, got)
	}
}

func TestHandleRefusesWhatDoesNotFit(t *testing.T) {
	var got getParams
	v := getVerb(&got)
	for _, tc := range []struct {
		name   string
		params []any
		param  string
	}{
		{"a wrong type", []any{"kb", int64(1)}, "id"},
		{"a required one missing", []any{"kb"}, "id"},
		{"a nil required one", []any{"kb", nil}, "id"},
		{"a decoder refusing", []any{int64(1), "t"}, "at"},
		{"an unknown option", []any{"kb", "t", map[string]any{"nope": true}}, "opts"},
		{"too many", []any{"kb", "t", nil, "extra"}, "params"},
	} {
		_, err := v.Call(context.Background(), tc.params)
		var pe *module.ParamError
		if !errors.As(err, &pe) || pe.Param != tc.param || !errors.Is(err, module.ErrInvalidParams) {
			t.Errorf("%s: %v, want a ParamError for %s", tc.name, err, tc.param)
		}
	}
}

func TestHandleTakesTheWiresIntegers(t *testing.T) {
	type p struct {
		N int64 `param:"n"`
	}
	var got int64
	v := module.Handle(module.Contract{Verb: "x.n"}, func(_ context.Context, q p) (any, error) { got = q.N; return nil, nil })
	for _, n := range []any{int64(7), uint64(7), float64(7), 7} {
		got = 0
		if _, err := v.Call(context.Background(), []any{n}); err != nil || got != 7 {
			t.Errorf("%T: %v, got %d", n, err, got)
		}
	}
	if _, err := v.Call(context.Background(), []any{7.5}); err == nil {
		t.Error("a fraction was taken as an integer")
	}
}

func TestHandlePanicsOnWhatItCannotExpress(t *testing.T) {
	type badOrder struct {
		A string `param:"a,optional"`
		B string `param:"b"`
	}
	type badType struct {
		C chan int `param:"c"`
	}
	for name, f := range map[string]func(){
		"given params": func() {
			module.Handle(module.Contract{Verb: "x", Params: []module.Param{{Name: "a"}}}, func(context.Context, struct{}) (any, error) { return nil, nil })
		},
		"a non-struct": func() {
			module.Handle(module.Contract{Verb: "x"}, func(context.Context, string) (any, error) { return nil, nil })
		},
		"required after optional": func() {
			module.Handle(module.Contract{Verb: "x"}, func(context.Context, badOrder) (any, error) { return nil, nil })
		},
		"a type the wire lacks": func() {
			module.Handle(module.Contract{Verb: "x"}, func(context.Context, badType) (any, error) { return nil, nil })
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}

func TestWireCarriesAStructByItsJSONTags(t *testing.T) {
	at := time.Date(2026, 10, 11, 9, 30, 0, 0, time.FixedZone("", 9*3600))
	got, err := module.Wire(struct {
		T     task      `json:"task"`
		List  []task    `json:"list"`
		None  []task    `json:"none"`
		At    time.Time `json:"at"`
		Bytes []byte    `json:"bytes"`
		Ptr   *task     `json:"ptr"`
	}{T: task{ID: "a", Skip: "x", Count: 2}, List: []task{{ID: "b", Tags: []string{"t"}}}, At: at, Bytes: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"task":  map[string]any{"id": "a", "count": int64(2)},
		"list":  []any{map[string]any{"id": "b", "tags": []any{"t"}, "count": int64(0)}},
		"none":  []any{},
		"at":    "2026-10-11T09:30:00+09:00",
		"bytes": []byte("hi"),
		"ptr":   nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wire\n got %#v\nwant %#v", got, want)
	}
	if _, err := module.Wire(map[int]string{1: "a"}); err == nil {
		t.Error("a map with integer keys was carried")
	}
	if _, err := module.Wire(make(chan int)); err == nil {
		t.Error("a channel was carried")
	}
}

func TestEventsOfNamesTheParams(t *testing.T) {
	v := module.Verb{Contract: module.Contract{Verb: "workspace.rename",
		Params: []module.Param{{Name: "name"}, {Name: "new name"}},
		Event:  &module.EventSpec{Kind: "workspace.renamed", Workspace: "name", Detail: "new name"}}}
	got := v.EventsOf(context.Background(), []any{"kb", "wiki"}, nil)
	if want := []module.Event{{Kind: "workspace.renamed", Workspace: "kb", Detail: "wiki"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("events %+v, want %+v", got, want)
	}
	v.Event = nil
	if got := v.EventsOf(context.Background(), []any{"kb", "wiki"}, nil); got != nil {
		t.Errorf("a verb with no event logged %+v", got)
	}
	if v.Logs() {
		t.Error("a verb with no event says it logs")
	}
	v.Events = func(_ context.Context, p []any, result any) []module.Event {
		return []module.Event{{Kind: "todos.changed", Detail: result.(string)}}
	}
	if got := v.EventsOf(context.Background(), nil, "t1"); len(got) != 1 || got[0].Detail != "t1" {
		t.Errorf("a resolver's events %+v", got)
	}
	if !v.Logs() {
		t.Error("a verb with only a resolver says it logs nothing")
	}
}

type mod struct{ errs []module.PublicError }

func (mod) Name() string                   { return "todos" }
func (mod) Verbs() []module.Verb           { return nil }
func (m mod) Errors() []module.PublicError { return m.errs }

type other struct{ mod }

func (other) Name() string { return "nobody" }

func TestCheckCodesKeepsAModuleInItsRange(t *testing.T) {
	ok := mod{[]module.PublicError{{Code: -32071, Name: "todos.stale"}, {Code: -32089, Name: "todos.last"}}}
	if err := module.CheckCodes(ok); err != nil {
		t.Error(err)
	}
	if err := module.CheckCodes(mod{[]module.PublicError{{Code: -32050, Name: "todos.elsewhere"}}}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("a code outside the range: %v", err)
	}
	if err := module.CheckCodes(other{ok}); err == nil {
		t.Error("a module with no range was let through")
	}
	if err := module.CheckCodes(other{mod{[]module.PublicError{{Code: -32060, Name: "no_such_workspace"}}}}); err != nil {
		t.Errorf("a shared code was refused: %v", err)
	}
}

func TestTheContextCarriesWhatTheHostResolved(t *testing.T) {
	ctx := module.WithClient(module.WithProtocol(module.WithWorkspace(context.Background(), module.Workspace{Name: "kb", Root: "/kb"}), 20), "autodoc-tui#1")
	if w, ok := module.WorkspaceFrom(ctx); !ok || w.Root != "/kb" {
		t.Errorf("workspace %+v %v", w, ok)
	}
	if module.ProtocolFrom(ctx) != 20 || module.ClientFrom(ctx) != "autodoc-tui#1" {
		t.Error("protocol or client lost")
	}
	if _, ok := module.WorkspaceFrom(context.Background()); ok {
		t.Error("a workspace from an empty context")
	}
}

func TestScopeAndAudienceName(t *testing.T) {
	if module.ScopeWorkspace.String() != "workspace" || module.ScopeLocalPeer.String() != "local-peer" || module.ScopeGlobal.String() != "global" {
		t.Error("scope names")
	}
	if module.AudienceUser.String() != "user" || module.AudienceInternal.String() != "internal" || module.AudienceAgent.String() != "agent" {
		t.Error("audience names")
	}
}

func TestHandleDecodesEveryWireType(t *testing.T) {
	type all struct {
		B    bool           `param:"b"`
		Raw  []byte         `param:"raw"`
		Text []byte         `param:"text"`
		List []string       `param:"list"`
		Map  map[string]any `param:"map"`
		Any  any            `param:"any"`
	}
	var got all
	v := module.Handle(module.Contract{Verb: "x.all"}, func(_ context.Context, p all) (any, error) { got = p; return nil, nil })
	types := []string{}
	for _, p := range v.Params {
		types = append(types, p.Type)
	}
	if want := []string{"bool", "bytes", "bytes", "[]string", "map", "any"}; !reflect.DeepEqual(types, want) {
		t.Errorf("types %v, want %v", types, want)
	}
	in := []any{true, []byte{1}, "hi", []any{"a", "b"}, map[string]any{"k": int64(1)}, int64(9)}
	if _, err := v.Call(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if !got.B || string(got.Text) != "hi" || got.Raw[0] != 1 || len(got.List) != 2 || got.Map["k"] != int64(1) || got.Any != int64(9) {
		t.Errorf("decoded %+v", got)
	}
	for i, bad := range [][]any{
		{"no", []byte{1}, "hi", []any{}, map[string]any{}, nil},
		{true, int64(1), "hi", []any{}, map[string]any{}, nil},
		{true, []byte{1}, "hi", []any{int64(1)}, map[string]any{}, nil},
		{true, []byte{1}, "hi", "a", map[string]any{}, nil},
		{true, []byte{1}, "hi", []any{}, "m", nil},
	} {
		if _, err := v.Call(context.Background(), bad); !errors.Is(err, module.ErrInvalidParams) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func TestCheckRefusesAContractAHostCannotServe(t *testing.T) {
	good := module.Contract{Verb: "workspace.rename", Params: []module.Param{{Name: "name"}, {Name: "new name"}},
		Event: &module.EventSpec{Kind: "workspace.renamed", Workspace: "name", Detail: "new name"}}
	if err := good.Check(); err != nil {
		t.Error(err)
	}
	for name, c := range map[string]module.Contract{
		"no name":                 {Params: good.Params},
		"an event's lost param":   {Verb: "x", Params: []module.Param{{Name: "name"}}, Event: &module.EventSpec{Kind: "k", Detail: "new name"}},
		"required after optional": {Verb: "x", Params: []module.Param{{Name: "a", Optional: true}, {Name: "b"}}},
	} {
		if c.Check() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
