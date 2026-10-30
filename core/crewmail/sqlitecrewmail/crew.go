package sqlitecrewmail

import (
	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// col names a column of a crew, mail or inbox table; sort names an order.
type (
	col  string
	sort string
)

const (
	cID          col = "id"
	cEpoch       col = "epoch"
	cIncarnation col = "incarnation"
	cProc        col = "proc"
	cRenewed     col = "renewed"
	cKind        col = "kind"
	cState       col = "state"
	cCreated     col = "created"
	cScope       col = "scope"
	cCodename    col = "codename"
	cStatus      col = "status"
	cMergedInto  col = "merged_into"
	cProfile     col = "profile"
	cHost        col = "host"
	cInstance    col = "instance"
	cSecretHash  col = "secret_hash"
	cArgsDigest  col = "args_digest"
	cStarted     col = "started"
	cEnded       col = "ended"
	cEndedWhy    col = "ended_why"
	cSession     col = "session"
	cPendingGen  col = "pending_gen"
	cAnnounced   col = "announced_gen"
	cToken       col = "token"
	cAttemptGen  col = "attempt_gen"
	cFailures    col = "failures"
	cDaemonRun   col = "daemon_run"
	cNextAt      col = "next_at"

	byID sort = "id"
)

type schema[R any] = dao.Schema[R, col, sort, string]

// crewTables are the crew tables, declared to the store (built for its writer and each reader).
type crewTables struct {
	hosts    *schema[*crew.Host]
	scopes   *schema[*crew.Scope]
	profiles *schema[*crew.Profile]
	sessions *schema[*crew.Session]
	wakes    *schema[*crew.Wake]
}

var tables = store.Declare("crewmail", newCrewTables)

// table declares one table: its columns, the key an upsert conflicts on, and its id order.
func table[R any](c dao.DataConn, name string, fields map[col]func(R) any, key ...col) *schema[R] {
	fs := map[col]dao.Field[R]{}
	for n, scan := range fields {
		fs[n] = store.Col(name, n, scan)
	}
	return dao.New(c,
		dao.Table[R, col, sort, string](name),
		dao.Fields[R, col, sort, string](fs),
		dao.SortMap[R, col, sort, string](map[sort]string{byID: `"` + name + `"."` + string(key[0]) + `"`}),
		dao.Conflict[R, col, sort, string](key...),
	)
}

func newCrewTables(c dao.DataConn) *crewTables {
	return &crewTables{
		hosts: table(c, "crew_host", map[col]func(*crew.Host) any{
			cID: func(r *crew.Host) any { return &r.ID }, cEpoch: func(r *crew.Host) any { return &r.Epoch },
			cIncarnation: func(r *crew.Host) any { return &r.Incarnation }, cProc: func(r *crew.Host) any { return &r.Proc },
			cRenewed: func(r *crew.Host) any { return &r.Renewed },
		}, cID),
		scopes: table(c, "crew_scope", map[col]func(*crew.Scope) any{
			cID: func(r *crew.Scope) any { return &r.ID }, cKind: func(r *crew.Scope) any { return &r.Kind },
			cState: func(r *crew.Scope) any { return &r.State }, cCreated: func(r *crew.Scope) any { return &r.Created },
		}, cID),
		profiles: table(c, "crew_profile", map[col]func(*crew.Profile) any{
			cID: func(r *crew.Profile) any { return &r.ID }, cScope: func(r *crew.Profile) any { return &r.Scope },
			cCodename: func(r *crew.Profile) any { return &r.Codename }, cKind: func(r *crew.Profile) any { return &r.Kind },
			cStatus: func(r *crew.Profile) any { return &r.Status }, cMergedInto: func(r *crew.Profile) any { return &r.MergedInto },
			cCreated: func(r *crew.Profile) any { return &r.Created },
		}, cID),
		sessions: table(c, "crew_session", map[col]func(*crew.Session) any{
			cID: func(r *crew.Session) any { return &r.ID }, cProfile: func(r *crew.Session) any { return &r.Profile },
			cHost: func(r *crew.Session) any { return &r.Host }, cEpoch: func(r *crew.Session) any { return &r.Epoch },
			cInstance: func(r *crew.Session) any { return &r.Instance }, cSecretHash: func(r *crew.Session) any { return &r.SecretHash },
			cArgsDigest: func(r *crew.Session) any { return &r.ArgsDigest }, cStarted: func(r *crew.Session) any { return &r.Started },
			cEnded: func(r *crew.Session) any { return &r.Ended }, cEndedWhy: func(r *crew.Session) any { return &r.EndedWhy },
		}, cID),
		wakes: table(c, "crew_wake", map[col]func(*crew.Wake) any{
			cSession: func(r *crew.Wake) any { return &r.Session }, cPendingGen: func(r *crew.Wake) any { return &r.PendingGen },
			cAnnounced: func(r *crew.Wake) any { return &r.AnnouncedGen }, cState: func(r *crew.Wake) any { return &r.State },
			cToken: func(r *crew.Wake) any { return &r.Token }, cAttemptGen: func(r *crew.Wake) any { return &r.AttemptGen },
			cFailures: func(r *crew.Wake) any { return &r.Failures }, cDaemonRun: func(r *crew.Wake) any { return &r.DaemonRun },
			cNextAt: func(r *crew.Wake) any { return &r.NextAt },
		}, cSession),
	}
}

// the session table's live rows: ended IS NULL (crew_session_live covers it)
var live = dao.IsNull(`"crew_session"."ended"`)

func (t *Tx) Host(id string) (crew.Host, bool, error) {
	r, ok, err := got(t.t.hosts.On(t.tx).With(cID, id).Get())
	if !ok {
		return crew.Host{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) PutHost(h crew.Host) error {
	return t.t.hosts.On(t.tx).SetMap(map[col]any{cID: h.ID, cEpoch: h.Epoch, cIncarnation: blob(h.Incarnation),
		cProc: h.Proc, cRenewed: h.Renewed}).Upsert()
}

func (t *Tx) Scope(id string) (crew.Scope, bool, error) {
	r, ok, err := got(t.t.scopes.On(t.tx).With(cID, id).Get())
	if !ok {
		return crew.Scope{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) PutScope(s crew.Scope) error {
	return t.t.scopes.On(t.tx).SetMap(map[col]any{cID: s.ID, cKind: s.Kind, cState: s.State, cCreated: s.Created}).Upsert()
}

func (t *Tx) Profile(id string) (crew.Profile, bool, error) {
	r, ok, err := got(t.t.profiles.On(t.tx).With(cID, id).Get())
	if !ok {
		return crew.Profile{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) ActiveProfile(scope, codename string) (crew.Profile, bool, error) {
	r, ok, err := one(t.t.profiles.On(t.tx).With(cScope, scope).With(cCodename, codename).
		With(cStatus, crew.ProfileActive).Select())
	if !ok {
		return crew.Profile{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) PutProfile(p crew.Profile) error {
	return t.t.profiles.On(t.tx).SetMap(map[col]any{cID: p.ID, cScope: p.Scope, cCodename: p.Codename,
		cKind: p.Kind, cStatus: p.Status, cMergedInto: p.MergedInto, cCreated: p.Created}).Upsert()
}

func (t *Tx) Session(id string) (crew.Session, bool, error) {
	r, ok, err := got(t.t.sessions.On(t.tx).With(cID, id).Get())
	if !ok {
		return crew.Session{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) SessionByInstance(host string, epoch int64, instance string) (crew.Session, bool, error) {
	r, ok, err := one(t.t.sessions.On(t.tx).With(cHost, host).With(cEpoch, epoch).With(cInstance, instance).Select())
	if !ok {
		return crew.Session{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) LiveSession(profile string) (crew.Session, bool, error) {
	r, ok, err := one(t.t.sessions.On(t.tx).With(cProfile, profile).WithPredicate(live).Select())
	if !ok {
		return crew.Session{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) LiveSessionsOfScope(scope string) ([]crew.Session, error) {
	rows, err := t.t.sessions.On(t.tx).With(cHost, scope).WithPredicate(live).OrderBy(dao.Asc(byID)).Select()
	if err != nil {
		return nil, err
	}
	out := make([]crew.Session, len(rows))
	for i, r := range rows {
		out[i] = *r
	}
	return out, nil
}

func (t *Tx) PutSession(s crew.Session) error {
	return t.t.sessions.On(t.tx).SetMap(map[col]any{cID: s.ID, cProfile: s.Profile, cHost: s.Host, cEpoch: s.Epoch,
		cInstance: s.Instance, cSecretHash: blob(s.SecretHash), cArgsDigest: blob(s.ArgsDigest), cStarted: s.Started,
		cEnded: s.Ended, cEndedWhy: s.EndedWhy}).Upsert()
}

func (t *Tx) Wake(session string) (crew.Wake, bool, error) {
	r, ok, err := got(t.t.wakes.On(t.tx).With(cSession, session).Get())
	if !ok {
		return crew.Wake{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) PutWake(w crew.Wake) error {
	return t.t.wakes.On(t.tx).SetMap(map[col]any{cSession: w.Session, cPendingGen: w.PendingGen,
		cAnnounced: w.AnnouncedGen, cState: w.State, cToken: w.Token, cAttemptGen: w.AttemptGen,
		cFailures: w.Failures, cDaemonRun: w.DaemonRun, cNextAt: w.NextAt}).Upsert()
}
