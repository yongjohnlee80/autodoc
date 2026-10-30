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
