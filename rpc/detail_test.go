package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yongjohnlee80/autodoc/core/module"
)

var (
	errFakeInvalid = errors.New("fake: invalid")
	errFakeRefused = errors.New("fake: refused")
)

type failParams struct {
	Which string `param:"which"`
	Text  string `param:"text"`
}

// detailModule is a module named todos whose verb fails with the error and the text it is given:
// "invalid", whose catalog entry sends its detail, or "refused", whose entry does not.
func detailModule() fakeModule {
	fail := module.Handle(module.Contract{Verb: "todos.fail", Since: 12, Errors: []int64{-32072, -32073}},
		func(_ context.Context, p failParams) (bool, error) {
			if p.Which == "invalid" {
				return false, fmt.Errorf("todo: put a.md: %w: %s", errFakeInvalid, p.Text)
			}
			return false, fmt.Errorf("%w: %s", errFakeRefused, p.Text)
		})
	return fakeModule{name: "todos", verbs: []module.Verb{fail}, errs: []module.PublicError{
		{Err: errFakeInvalid, Code: -32072, Name: "todos.invalid", Message: "a value the rules refuse", Detail: true},
		{Err: errFakeRefused, Code: -32073, Name: "todos.refused", Message: "refused"},
	}}
}

// A catalogued error whose entry sends its detail crosses the wire as "<message>: <detail>": the
// text after its own, on one line, bounded. One whose entry does not crosses as its message alone,
// however much its wrapping says. Code is the contract either way. The mutant, named here: every
// error's detail sent (the refused one's host path crosses).
func TestACataloguedErrorsDetailCrossesOnlyWhenItsEntrySays(t *testing.T) {
	cli, _ := moduleServer(t, detailModule())
	ctx := context.Background()
	long := strings.Repeat("é", maxDetail)
	for _, c := range []struct {
		name, which, text string
		code              int64
		want              string
	}{
		{"its detail", "invalid", "title is required", -32072, "a value the rules refuse: title is required"},
		{"on one line", "invalid", "line one\nline\ttwo  ", -32072, "a value the rules refuse: line one line two"},
		{"none past its own", "invalid", "", -32072, "a value the rules refuse"},
		{"no detail sent", "refused", "disk full at /secret/path", -32073, "refused"},
	} {
		_, err := cli.Call(ctx, "todos.fail", c.which, c.text)
		if code, msg := wireError(err); code != c.code || msg != c.want {
			t.Errorf("%s: %d %q, want %d %q", c.name, code, msg, c.code, c.want)
		}
	}
	_, err := cli.Call(ctx, "todos.fail", "invalid", long)
	_, msg := wireError(err)
	d := strings.TrimPrefix(msg, "a value the rules refuse: ")
	if !utf8.ValidString(msg) || !strings.HasSuffix(d, "…") || len(d) > maxDetail+len("…") || len(d) < maxDetail-4 {
		t.Errorf("a long detail: %d bytes, valid %v, %q…; want at most %d and a cut mark", len(d), utf8.ValidString(msg), d[:min(len(d), 20)], maxDetail)
	}
}
