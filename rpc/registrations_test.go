package rpc

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// workspace.list names the text extensions a registration of the build reads instead (Protocol
// 10), as an empty list when there are none.
func TestWorkspaceListNamesTextCollisions(t *testing.T) {
	got := asWire(t, listed(&Workspace{Name: "kb", TextCollisions: func() []string { return []string{".go"} }}))["text_collisions"]
	if !reflect.DeepEqual(got, []any{".go"}) {
		t.Errorf("text_collisions %#v", got)
	}
	if got := asWire(t, listed(&Workspace{Name: "kb"}))["text_collisions"]; !reflect.DeepEqual(got, []any{}) {
		t.Errorf("none: %#v", got)
	}
}

// A held document reaches the wire (Protocol 10): a hit's hold, index.status's counts, and
// index.reindex's refusal as invalid parameters naming the way out.
func TestHeldDocumentsOnTheWire(t *testing.T) {
	hits := asWire(t, resultOf(index.Result{Hits: []index.Hit{{Path: "a.go", Hold: index.HoldStale}, {Path: "n.md"}}}))["hits"].([]any)
	if hits[0].(map[string]any)["hold"] != "stale" || hits[1].(map[string]any)["hold"] != "" {
		t.Errorf("hits %v", hits)
	}
	st := asWire(t, statusOf(index.Status{Held: 3, HeldStale: 1, HeldUnchecked: 2}, &Workspace{}))
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

// index.status's optional parts reach the wire as they always have: following, warming, and the
// embeddings with their queue and the texts set aside.
func TestStatusOnTheWire(t *testing.T) {
	at := time.Unix(1700000000, 0)
	st := index.Status{Docs: 2, Failing: []index.JobError{{Path: "a.md", Attempts: 3, Err: "boom"}},
		Embeddings: &index.EmbeddingStatus{Provider: "ollama", Model: "fp1", Target: "fp2", Texts: 9, Pending: 4, Semantic: "switching",
			LastErr: "slow", Refused: 1, TargetPending: 5, TargetRefused: 2,
			RefusedTexts: []index.RefusedText{{Path: "a.md", Breadcrumb: "A", Error: "too long", Hash: "h", Bytes: 10, Tokens: 3, RetryAt: at}}}}
	w := &Workspace{Following: func() follow.Status { return follow.Status{Following: "watching", Retrying: []string{"x"}} },
		Warming: func() []string { return []string{"restarting"} }, EmbeddingQueue: func() (string, string) { return "", "" }}
	want := map[string]any{"cursor": int64(0), "oldest_retained": int64(0), "docs": int64(2), "chunks": int64(0), "pending_jobs": int64(0),
		"unparsed_frontmatter": []any{}, "diagnosed": int64(0), "held": int64(0), "held_stale": int64(0), "held_unchecked": int64(0),
		"failing":   []any{map[string]any{"path": "a.md", "attempts": int64(3), "error": "boom"}},
		"following": map[string]any{"mode": "watching", "error": "", "retrying": []any{"x"}},
		"warming":   []any{"restarting"},
		"embeddings": map[string]any{"provider": "ollama", "model": "fp1", "target": "fp2", "texts": int64(9), "pending": int64(4),
			"semantic": "switching", "last_error": "slow", "refused": int64(1), "target_pending": int64(5), "target_refused": int64(2),
			"refused_texts": []any{map[string]any{"path": "a.md", "breadcrumb": "A", "error": "too long", "bytes": int64(10),
				"estimated_tokens": int64(3), "retry_at": at.Unix()}},
			"queue_state": "", "waiting_for": ""}}
	if got := asWire(t, statusOf(st, w)); !reflect.DeepEqual(got, want) {
		t.Errorf("status\n got %v\nwant %v", got, want)
	}
	if got := asWire(t, statusOf(index.Status{}, &Workspace{Warming: func() []string { return nil }})); got["warming"] != nil || got["embeddings"] != nil || got["following"] != nil {
		t.Errorf("a bare status carries %v", got)
	}
}
