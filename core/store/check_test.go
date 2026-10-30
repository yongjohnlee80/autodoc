package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao/sqlite"
)

// withFiller makes a store at path with a filler table of n rows of 1 KiB, closed again: pages a damage
// can land on.
func withFiller(t *testing.T, path string, n int) {
	t.Helper()
	s := openAt(t, path)
	raw := s.raw(t)
	ctx := context.Background()
	if _, err := raw.ExecContext(ctx, "CREATE TABLE filler (id INTEGER PRIMARY KEY, body BLOB, tag TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "CREATE INDEX filler_tag ON filler(tag)"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i += 500 {
		var b strings.Builder
		b.WriteString("INSERT INTO filler (body, tag) VALUES ")
		for j := i; j < min(i+500, n); j++ {
			if j > i {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, "(randomblob(1024), 'tag-%d')", j%997)
		}
		if _, err := raw.ExecContext(ctx, b.String()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// spoil overwrites page n (1-based, of size 4096) of the file at path with garbage.
func spoil(t *testing.T, path string, n int64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	junk := bytes.Repeat([]byte{0xA5}, 4096)
	if _, err := f.WriteAt(junk, (n-1)*4096); err != nil {
		t.Fatal(err)
	}
}

// TestASoundStoreChecksClean: a store as it is made, and one with rows, checks clean, quick and whole.
func TestASoundStoreChecksClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autodoc.db")
	withFiller(t, path, 300)
	s := openAt(t, path)
	ctx := context.Background()
	if r, err := s.QuickCheck(ctx); err != nil || !r.OK() || !r.Quick {
		t.Fatalf("QuickCheck: %+v, %v", r, err)
	}
	if r, err := s.Check(ctx); err != nil || !r.OK() || r.Quick {
		t.Fatalf("Check: %+v, %v", r, err)
	}
}

// TestADamagedStoreIsRefusedAndReported (AM-4): a store whose pages are damaged is refused at Open
// (ErrDamaged, before a schema script writes), and OpenForCheck still reports it, without writing.
func TestADamagedStoreIsRefusedAndReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autodoc.db")
	withFiller(t, path, 300)
	spoil(t, path, 6)
	_, err := Open(context.Background(), path)
	var d *DamagedError
	if !errors.Is(err, ErrDamaged) || !errors.As(err, &d) || len(d.Report.Integrity) == 0 {
		t.Fatalf("Open of a damaged store: %v; want ErrDamaged with what was found", err)
	}
	before, _ := os.ReadFile(path)
	c, err := OpenForCheck(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Check(context.Background())
	_ = c.Close()
	if err != nil || r.OK() || len(r.Integrity) == 0 {
		t.Fatalf("Check of a damaged store: %+v, %v; want its damage reported", r, err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("checking the store wrote to it")
	}
}

// TestAFileThatIsNoDatabaseIsDamage: a store file that is not a database at all is damage too.
func TestAFileThatIsNoDatabaseIsDamage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autodoc.db")
	junk := make([]byte, 64<<10)
	_, _ = rand.Read(junk)
	if err := os.WriteFile(path, junk, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), path); !errors.Is(err, ErrDamaged) {
		t.Fatalf("Open of a file that is no database: %v, want ErrDamaged", err)
	}
}

// TestALockOrIOErrorIsNotDamage: a store held by another process, or one that cannot be reached,
// is the error it is, never reported as damage.
func TestALockOrIOErrorIsNotDamage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autodoc.db")
	held := openAt(t, path)
	_ = held
	if _, err := Open(context.Background(), path); !errors.Is(err, ErrBusy) || errors.Is(err, ErrDamaged) {
		t.Fatalf("Open of a held store: %v, want ErrBusy and no damage", err)
	}
	if _, err := OpenForCheck(context.Background(), path); !errors.Is(err, ErrBusy) || errors.Is(err, ErrDamaged) {
		t.Fatalf("OpenForCheck of a held store: %v, want ErrBusy and no damage", err)
	}
	gone := filepath.Join(t.TempDir(), "no", "such", "autodoc.db")
	if _, err := Open(context.Background(), gone); err == nil || errors.Is(err, ErrDamaged) {
		t.Fatalf("Open in a folder that is not there: %v, want an error that is no damage", err)
	}
	if _, err := OpenForCheck(context.Background(), gone); err == nil || errors.Is(err, ErrDamaged) {
		t.Fatalf("OpenForCheck of a store that is not there: %v", err)
	}
	if _, err := os.Stat(gone); err == nil {
		t.Fatal("OpenForCheck made a store")
	}
}

// codedErr is an error with SQLite's result code, as the driver's.
type codedErr int

func (e codedErr) Error() string { return "sqlite error " + strconv.Itoa(int(e)) }
func (e codedErr) Code() int     { return int(e) }

// TestOnlySQLitesDamageCodesAreDamage: SQLITE_CORRUPT and SQLITE_NOTADB, extended or not, are damage;
// busy, locked, I/O, full and can't-open are not.
func TestOnlySQLitesDamageCodesAreDamage(t *testing.T) {
	for code, want := range map[int]bool{11: true, 11 | 1<<8: true, 26: true, 5: false, 6: false, 10: false, 10 | 3<<8: false, 13: false, 14: false} {
		if got := isDamage(fmt.Errorf("wrapped: %w", codedErr(code))); got != want {
			t.Errorf("code %d: damage %v, want %v", code, got, want)
		}
	}
	if isDamage(errors.New("no code")) {
		t.Error("an error with no code is damage")
	}
}

// TestAMissingKeyIsReportedNeverMade: a store with sealed secrets whose key file is gone reports each
// secret, and the check does not make a new key (which would hide the loss).
func TestAMissingKeyIsReportedNeverMade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	s := openAt(t, path)
	if _, err := s.AddProvider(ctx, ProviderSpec{Name: "work", Kind: KindOpenAI, BaseURL: "https://api.example", Model: "m", Key: strp("sk-1")}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if err := os.Remove(path + ".key"); err != nil {
		t.Fatal(err)
	}
	c, err := OpenForCheck(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := c.Check(ctx)
	if err != nil || len(r.Secrets) != 1 || !strings.Contains(r.Secrets[0], `embedding provider "work"`) || !strings.Contains(r.Secrets[0], "missing") {
		t.Fatalf("Check with the key gone: %+v, %v", r, err)
	}
	if _, err := os.Stat(path + ".key"); err == nil {
		t.Fatal("the check made a new key")
	}
}

// TestAWrongKeyIsReported: a key file that is not the one the secrets were sealed with leaves each
// secret unopened, and says which.
func TestAWrongKeyIsReported(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	s := openAt(t, path)
	if _, err := s.AddProvider(ctx, ProviderSpec{Name: "work", Kind: KindOpenAI, BaseURL: "https://api.example", Model: "m", Key: strp("sk-1")}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	other := make([]byte, keySize)
	_, _ = rand.Read(other)
	if err := os.WriteFile(path+".key", other, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := OpenForCheck(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if r, err := c.Check(ctx); err != nil || len(r.Secrets) != 1 || !strings.Contains(r.Secrets[0], "does not open") {
		t.Fatalf("Check with another key: %+v, %v", r, err)
	}
}

// TestQuickCheckTimeOnALargeStore measures the start's check on a store of AUTODOC_STORE_CHECK_MB
// megabytes (unset: skipped): the figure the PR reports. Run on VM43 only.
func TestQuickCheckTimeOnALargeStore(t *testing.T) {
	mb, _ := strconv.Atoi(os.Getenv("AUTODOC_STORE_CHECK_MB"))
	if mb <= 0 {
		t.Skip("AUTODOC_STORE_CHECK_MB unset")
	}
	path := filepath.Join(t.TempDir(), "autodoc.db")
	withFiller(t, path, mb*1000)
	fi, _ := os.Stat(path)
	for i := range 3 {
		start := time.Now()
		s, err := Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		opened := time.Since(start)
		start = time.Now()
		if r, err := s.QuickCheck(context.Background()); err != nil || !r.OK() {
			t.Fatalf("QuickCheck: %+v, %v", r, err)
		}
		quick := time.Since(start)
		start = time.Now()
		if r, err := s.Check(context.Background()); err != nil || !r.OK() {
			t.Fatalf("Check: %+v, %v", r, err)
		}
		t.Logf("run %d, store %d MB: Open (with quick_check) %v, quick_check alone %v, the whole Check %v", i, fi.Size()>>20, opened, quick, time.Since(start))
		_ = s.Close()
	}
}

// TestARowWhoseParentIsGoneIsReported: a row left behind with foreign keys off (its workspace gone)
// is what foreign_key_check reports, naming its table and the parent's.
func TestARowWhoseParentIsGoneIsReported(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	_ = openAt(t, path).Close()
	raw, err := sqlite.Open(ctx, "file:"+path+"?_pragma=foreign_keys(0)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "INSERT INTO workspace_pattern (workspace_id, kind, ord, pattern) VALUES (999, 'include', 0, '*.md')"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	c, err := OpenForCheck(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := c.Check(ctx)
	if err != nil || len(r.ForeignKeys) != 1 || !strings.Contains(r.ForeignKeys[0], "workspace_pattern") || !strings.Contains(r.ForeignKeys[0], "workspace row") {
		t.Fatalf("Check with an orphan pattern: %+v, %v", r, err)
	}
}

// lastPage is the number of the store file's last 4096-byte page: a filler page, far from the
// schema the start reads.
func lastPage(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size() / 4096
}

func noWAL(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path + "-wal"); err == nil {
		t.Fatal("precondition: a clean close left the write-ahead log")
	}
}

// TestTheStartCheckIsBoundedAndFinishedLater (AM-4): a cleanly closed store with no script pending
// whose quick_check does not finish in the start's wait opens unverified; the check run later on a
// reader finds what it would have, and marked damaged, the store is refused at its next start.
func TestTheStartCheckIsBoundedAndFinishedLater(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	withFiller(t, path, 300)
	spoil(t, path, lastPage(t, path))
	noWAL(t, path)
	s, err := Open(ctx, path, WithStartCheckWait(time.Nanosecond))
	if err != nil {
		t.Fatalf("a bounded start of a store damaged far from its schema: %v; want it opened unverified", err)
	}
	if !s.Unverified() {
		t.Fatal("the start's check finished in a nanosecond")
	}
	r, err := s.QuickCheck(ctx)
	if err != nil || r.OK() {
		t.Fatalf("the background check: %+v, %v; want the damage found", r, err)
	}
	if err := s.MarkDamaged(r); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(ctx, path, WithStartCheckWait(time.Nanosecond)); !errors.Is(err, ErrDamaged) {
		t.Fatalf("the next start of a store marked damaged: %v; want it checked whole and refused", err)
	}
}

// TestTheStartCheckRunsWholeAfterACrash (AM-4): a store whose write-ahead log is there before it is
// opened (the sign of a crash) is checked whole, whatever the wait.
func TestTheStartCheckRunsWholeAfterACrash(t *testing.T) {
	ctx := context.Background()
	live := filepath.Join(t.TempDir(), "autodoc.db")
	s := openAt(t, live)
	if _, err := s.AddWorkspace(ctx, "kb", "/kb", nil, nil); err != nil {
		t.Fatal(err)
	}
	crashed := filepath.Join(t.TempDir(), "autodoc.db")
	for _, suffix := range []string{"", "-wal"} {
		b, err := os.ReadFile(live + suffix)
		if err != nil {
			t.Fatalf("the live store's %q: %v", suffix, err)
		}
		if err := os.WriteFile(crashed+suffix, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Open(ctx, crashed, WithStartCheckWait(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Unverified() {
		t.Fatal("a store left by a crash was not checked whole at its start")
	}
}

// TestTheStartCheckRunsWholeWithScriptsPending (AM-4): a store with schema scripts to apply (here a
// new one: all of them) is checked whole before they write, whatever the wait.
func TestTheStartCheckRunsWholeWithScriptsPending(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "autodoc.db"), WithStartCheckWait(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Unverified() {
		t.Fatal("a store with scripts pending was not checked whole before they wrote")
	}
}

// TestAMarkedStoreIsRefusedUntilAWholeCheckClearsIt (AM-4): a store carrying the damage mark is
// checked inline at its start and refused, even when that quick check finds nothing (a restored
// store): only a whole check that finds it sound clears the mark, and then it opens.
func TestAMarkedStoreIsRefusedUntilAWholeCheckClearsIt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	withFiller(t, path, 50)
	if err := os.WriteFile(path+damagedSuffix, []byte(`{"quick":true,"integrity":["page 9: bad"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(ctx, path, WithStartCheckWait(time.Nanosecond))
	var d *DamagedError
	if !errors.As(err, &d) || !strings.Contains(err.Error(), "page 9: bad") || !strings.Contains(err.Error(), "--check-store") {
		t.Fatalf("the start of a marked store: %v; want it refused with the mark's report and the way out", err)
	}
	if _, err := os.Stat(path + damagedSuffix); err != nil {
		t.Fatal("a start's check cleared the mark")
	}
	c, err := OpenForCheck(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := c.Check(ctx); err != nil || !r.OK() || !c.Marked() {
		t.Fatalf("the whole check of the restored store: %+v, %v, marked %v", r, err, c.Marked())
	}
	if err := c.ClearDamaged(); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	s, err := Open(ctx, path, WithStartCheckWait(time.Nanosecond))
	if err != nil {
		t.Fatalf("the start once the mark is cleared: %v", err)
	}
	_ = s.Close()
}

// TestTheStartCheckTriggers: the start's check runs whole after a crash, with damage marked, or with
// scripts pending, and bounded otherwise.
func TestTheStartCheckTriggers(t *testing.T) {
	for _, c := range []struct{ unclean, marked, pending, want bool }{
		{false, false, false, false}, {true, false, false, true}, {false, true, false, true}, {false, false, true, true},
	} {
		if got := startCheckInline(c.unclean, c.marked, c.pending); got != c.want {
			t.Errorf("unclean %v marked %v pending %v: inline %v, want %v", c.unclean, c.marked, c.pending, got, c.want)
		}
	}
}
