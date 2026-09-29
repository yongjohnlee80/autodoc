package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/sqlite"
)

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func strp(s string) *string { return &s }

// TestAProvidersKeyIsSealed: the key reads back only through ProviderWithKey, the row holds
// ciphertext, the keyslot is made 0600 beside the store, and a client's listing says only that
// there is a key.
func TestAProvidersKeyIsSealed(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	s := openAt(t, path)
	info, err := s.AddProvider(ctx, ProviderSpec{Name: "work", Kind: KindOpenAI, BaseURL: "https://api.example", Model: "m", Key: strp("sk-secret-123")})
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasKey {
		t.Fatal("the listing does not say the provider has a key")
	}
	_, key, err := s.ProviderWithKey(ctx, "work")
	if err != nil || key != "sk-secret-123" {
		t.Fatalf("ProviderWithKey = %q, %v", key, err)
	}
	fi, err := os.Stat(path + ".key")
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the keyslot: %v, %v; want a 0600 file beside the store", fi, err)
	}
	raw := s.raw(t)
	var stored []byte
	if err := one(t, raw, "SELECT api_key FROM embedding_provider WHERE name = 'work'", &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) == 0 || bytes.Contains(stored, []byte("sk-secret-123")) {
		t.Fatalf("the row holds %q: not ciphertext", stored)
	}
	// nil keeps the key, "" removes it
	if err := s.UpdateProvider(ctx, "work", ProviderSpec{Name: "work", Kind: KindOpenAI, BaseURL: "https://api.example", Model: "m2"}); err != nil {
		t.Fatal(err)
	}
	if _, key, _ := s.ProviderWithKey(ctx, "work"); key != "sk-secret-123" {
		t.Fatalf("an update without a key lost it: %q", key)
	}
	if err := s.UpdateProvider(ctx, "work", ProviderSpec{Name: "work", Kind: KindOpenAI, BaseURL: "https://api.example", Model: "m2", Key: strp("")}); err != nil {
		t.Fatal(err)
	}
	if ps, _ := s.Providers(ctx); len(ps) != 1 || ps[0].HasKey || ps[0].Model != "m2" {
		t.Fatalf("after removing the key: %+v", ps)
	}
}

// TestASealedKeyOpensOnlyInItsRow: moved to another provider's row, a key does not open; nor
// under another keyslot.
func TestASealedKeyOpensOnlyInItsRow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openAt(t, filepath.Join(dir, "autodoc.db"))
	for _, n := range []string{"a", "b"} {
		if _, err := s.AddProvider(ctx, ProviderSpec{Name: n, Kind: KindOpenAI, BaseURL: "https://x.example", Model: "m", Key: strp("key-" + n)}); err != nil {
			t.Fatal(err)
		}
	}
	raw := s.raw(t)
	if _, err := raw.ExecContext(ctx, "UPDATE embedding_provider SET api_key = (SELECT api_key FROM embedding_provider WHERE name = 'a') WHERE name = 'b'"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ProviderWithKey(ctx, "b"); !errors.Is(err, ErrSealed) {
		t.Fatalf("a's key in b's row opened: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.db.key"), bytes.Repeat([]byte{7}, keySize), 0o600); err != nil {
		t.Fatal(err)
	}
	other := &keyslot{path: filepath.Join(dir, "other.db.key")}
	var sealed []byte
	if err := one(t, raw, "SELECT api_key FROM embedding_provider WHERE name = 'a'", &sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := other.open(sealed, keyAAD(1)); !errors.Is(err, ErrSealed) {
		t.Fatalf("another keyslot opened a's key: %v", err)
	}
}

// TestAnExposedKeyslotIsRefused: a keyslot others can read is not used.
func TestAnExposedKeyslotIsRefused(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.key")
	if err := os.WriteFile(p, bytes.Repeat([]byte{1}, keySize), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (&keyslot{path: p}).seal([]byte("k"), "aad"); !errors.Is(err, ErrKeyslotExposed) {
		t.Fatalf("a 0644 keyslot: %v, want ErrKeyslotExposed", err)
	}
}

// TestUsageAddsUpByDayAndTheLogKeepsTheLatest: calls add to their day's usage, limits and
// failures counted; the log keeps the last 200; the provider's removal takes both.
func TestUsageAddsUpByDayAndTheLogKeepsTheLatest(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "autodoc.db"))
	p, err := s.AddProvider(ctx, ProviderSpec{Name: "local", Kind: KindOllama, BaseURL: "http://x", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	day1 := time.Date(2026, 9, 28, 23, 0, 0, 0, time.UTC)
	day2 := day1.Add(2 * time.Hour)
	var calls []CallRecord
	for i := 0; i < 205; i++ {
		calls = append(calls, CallRecord{At: day1, Texts: 2, Tokens: 10, Millis: 5, Outcome: "ok"})
	}
	calls = append(calls, CallRecord{At: day2, Texts: 1, Outcome: "rate limited", Failed: true, Limited: true})
	if err := s.RecordCalls(ctx, p.ID, calls); err != nil {
		t.Fatal(err)
	}
	usage, err := s.ProviderUsage(ctx, "local", 7)
	if err != nil || len(usage) != 2 {
		t.Fatalf("usage %+v, %v", usage, err)
	}
	if u := usage[0]; u.Day != "2026-09-29" || u.Requests != 1 || u.Failures != 1 || u.Limited != 1 {
		t.Errorf("the latest day: %+v", u)
	}
	if u := usage[1]; u.Day != "2026-09-28" || u.Requests != 205 || u.Texts != 410 || u.Tokens != 2050 {
		t.Errorf("the first day: %+v", u)
	}
	log, err := s.ProviderLog(ctx, "local", 1000)
	if err != nil || len(log) != logKept || log[0].Outcome != "rate limited" {
		t.Fatalf("the log has %d entries (latest %+v), %v; want the last %d", len(log), log[0], err, logKept)
	}
	if err := s.RemoveProvider(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := one(t, s.raw(t), "SELECT (SELECT COUNT(*) FROM embedding_usage) + (SELECT COUNT(*) FROM embedding_log)", &left); err != nil || left != 0 {
		t.Fatalf("after the provider went, %d usage and log rows are left (%v)", left, err)
	}
	if err := s.RemoveProvider(ctx, "local"); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("removing it again: %v, want ErrNoProvider", err)
	}
}

// raw is the store's file as SQLite alone, for the rows the API does not show: tests only.
func (s *Store) raw(t *testing.T) dao.DataConn {
	t.Helper()
	db, err := sqlite.Open(context.Background(), "file:"+s.path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// one scans the first row q answers into dst.
func one(t *testing.T, db dao.DataConn, q string, dst ...any) error {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), q)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return errors.New("no row")
	}
	return rows.Scan(dst...)
}

// TestAProviderIsCheckedAtTheDoor: a name, a known kind, a model, and an http or https URL.
func TestAProviderIsCheckedAtTheDoor(t *testing.T) {
	s := openAt(t, filepath.Join(t.TempDir(), "autodoc.db"))
	for _, sp := range []ProviderSpec{
		{Kind: KindOllama, BaseURL: "http://x", Model: "m"},
		{Name: "n", Kind: "cohere", BaseURL: "http://x", Model: "m"},
		{Name: "n", Kind: KindOllama, BaseURL: "http://x"},
		{Name: "n", Kind: KindOllama, BaseURL: "localhost:11434", Model: "m"},
	} {
		if _, err := s.AddProvider(context.Background(), sp); !errors.Is(err, ErrProviderInvalid) {
			t.Errorf("%+v: %v, want ErrProviderInvalid", sp, err)
		}
	}
}

// TestAnOllamaCloudProviderNeedsItsKey: added without one it is refused, and nothing is kept;
// with one it is kept; an update removing the key is refused and the key stays.
func TestAnOllamaCloudProviderNeedsItsKey(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "autodoc.db"))
	cloud := ProviderSpec{Name: "cloud", Kind: KindOllamaCloud, BaseURL: "https://ollama.com", Model: "m"}
	if _, err := s.AddProvider(ctx, cloud); !errors.Is(err, ErrProviderNeedsKey) {
		t.Fatalf("a keyless cloud provider: %v, want ErrProviderNeedsKey", err)
	}
	if ps, _ := s.Providers(ctx); len(ps) != 0 {
		t.Fatalf("the refused provider was kept: %+v", ps)
	}
	cloud.Key = strp("ck-1")
	if _, err := s.AddProvider(ctx, cloud); err != nil {
		t.Fatal(err)
	}
	cloud.Key = strp("")
	if err := s.UpdateProvider(ctx, "cloud", cloud); !errors.Is(err, ErrProviderNeedsKey) {
		t.Fatalf("removing a cloud provider's key: %v, want ErrProviderNeedsKey", err)
	}
	if _, key, _ := s.ProviderWithKey(ctx, "cloud"); key != "ck-1" {
		t.Fatalf("the refused update changed the key: %q", key)
	}
	local := ProviderSpec{Name: "local", Kind: KindOllama, BaseURL: "http://localhost:11434", Model: "m"}
	if _, err := s.AddProvider(ctx, local); err != nil {
		t.Fatalf("a local provider needs no key: %v", err)
	}
}

// TestThePreferenceFollowsItsProvider: renaming the provider the preference names renames it in
// the preference, and removing it leaves none named; another provider's rename or remove leaves
// the preference as it was.
func TestThePreferenceFollowsItsProvider(t *testing.T) {
	s := openAt(t, filepath.Join(t.TempDir(), "autodoc.db"))
	ctx := context.Background()
	for _, n := range []string{"a", "b"} {
		if _, err := s.AddProvider(ctx, ProviderSpec{Name: n, Kind: KindOllama, BaseURL: "http://x", Model: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetPreference(ctx, PrefProvider, "a"); err != nil {
		t.Fatal(err)
	}
	named := func() string {
		t.Helper()
		p, err := s.Preferences(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return p[PrefProvider]
	}
	step := func(what string, do func() error, want string) {
		t.Helper()
		if err := do(); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if got := named(); got != want {
			t.Fatalf("after %s the preference names %q, want %q", what, got, want)
		}
	}
	step("renaming b", func() error {
		return s.UpdateProvider(ctx, "b", ProviderSpec{Name: "b2", Kind: KindOllama, BaseURL: "http://x", Model: "m"})
	}, "a")
	step("renaming a", func() error {
		return s.UpdateProvider(ctx, "a", ProviderSpec{Name: "a2", Kind: KindOllama, BaseURL: "http://x", Model: "m"})
	}, "a2")
	step("removing b2", func() error { return s.RemoveProvider(ctx, "b2") }, "a2")
	step("removing a2", func() error { return s.RemoveProvider(ctx, "a2") }, "")
}
