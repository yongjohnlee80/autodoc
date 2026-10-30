package sqlitecrewmail

import (
	"errors"
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
	cGeneration  col = "generation"
	cLocator     col = "locator"
	cRegistered  col = "registered"
	cOwner       col = "owner"
	cText        col = "text"
	cRaised      col = "raised"
	cCleared     col = "cleared"
	cSeq         col = "seq"
	cRef         col = "ref"
	cAt          col = "at"

	byID       sort = "id"
	byCodename sort = "codename"
)

type schema[R any] = dao.Schema[R, col, sort, string]

// crewTables are the crew tables, declared to the store (built for its writer and each reader).
type crewTables struct {
	hosts    *schema[*crew.Host]
	scopes   *schema[*crew.Scope]
	profiles *schema[*crew.Profile]
	sessions *schema[*crew.Session]
	wakes    *schema[*crew.Wake]
	ends     *schema[*crew.Endpoint]
	attn     *schema[*crew.Attention]
	inbox    *dao.Schema[*inboxRow, col, sort, int64] // int64 ids: seq is the table's own
}

// inboxRow is one row of the human feed's journal (inbox_change).
type inboxRow struct {
	Seq  int64
	Kind string
	Ref  string
	At   int64
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

// profileTable is crew_profile, which also lists in (scope, codename) order.
func profileTable(c dao.DataConn, fields map[col]func(*crew.Profile) any) *schema[*crew.Profile] {
	fs := map[col]dao.Field[*crew.Profile]{}
	for n, scan := range fields {
		fs[n] = store.Col("crew_profile", n, scan)
	}
	return dao.New(c,
		dao.Table[*crew.Profile, col, sort, string]("crew_profile"),
		dao.Fields[*crew.Profile, col, sort, string](fs),
		dao.SortMap[*crew.Profile, col, sort, string](map[sort]string{
			byID: `"crew_profile"."id"`, byCodename: `"crew_profile"."scope", "crew_profile"."codename"`}),
		dao.Conflict[*crew.Profile, col, sort, string](cID),
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
		profiles: profileTable(c, map[col]func(*crew.Profile) any{
			cID: func(r *crew.Profile) any { return &r.ID }, cScope: func(r *crew.Profile) any { return &r.Scope },
			cCodename: func(r *crew.Profile) any { return &r.Codename }, cKind: func(r *crew.Profile) any { return &r.Kind },
			cStatus: func(r *crew.Profile) any { return &r.Status }, cMergedInto: func(r *crew.Profile) any { return &r.MergedInto },
			cCreated: func(r *crew.Profile) any { return &r.Created },
		}),
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
		ends: table(c, "crew_endpoint", map[col]func(*crew.Endpoint) any{
			cSession: func(r *crew.Endpoint) any { return &r.Session }, cGeneration: func(r *crew.Endpoint) any { return &r.Generation },
			cKind: func(r *crew.Endpoint) any { return &r.Kind }, cLocator: func(r *crew.Endpoint) any { return &r.Locator },
			cRegistered: func(r *crew.Endpoint) any { return &r.Registered },
		}, cSession),
		attn: table(c, "crew_attention", map[col]func(*crew.Attention) any{
			cSession: func(r *crew.Attention) any { return &r.Session }, cOwner: func(r *crew.Attention) any { return &r.Owner },
			cKind: func(r *crew.Attention) any { return &r.Kind }, cText: func(r *crew.Attention) any { return &r.Text },
			cRaised: func(r *crew.Attention) any { return &r.Raised }, cCleared: func(r *crew.Attention) any { return &r.Cleared },
		}, cSession),
		inbox: dao.New(c,
			dao.Table[*inboxRow, col, sort, int64]("inbox_change"),
			dao.Fields[*inboxRow, col, sort, int64](map[col]dao.Field[*inboxRow]{
				cSeq:  store.Col("inbox_change", cSeq, func(r *inboxRow) any { return &r.Seq }),
				cKind: store.Col("inbox_change", cKind, func(r *inboxRow) any { return &r.Kind }),
				cRef:  store.Col("inbox_change", cRef, func(r *inboxRow) any { return &r.Ref }),
				cAt:   store.Col("inbox_change", cAt, func(r *inboxRow) any { return &r.At }),
			}),
			dao.SortMap[*inboxRow, col, sort, int64](map[sort]string{byID: `"inbox_change"."seq"`}),
			dao.ID[*inboxRow, col, sort, int64](cSeq),
		),
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

func (t *Tx) Scopes(kind string) ([]crew.Scope, error) {
	rows, err := t.t.scopes.On(t.tx).With(cKind, kind).OrderBy(dao.Asc(byID)).Select()
	if err != nil {
		return nil, err
	}
	out := make([]crew.Scope, len(rows))
	for i, r := range rows {
		out[i] = *r
	}
	return out, nil
}

func (t *Tx) ActiveProfiles() ([]crew.Profile, error) {
	rows, err := t.t.profiles.On(t.tx).With(cStatus, crew.ProfileActive).OrderBy(dao.Asc(byCodename)).Select()
	if err != nil {
		return nil, err
	}
	out := make([]crew.Profile, len(rows))
	for i, r := range rows {
		out[i] = *r
	}
	return out, nil
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

// the wake rows a reservation, a timeout and a deadline read (§4.7)
var (
	wakeHasWork = dao.Raw(`(("crew_wake"."state" = ? AND "crew_wake"."pending_gen" > "crew_wake"."announced_gen")`+
		` OR "crew_wake"."state" = ?)`, crew.WakeIdle, crew.WakeAwaiting)
)

func wakes(rows []*crew.Wake, err error) ([]crew.Wake, error) {
	if err != nil {
		return nil, err
	}
	out := make([]crew.Wake, len(rows))
	for i, r := range rows {
		out[i] = *r
	}
	return out, nil
}

// errLimit is a limited read asked for no rows: dao would emit LIMIT 0 and answer nothing.
var errLimit = errors.New("sqlitecrewmail: a limit must be > 0")

// wakeLiveReachable is a wake row whose session is live and has an endpoint.
var wakeLiveReachable = dao.Raw(`EXISTS (SELECT 1 FROM "crew_session" WHERE "crew_session"."id" = "crew_wake"."session"` +
	` AND "crew_session"."ended" IS NULL) AND EXISTS (SELECT 1 FROM "crew_endpoint" WHERE "crew_endpoint"."session" = "crew_wake"."session")`)

func (t *Tx) WakesDue(now int64, limit int) ([]crew.Wake, error) {
	if limit <= 0 {
		return nil, errLimit
	}
	return wakes(t.t.wakes.On(t.tx).With(cState, crew.WakeIdle).WithPredicate(wakeLiveReachable).
		WithPredicate(dao.Raw(`"crew_wake"."pending_gen" > "crew_wake"."announced_gen"`)).
		WithPredicate(dao.Raw(`("crew_wake"."next_at" IS NULL OR "crew_wake"."next_at" <= ?)`, now)).
		OrderBy(dao.Asc(byID)).Limit(uint64(limit)).Select())
}

func (t *Tx) WakesTimedOut(now int64, limit int) ([]crew.Wake, error) {
	if limit <= 0 {
		return nil, errLimit
	}
	return wakes(t.t.wakes.On(t.tx).With(cState, crew.WakeAwaiting).
		WithPredicate(dao.Lte(`"crew_wake"."next_at"`, now)).OrderBy(dao.Asc(byID)).Limit(uint64(limit)).Select())
}

func (t *Tx) WakesInFlight() ([]crew.Wake, error) {
	return wakes(t.t.wakes.On(t.tx).With(cState, crew.WakeInFlight).OrderBy(dao.Asc(byID)).Select())
}

// NextWakeAt reads the rows with work and a deadline, and takes the earliest: at most one per live
// session, so the scan is the crew's size.
func (t *Tx) NextWakeAt() (int64, bool, error) {
	ws, err := wakes(t.t.wakes.On(t.tx).WithPredicate(wakeHasWork).
		WithPredicate(dao.Raw(`"crew_wake"."next_at" IS NOT NULL`)).Select())
	if err != nil {
		return 0, false, err
	}
	var at int64
	has := false
	for _, w := range ws {
		if !has || *w.NextAt < at {
			at, has = *w.NextAt, true
		}
	}
	return at, has, nil
}

func (t *Tx) Endpoint(session string) (crew.Endpoint, bool, error) {
	r, ok, err := got(t.t.ends.On(t.tx).With(cSession, session).Get())
	if !ok {
		return crew.Endpoint{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) PutEndpoint(e crew.Endpoint) error {
	return t.t.ends.On(t.tx).SetMap(map[col]any{cSession: e.Session, cGeneration: e.Generation, cKind: e.Kind,
		cLocator: e.Locator, cRegistered: e.Registered}).Upsert()
}

func (t *Tx) DeleteEndpoint(session string) error {
	return t.t.ends.On(t.tx).With(cSession, session).Delete()
}

func (t *Tx) Attention(session string) (crew.Attention, bool, error) {
	r, ok, err := got(t.t.attn.On(t.tx).With(cSession, session).Get())
	if !ok {
		return crew.Attention{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) PutAttention(a crew.Attention) error {
	return t.t.attn.On(t.tx).SetMap(map[col]any{cSession: a.Session, cOwner: a.Owner, cKind: a.Kind, cText: a.Text,
		cRaised: a.Raised, cCleared: a.Cleared}).Upsert()
}

// AddInboxChange journals one change of the human feed; its seq is the table's own.
func (t *Tx) AddInboxChange(kind, ref string) error {
	_, err := t.t.inbox.On(t.tx).SetMap(map[col]any{cKind: kind, cRef: ref, cAt: t.now.UnixNano()}).Insert()
	return err
}
