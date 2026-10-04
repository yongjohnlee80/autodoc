package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/dao"
)

// DATABASES — a workspace's source, the database its .view files run on, and its destination,
// where its index and vectors live (ADR 0214). A workspace is still a directory; these attach to
// it. Each workspace has at most one connection of each role. Its DSN is sealed by the keyslot and
// bound to the workspace and the role, as a provider's API key is: it is never stored as written,
// and only the daemon opens it. A client sees a ConnectionSummary, never the DSN.

// The roles a connection plays, the engines one names, and where an index can live.
const (
	RoleSource      = "source"
	RoleDestination = "destination"

	EnginePostgres = "postgres"
	EngineSQLite   = "sqlite"

	DestinationLocal    = "sqlite"   // the local store, every workspace's index before 000011
	DestinationPostgres = "postgres" // a Postgres database with pgvector

	IndexHNSW    = "hnsw"
	IndexIVFFlat = "ivfflat"
)

// ErrSetting is a workspace setting the store refuses; a SettingError says which and why.
var ErrSetting = errors.New("store: invalid workspace setting")

// SettingError is ErrSetting with its reason, a constant of this package's own: a client may be
// shown it.
type SettingError struct{ Reason string }

func (e *SettingError) Error() string        { return "store: " + e.Reason }
func (e *SettingError) Is(target error) bool { return target == ErrSetting }

// ConnectionInfo is a connection with its DSN opened: for the daemon, which connects with it.
type ConnectionInfo struct {
	Role, Engine, DSN, Schema string
	UpdatedAt                 int64
}

// ConnectionSummary is what a client may see of a connection: where it points, never its secret.
type ConnectionSummary struct {
	Role, Engine, Host, Database, User, Schema string
	HasPassword                                bool
	UpdatedAt                                  int64
}

// newUID is a workspace's identity in a shared destination: 128 random bits in hex, the form
// 000011 gives every workspace that existed before it.
func newUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// dsnAAD binds a sealed DSN to its workspace and role, so a DSN copied to another row opens there
// as nothing.
func dsnAAD(ws int64, role string) string {
	return "autodoc:workspace_connection.dsn:" + strconv.FormatInt(ws, 10) + ":" + role
}

func validRole(role string) bool { return role == RoleSource || role == RoleDestination }

// validEngine reports whether engine may play role: a source may be either engine; a destination
// other than the local store is Postgres.
func validEngine(role, engine string) bool {
	if role == RoleDestination {
		return engine == EnginePostgres
	}
	return engine == EnginePostgres || engine == EngineSQLite
}

// Connection opens workspace id's connection of role; found is false when it has none.
func (s *Store) Connection(ctx context.Context, id int64, role string) (info ConnectionInfo, found bool, err error) {
	err = s.Read(ctx, func(tx *Tx) error {
		c, err := s.Workspace(id).Connections(tx).With(ConnRole, role).Get()
		if errors.Is(err, dao.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		dsn, err := s.keys.open(c.DSN, dsnAAD(id, role))
		if err != nil {
			return err
		}
		found = true
		info = ConnectionInfo{Role: c.Role, Engine: c.Engine, DSN: string(dsn), UpdatedAt: c.UpdatedAt}
		if c.Schema != nil {
			info.Schema = *c.Schema
		}
		return nil
	})
	return info, found, err
}

// Connections summarizes workspace id's connections, by role, for a client.
func (s *Store) Connections(ctx context.Context, id int64) ([]ConnectionSummary, error) {
	var out []ConnectionSummary
	err := s.Read(ctx, func(tx *Tx) error {
		rows, err := s.Workspace(id).Connections(tx).OrderBy(dao.Asc(ByKey)).Select()
		if err != nil {
			return err
		}
		for _, c := range rows {
			dsn, err := s.keys.open(c.DSN, dsnAAD(id, c.Role))
			if err != nil {
				return err
			}
			sum := Summarize(c.Engine, string(dsn))
			sum.Role, sum.UpdatedAt = c.Role, c.UpdatedAt
			if c.Schema != nil {
				sum.Schema = *c.Schema
			}
			out = append(out, sum)
		}
		return nil
	})
	return out, err
}

// Summarize is what a client may see of a DSN. A Postgres URL (postgres://user:pass@host/db) or
// keyword string (host=… dbname=… user=… password=…) gives its host, database and user, and
// whether it carries a password; a SQLite DSN is a file path, shown as the database.
func Summarize(engine, dsn string) ConnectionSummary {
	sum := ConnectionSummary{Engine: engine}
	if engine == EngineSQLite {
		sum.Database = dsn
		return sum
	}
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		sum.Host = u.Host
		sum.Database = strings.TrimPrefix(u.Path, "/")
		if u.User != nil {
			sum.User = u.User.Username()
			_, sum.HasPassword = u.User.Password()
		}
		if u.Query().Get("password") != "" {
			sum.HasPassword = true
		}
		return sum
	}
	for _, kv := range strings.Fields(dsn) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, "'")
		switch k {
		case "host":
			sum.Host = v
		case "port":
			sum.Host += ":" + v
		case "dbname":
			sum.Database = v
		case "user":
			sum.User = v
		case "password":
			sum.HasPassword = v != ""
		}
	}
	return sum
}

// setConnection seals and stores a connection; an empty dsn keeps the one already stored, so a
// client edits a connection's engine or schema without being shown, or sending again, its secret.
func (s *Store) setConnection(tx *Tx, id int64, role string, c ConnectionSpec) error {
	if !validRole(role) || !validEngine(role, c.Engine) {
		return errSetting(role + " engine must be postgres" + map[bool]string{true: "", false: " or sqlite"}[role == RoleDestination])
	}
	var sealed []byte
	if c.DSN == "" {
		old, err := s.Workspace(id).Connections(tx).With(ConnRole, role).Get(ConnDSN)
		if errors.Is(err, dao.ErrNoRows) {
			return errSetting(role + " connection needs a DSN")
		}
		if err != nil {
			return err
		}
		sealed = old.DSN
	} else {
		var err error
		if sealed, err = s.keys.seal([]byte(c.DSN), dsnAAD(id, role)); err != nil {
			return err
		}
	}
	var schema any
	if c.Schema != "" {
		schema = c.Schema
	}
	return s.Workspace(id).Connections(tx).Set(ConnRole, role).Set(ConnEngine, c.Engine).Set(ConnDSN, sealed).
		Set(ConnSchema, schema).Set(ConnUpdatedAt, time.Now().Unix()).Upsert()
}

func errSetting(reason string) error { return &SettingError{Reason: reason} }
