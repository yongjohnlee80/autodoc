package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

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

// QuickCheck is SQLite's quick_check of the store's file (what Open runs).
func (s *Store) QuickCheck(ctx context.Context) (CheckReport, error) {
	found, err := checkPragma(ctx, s.r, "quick_check")
	return CheckReport{Quick: true, Integrity: found}, err
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
	if ok, err := s.keys.present(); err != nil {
		return nil, err
	} else if !ok {
		for _, x := range all {
			found = append(found, fmt.Sprintf("%s: the store's key %s is missing, so it cannot be opened", x.what, s.keys.path))
		}
		return found, nil
	}
	for _, x := range all {
		if _, err := s.keys.open(x.secret, x.aad); err != nil {
			if !errors.Is(err, ErrSealed) {
				return nil, err // the key itself is unreadable (exposed, the wrong size): said once, as the error
			}
			found = append(found, fmt.Sprintf("%s: %s", x.what, strings.TrimPrefix(err.Error(), "store: ")))
		}
	}
	return found, nil
}

// present reports whether the key file is there, without making one.
func (k *keyslot) present() (bool, error) {
	_, err := os.Stat(k.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}
