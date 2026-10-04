package rpc

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// workspace.list names the text extensions a registration of the build reads instead (Protocol
// 10), as an empty list when there are none.
func TestWorkspaceListNamesTextCollisions(t *testing.T) {
	got := workspaceMap(&Workspace{Name: "kb", TextCollisions: func() []string { return []string{".go"} }})["text_collisions"]
	if !reflect.DeepEqual(got, []any{".go"}) {
		t.Errorf("text_collisions %#v", got)
	}
	if got := workspaceMap(&Workspace{Name: "kb"})["text_collisions"]; !reflect.DeepEqual(got, []any{}) {
		t.Errorf("none: %#v", got)
	}
}

// A held document reaches the wire (Protocol 10): a hit's hold, index.status's counts, and
// index.reindex's refusal as invalid parameters naming the way out.
func TestHeldDocumentsOnTheWire(t *testing.T) {
	hits := resultMap(index.Result{Hits: []index.Hit{{Path: "a.go", Hold: index.HoldStale}, {Path: "n.md"}}})["hits"].([]any)
	if hits[0].(map[string]any)["hold"] != "stale" || hits[1].(map[string]any)["hold"] != "" {
		t.Errorf("hits %v", hits)
	}
	st := statusMap(index.Status{Held: 3, HeldStale: 1, HeldUnchecked: 2}, &Workspace{})
	if st["held"] != int64(3) || st["held_stale"] != int64(1) || st["held_unchecked"] != int64(2) {
		t.Errorf("status %v", st)
	}
	var re *golibrpc.Error
	err := wireErr(fmt.Errorf("reindex: %w", &index.HeldError{Path: "a.go", Ext: ".go", What: "chunker"}))
	if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams || !strings.HasPrefix(re.Message, "held: this backend has no chunker for .go") {
		t.Errorf("the refusal: %v", err)
	}
}

// sys.capabilities reports the build's registrations, read back as they were; without them, a
// Protocol 10 answer is a protocol error, never none.
func TestCapabilitiesReportTheRegistrations(t *testing.T) {
	pro := registrations.Tables{Chunkers: map[string]string{".go": "code-go-1+text-5"},
		Formats: map[string]registrations.Format{".pdf": {ID: "autorag/pdf", Version: "1"}}}
	got, err := RegistrationsOf(map[string]any{"registrations": RegistrationsMap(pro)})
	if err != nil || got.Fingerprint() != pro.Fingerprint() {
		t.Fatalf("read back %+v, %v", got, err)
	}
	if m := RegistrationsMap(pro); m["fingerprint"] != pro.Fingerprint() {
		t.Errorf("fingerprint %v", m["fingerprint"])
	}
	for _, caps := range []any{nil, map[string]any{"databases": true}, map[string]any{"registrations": map[string]any{"chunkers": map[string]any{}}},
		map[string]any{"registrations": map[string]any{"chunkers": map[string]any{".go": int64(1)}, "formats": map[string]any{}}}} {
		if _, err := RegistrationsOf(caps); !errors.Is(err, ErrNoRegistrations) {
			t.Errorf("%v: %v, want a protocol error", caps, err)
		}
	}
	r := serve(t)
	cli := r.dial(true)
	community, err := RegistrationsOf(call(t, cli, "sys.capabilities"))
	if err != nil || len(community.Chunkers)+len(community.Formats) != 0 || community.Fingerprint() != (registrations.Tables{}).Fingerprint() {
		t.Fatalf("the community build's: %+v, %v", community, err)
	}
}
