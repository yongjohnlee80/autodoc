package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
)

// THE STORE CHECK (AM-4): whether the store's file is sound, and whether its key still opens what
// it sealed. QuickCheck is SQLite's quick_check: every page and record well formed, but not that
// each index matches its table, so it costs a read of the file and no more, and the daemon runs it
// at every start. Check is the whole of it, for when the user asks (autodoc --check-store, the
// sys.check verb): integrity_check, which matches every index to its table too; foreign_key_check;
// and every sealed secret opened with the store's key. A check never writes, and never makes the
// key: a store whose key is gone says so, where opening a secret would have made a new key and
// hidden the loss.

// CheckReport is what a check found; a sound store has nothing in it.
type CheckReport struct {
	Quick       bool     `json:"quick"`                  // quick_check alone (the start's check)
	Integrity   []string `json:"integrity,omitempty"`    // what quick_check or integrity_check found
	ForeignKeys []string `json:"foreign_keys,omitempty"` // each row whose parent row is gone
	Secrets     []string `json:"secrets,omitempty"`      // each sealed secret the store's key does not open
}

// OK reports whether the check found nothing.
func (r CheckReport) OK() bool { return len(r.Integrity)+len(r.ForeignKeys)+len(r.Secrets) == 0 }

// Problems is every problem the check found, in one list.
func (r CheckReport) Problems() []string {
	return append(append(append([]string(nil), r.Integrity...), r.ForeignKeys...), r.Secrets...)
}

// DamagedError is a store whose file failed its check: Open refuses it, and says what was found.
type DamagedError struct {
	Path   string
	Report CheckReport
}

func (e *DamagedError) Error() string {
	found := e.Report.Problems()
	more := ""
	if len(found) > 3 {
		found, more = found[:3], fmt.Sprintf(" (and %d more)", len(e.Report.Problems())-3)
	}
	return fmt.Sprintf("store: %s is damaged: %s%s", e.Path, strings.Join(found, "; "), more)
}

// SQLite's primary result codes that say the file itself is damaged (sqlite.org/rescode.html).
const (
	sqliteCorrupt = 11 // SQLITE_CORRUPT
	sqliteNotADB  = 26 // SQLITE_NOTADB
)

// isDamage reports whether err is SQLite saying the store's file is damaged (corrupt, or not a
// database), as against a busy, locked or I/O error, which a check must never report as damage.
func isDamage(err error) bool {
	var coded interface{ Code() int }
	if !errors.As(err, &coded) {
		return false
	}
	switch coded.Code() & 0xff { // the primary code of an extended one
	case sqliteCorrupt, sqliteNotADB:
		return true
	}
	return false
}

// damaged is the DamagedError for err, when SQLite says the file is damaged; else nil.
func damaged(path string, err error) *DamagedError {
	if !isDamage(err) {
		return nil
	}
	return &DamagedError{Path: path, Report: CheckReport{Quick: true, Integrity: []string{strings.TrimPrefix(err.Error(), "store: ")}}}
}

// Unwrap is ErrDamaged.
func (e *DamagedError) Unwrap() error { return ErrDamaged }

// ErrDamaged is a store whose file failed its check.
var ErrDamaged = errs.Sentinel(errs.ErrPrecondition, "store: the store is damaged")

// QuickCheck is SQLite's quick_check of the store's file, on a reader: what Open runs, and what the
// daemon finishes in the background for a store that opened Unverified. SQLite saying the file is
// damaged is in the report, as what was found.
func (s *Store) QuickCheck(ctx context.Context) (CheckReport, error) {
	found, err := checkPragma(ctx, s.r, "quick_check")
	if d := damaged(s.path, err); d != nil {
		return d.Report, nil
	}
	return CheckReport{Quick: true, Integrity: found}, err
}

// THE START'S CHECK, BOUNDED (AM-4). quick_check reads the whole file, about 0.9 s a GB warm, so the
// start runs it for StartCheckWait at most and the daemon finishes it on a reader in the background
// (Unverified). It runs inline and unbounded where a write could land on a damaged store before
// a background check finds the damage:
//   - schema scripts are pending: their writes never run before the check passes;
//   - the write-ahead log is there before SQLite opens the store: a clean close checkpoints and
//     removes it, so it is the sign of a crash (a CLI reader holding the store open, sqlite3 say,
//     makes it a false positive, which costs only the whole check);
//   - the store is marked damaged (<store>.damaged, MarkDamaged): a background check found damage.
//     Every start refuses a marked store, with what its own check found or else the mark's report;
//     only a whole check that finds its file sound (autodoc --check-store, after a restore) clears
//     the mark. The mark is for the file: secrets the key does not open never keep it.

// StartCheckWait is how long the start's quick_check may take before the rest is left to the daemon.
const StartCheckWait = time.Second

// damagedSuffix names the mark beside the store that a background check found it damaged.
const damagedSuffix = ".damaged"

// startCheckInline reports whether the start's check runs whole, unbounded: after a crash (unclean),
// with damage marked, or with schema scripts pending.
func startCheckInline(unclean, marked, pending bool) bool { return unclean || marked || pending }

// startCheck is the start's quick_check on w: a *DamagedError for damage found; unverified when it
// was bounded and did not finish in wait. A busy, locked or I/O error is the error it is.
func startCheck(ctx context.Context, w dao.DataConn, path string, wait time.Duration, inline bool) (unverified bool, err error) {
	checkCtx := ctx
	if !inline {
		var cancel context.CancelFunc
		checkCtx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}
	found, err := checkPragma(checkCtx, w, "quick_check")
	if err != nil && !inline && ctx.Err() == nil && checkCtx.Err() != nil {
		return true, nil // the wait passed: the daemon finishes the check
	}
	if d := damaged(path, err); d != nil {
		return false, d
	}
	if err != nil {
		return false, fmt.Errorf("store: checking %s: %w", path, err)
	}
	if len(found) > 0 {
		return false, &DamagedError{Path: path, Report: CheckReport{Quick: true, Integrity: found}}
	}
	return false, nil
}

// Unverified reports whether the start's quick_check did not finish in its wait: the daemon runs it
// on a reader (QuickCheck), and on damage marks the store (MarkDamaged) and stops.
func (s *Store) Unverified() bool { return s.unverified }

// MarkDamaged writes the mark that a check found the store damaged (<store>.damaged, the report as
// JSON, 0600): every start refuses the store until a whole check finds it sound (ClearDamaged).
func (s *Store) MarkDamaged(r CheckReport) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path+damagedSuffix, b, 0o600)
}

// Marked reports whether the store carries the damage mark.
func (s *Store) Marked() bool {
	_, err := os.Stat(s.path + damagedSuffix)
	return err == nil
}

// ClearDamaged removes the damage mark: for a store whose file a whole check (Check) has just found
// sound (no integrity or foreign-key problem; unopened secrets are not damage to the file), and for
// nothing else. A store with no mark is no error.
func (s *Store) ClearDamaged() error {
	if err := os.Remove(s.path + damagedSuffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// markedDamaged is the start's refusal of a store carrying the damage mark whose own check found
// nothing: the mark's report, and how to clear it.
func markedDamaged(path string) error {
	r := CheckReport{Quick: true}
	if b, err := os.ReadFile(path + damagedSuffix); err == nil {
		_ = json.Unmarshal(b, &r)
	}
	if r.OK() {
		r.Integrity = []string{"a check found the store damaged"}
	}
	r.Integrity = append(r.Integrity, "it is marked damaged ("+path+damagedSuffix+"): autodoc --check-store checks it whole and clears the mark when it is sound")
	return &DamagedError{Path: path, Report: r}
}

// Check is the whole check: integrity_check, foreign_key_check and the sealed secrets.
func (s *Store) Check(ctx context.Context) (CheckReport, error) {
	var r CheckReport
	var err error
	if r.Integrity, err = checkPragma(ctx, s.r, "integrity_check"); err != nil {
		if d := damaged(s.path, err); d != nil {
			return d.Report, nil // too damaged to check further: what SQLite said is the finding
		}
		return r, err
	}
	if r.ForeignKeys, err = s.foreignKeyCheck(ctx); err != nil {
		if d := damaged(s.path, err); d != nil {
			r.ForeignKeys = d.Report.Integrity
			return r, nil
		}
		return r, err
	}
	if r.Secrets, err = s.secretsCheck(ctx); err != nil {
		// the key unreadable, or tables older than this binary (OpenForCheck runs no script): said
		// as what the check could not do, beside what it found
		r.Secrets = []string{"the sealed secrets were not checked: " + strings.TrimPrefix(err.Error(), "store: ")}
	}
	return r, nil
}

// checkPragma runs quick_check or integrity_check on conn: what it found, nothing for "ok".
func checkPragma(ctx context.Context, conn dao.DataConn, name string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA "+name)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", name, err)
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, fmt.Errorf("store: %s: %w", name, err)
		}
		if line != "ok" {
			found = append(found, line)
		}
	}
	return found, rows.Err()
}

// foreignKeyCheck is each row foreign_key_check finds whose parent row is gone.
func (s *Store) foreignKeyCheck(ctx context.Context) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, fmt.Errorf("store: foreign_key_check: %w", err)
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fk int64
		if err := rows.Scan(&table, &rowid, &parent, &fk); err != nil {
			return nil, fmt.Errorf("store: foreign_key_check: %w", err)
		}
		row := "a row without a rowid"
		if rowid.Valid {
			row = fmt.Sprintf("rowid %d", rowid.Int64)
		}
		found = append(found, fmt.Sprintf("%s %s refers to a %s row that is gone", table, row, parent))
	}
	return found, rows.Err()
}

// sealed is one sealed secret: what it is, as a person names it, its bytes and its additional data.
type sealed struct {
	what   string
	secret []byte
	aad    string
}

// secretsCheck is each sealed secret the store's key does not open: every one, when the key file
// is gone (which the check never makes).
func (s *Store) secretsCheck(ctx context.Context) ([]string, error) {
	var all []sealed
	err := s.Read(ctx, func(tx *Tx) error {
		conns, err := tx.t.connections.On(tx.tx).Select()
		if err != nil {
			return err
		}
		for _, c := range conns {
			all = append(all, sealed{fmt.Sprintf("workspace %d's %s connection", c.WorkspaceID, c.Role), c.DSN, dsnAAD(c.WorkspaceID, c.Role)})
		}
		ps, err := s.providers().on(tx).Select()
		if err != nil {
			return err
		}
		for _, p := range ps {
			if len(p.APIKey) > 0 {
				all = append(all, sealed{fmt.Sprintf("embedding provider %q's API key", p.Name), p.APIKey, s.providers().aad(p.ID)})
			}
		}
		rs, err := s.rankers().on(tx).Select()
		if err != nil {
			return err
		}
		for _, r := range rs {
			if len(r.APIKey) > 0 {
				all = append(all, sealed{fmt.Sprintf("ranker %q's API key", r.Name), r.APIKey, s.rankers().aad(r.ID)})
			}
		}
		return nil
	})
	if err != nil || len(all) == 0 {
		return nil, err
	}
	var found []string
	key, problem, err := readKeyForCheck(s.keys.path)
	if err != nil {
		return nil, err
	}
	if problem != "" {
		for _, x := range all {
			found = append(found, fmt.Sprintf("%s: %s, so it cannot be opened", x.what, problem))
		}
		return found, nil
	}
	gcm, err := aeadOf(key)
	if err != nil {
		return nil, err
	}
	for _, x := range all {
		if _, err := openSealed(gcm, x.secret, x.aad); err != nil {
			found = append(found, fmt.Sprintf("%s: %s", x.what, strings.TrimPrefix(err.Error(), "store: ")))
		}
	}
	return found, nil
}

// readKeyForCheck reads the store's key file afresh for a check, never the key the store keeps in
// memory (a file replaced, cut short or exposed since is what a check must see) and never making
// one: a missing, exposed or wrong-sized key is the problem it names, and only a failure to read the
// file at all is an error.
func readKeyForCheck(path string) (key []byte, problem string, err error) {
	fi, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Sprintf("the store's key %s is missing", path), nil
	case err != nil:
		return nil, "", err
	case fi.Mode().Perm()&0o077 != 0:
		return nil, fmt.Sprintf("the store's key %s is readable by others (%v): make it 0600", path, fi.Mode().Perm()), nil
	}
	key, err = os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	if len(key) != keySize {
		return nil, fmt.Sprintf("the store's key %s is %d bytes, not %d", path, len(key), keySize), nil
	}
	return key, "", nil
}
