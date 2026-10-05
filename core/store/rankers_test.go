package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestARankersKeyIsSealedToItsRow: the key reads back only through RankerWithKey, the row holds
// ciphertext, the listing says only that there is one, and a rename never reseals it: it still
// opens under the same row id.
func TestARankersKeyIsSealedToItsRow(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "autodoc.db"))
	info, err := s.AddRanker(ctx, RankerSpec{Name: "cohere", Kind: KindRerankAPI, BaseURL: "https://api.cohere.com/v2", Model: "rerank-v3.5", Key: strp("co-secret")})
	if err != nil || !info.HasKey {
		t.Fatalf("add: %+v, %v", info, err)
	}
	var stored []byte
	if err := one(t, s.raw(t), "SELECT api_key FROM ranker WHERE name = 'cohere'", &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) == 0 || bytes.Contains(stored, []byte("co-secret")) {
		t.Fatalf("the row holds %q: not ciphertext", stored)
	}
	if err := s.UpdateRanker(ctx, "cohere", RankerSpec{Name: "renamed", Kind: KindRerankAPI, BaseURL: "https://api.cohere.com/v2", Model: "rerank-v3.5"}); err != nil {
		t.Fatal(err)
	}
	got, key, err := s.RankerWithKey(ctx, "renamed")
	if err != nil || key != "co-secret" || got.ID != info.ID {
		t.Fatalf("after a rename: %+v, %q, %v", got, key, err)
	}
	// a key moved into another ranker's row does not open there
	other, err := s.AddRanker(ctx, RankerSpec{Name: "other", Kind: KindTEI, BaseURL: "http://tei:8080"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.raw(t).ExecContext(ctx, "UPDATE ranker SET api_key = ? WHERE id = ?", stored, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RankerWithKey(ctx, "other"); err == nil {
		t.Fatal("a key opened in another ranker's row")
	}
	// an embedding provider's key does not open as a ranker's under the same id: the purpose differs
	if _, err := s.AddProvider(ctx, ProviderSpec{Name: "p", Kind: KindOpenAI, BaseURL: "https://x", Model: "m", Key: strp("pk")}); err != nil {
		t.Fatal(err)
	}
	var pk []byte
	if err := one(t, s.raw(t), "SELECT api_key FROM embedding_provider WHERE name = 'p'", &pk); err != nil {
		t.Fatal(err)
	}
	if _, err := s.raw(t).ExecContext(ctx, "UPDATE ranker SET api_key = ? WHERE id = ?", pk, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RankerWithKey(ctx, "other"); err == nil {
		t.Fatal("a provider's key opened as a ranker's")
	}
}

// TestARankerIsCheckedAtTheDoor: a name, a known kind and an http(s) base URL are required, and a
// rerank-API ranker names its model; a TEI ranker's model is the server's, so none is kept.
func TestARankerIsCheckedAtTheDoor(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "autodoc.db"))
	for _, sp := range []RankerSpec{
		{Name: "", Kind: KindTEI, BaseURL: "http://x"},
		{Name: "a", Kind: "ollama", BaseURL: "http://x"},
		{Name: "a", Kind: KindTEI, BaseURL: "ftp://x"},
		{Name: "a", Kind: KindRerankAPI, BaseURL: "http://x"},
	} {
		if _, err := s.AddRanker(ctx, sp); !errors.Is(err, ErrRankerInvalid) {
			t.Errorf("%+v: %v, want ErrRankerInvalid", sp, err)
		}
	}
	info, err := s.AddRanker(ctx, RankerSpec{Name: "tei", Kind: KindTEI, BaseURL: "http://tei:8080", Model: "ignored"})
	if err != nil || info.Model != "" {
		t.Fatalf("tei: %+v, %v; want no model kept", info, err)
	}
	if _, err := s.AddRanker(ctx, RankerSpec{Name: "tei", Kind: KindTEI, BaseURL: "http://other"}); !errors.Is(err, ErrRankerTaken) {
		t.Fatalf("a second ranker named tei: %v", err)
	}
	if err := s.UpdateRanker(ctx, "nope", RankerSpec{Name: "x", Kind: KindTEI, BaseURL: "http://x"}); !errors.Is(err, ErrNoRanker) {
		t.Fatalf("updating none: %v", err)
	}
}

// TestTheRankerInUseFollowsItsRanker: a rename carries ranker.in_use with it, and a removal names
// none, in the same transaction.
func TestTheRankerInUseFollowsItsRanker(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "autodoc.db"))
	for _, n := range []string{"a", "b"} {
		if _, err := s.AddRanker(ctx, RankerSpec{Name: n, Kind: KindTEI, BaseURL: "http://x"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetPreference(ctx, PrefRanker, "a"); err != nil {
		t.Fatal(err)
	}
	inUse := func() string {
		p, err := s.Preferences(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return p[PrefRanker]
	}
	if err := s.UpdateRanker(ctx, "b", RankerSpec{Name: "b2", Kind: KindTEI, BaseURL: "http://x"}); err != nil || inUse() != "a" {
		t.Fatalf("renaming b: %q, %v", inUse(), err)
	}
	if err := s.UpdateRanker(ctx, "a", RankerSpec{Name: "a2", Kind: KindTEI, BaseURL: "http://x"}); err != nil || inUse() != "a2" {
		t.Fatalf("renaming a: %q, %v", inUse(), err)
	}
	if err := s.RemoveRanker(ctx, "a2"); err != nil || inUse() != "" {
		t.Fatalf("removing a2: %q, %v", inUse(), err)
	}
}

// TestARankersUsageAndLogAreItsOwn: a ranker's calls add up by day and its log keeps the latest,
// apart from any embedding provider's, and go with it.
func TestARankersUsageAndLogAreItsOwn(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "autodoc.db"))
	r, err := s.AddRanker(ctx, RankerSpec{Name: "tei", Kind: KindTEI, BaseURL: "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.AddProvider(ctx, ProviderSpec{Name: "local", Kind: KindOllama, BaseURL: "http://x", Model: "m"})
	if err != nil || p.ID != r.ID {
		t.Fatalf("a provider with the ranker's id: %+v, %v", p, err) // so a mix-up between the tables would show
	}
	day := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	var calls []CallRecord
	for range 203 {
		calls = append(calls, CallRecord{At: day, Texts: 40, Millis: 12, Outcome: "ok"})
	}
	if err := s.RecordRankerCalls(ctx, r.ID, calls); err != nil {
		t.Fatal(err)
	}
	usage, err := s.RankerUsage(ctx, "tei", 7)
	if err != nil || len(usage) != 1 || usage[0].Requests != 203 || usage[0].Texts != 8120 {
		t.Fatalf("usage %+v, %v", usage, err)
	}
	log, err := s.RankerLog(ctx, "tei", 1000)
	if err != nil || len(log) != logKept {
		t.Fatalf("log %d, %v", len(log), err)
	}
	if pu, _ := s.ProviderUsage(ctx, "local", 7); len(pu) != 0 {
		t.Fatalf("the provider with the same id has the ranker's usage: %+v", pu)
	}
	if err := s.RemoveRanker(ctx, "tei"); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := one(t, s.raw(t), "SELECT (SELECT COUNT(*) FROM ranker_usage) + (SELECT COUNT(*) FROM ranker_log)", &left); err != nil || left != 0 {
		t.Fatalf("after the ranker went, %d usage and log rows are left (%v)", left, err)
	}
	if _, err := s.RankerUsage(ctx, "tei", 7); !errors.Is(err, ErrNoRanker) {
		t.Fatalf("usage of none: %v", err)
	}
}
