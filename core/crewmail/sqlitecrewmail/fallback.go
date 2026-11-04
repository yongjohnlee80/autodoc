package sqlitecrewmail

import (
	"cmp"
	"slices"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// fallbackTaker is crew_host's fallbacks column: the epoch in which the host polled for its jobs.
type fallbackTaker struct {
	Host  string
	Epoch int64
}

// fallbackCandidate is a delivery that may fall back, whatever the time
// (docs/decisions/crew.md#the-fallback-exactly-once): queued and never leased, with no job, its
// recipient live and started (startedRecipient), and its scope's host taking fallbacks in its
// current epoch.
var fallbackCandidate = dao.And(
	dao.Eq(`"mail_delivery"."state"`, mail.Queued),
	dao.Eq(`"mail_delivery"."attempts"`, 0),
	dao.Eq(`"mail_delivery"."lease"`, ""),
	store.Correlated(true, "mail_fallback", "delivery", "mail_delivery", "id"),
	liveRecipient(),
	startedRecipient,
	store.Correlated(false, "crew_profile", "id", "mail_delivery", "recipient",
		store.Correlated(false, "crew_host", "id", "crew_profile", "scope",
			dao.Cmp(dao.T("crew_host", "fallbacks"), dao.OpEq, dao.T("crew_host", "epoch")))),
)

// liveRecipient is a delivery whose recipient has a live session meeting conds.
func liveRecipient(conds ...dao.Predicate) dao.Predicate {
	return store.Correlated(false, "crew_session", "profile", "mail_delivery", "recipient",
		append([]dao.Predicate{dao.IsNull(`"crew_session"."ended"`)}, conds...)...)
}

// startedRecipient is a delivery whose recipient's live session is past its CLI's startup: of a
// kind that doesn't start with its endpoint, or with its endpoint registered
// (crew.StartsWithItsEndpoint). A Claude session with none may be showing its folder-trust
// question: its mail waits, queued, for its first receive.
var startedRecipient = dao.Or(
	store.Correlated(false, "crew_profile", "id", "mail_delivery", "recipient", dao.NotIn(`"crew_profile"."kind"`, endpointStartedKinds())),
	liveRecipient(store.Correlated(false, "crew_endpoint", "session", "crew_session", "id")))

func endpointStartedKinds() []any {
	out := make([]any, len(crew.EndpointStartedKinds))
	for i, k := range crew.EndpointStartedKinds {
		out[i] = k
	}
	return out
}

// unreachableRecipient is a delivery whose recipient's live session can't be woken: no endpoint,
// or crew.UnreachableAfter failed wakes.
var unreachableRecipient = liveRecipient(dao.Or(
	store.Correlated(true, "crew_endpoint", "session", "crew_session", "id"),
	store.Correlated(false, "crew_wake", "session", "crew_session", "id",
		dao.Gte(`"crew_wake"."failures"`, crew.UnreachableAfter))))

// sentBy is a delivery whose message was created at or before at.
func sentBy(at int64) dao.Predicate {
	return store.Correlated(false, "mail_message", "id", "mail_delivery", "message", dao.Lte(`"mail_message"."created"`, at))
}

// windowOpenBy is a delivery whose fallback window opened at or before at: its message was created
// then, its recipient's live session had started (a profile has one live session: registering
// again supersedes the old), and, for a kind that starts with its endpoint, that endpoint's first
// generation had registered then. A message that waited across a restart counts from the new
// session, one that waited out a Claude's startup from its first endpoint; another kind's endpoint,
// and a later generation of any, move nothing.
func windowOpenBy(at int64) dao.Predicate {
	return dao.And(sentBy(at), liveRecipient(dao.Lte(`"crew_session"."started"`, at),
		store.Correlated(true, "crew_endpoint", "session", "crew_session", "id",
			dao.In(`"crew_endpoint"."kind"`, endpointStartedKinds()), dao.Gt(`"crew_endpoint"."first_registered"`, at))))
}

func (t *Tx) FallbackDue(unreachableBefore, unansweredBefore int64, limit int) ([]mail.Delivery, error) {
	if limit <= 0 {
		return nil, errLimit
	}
	return deliveries(t.m.deliveries.On(t.tx).WithPredicate(fallbackCandidate).
		WithPredicate(dao.Or(windowOpenBy(unansweredBefore), dao.And(windowOpenBy(unreachableBefore), unreachableRecipient))).
		OrderBy(dao.Asc(bySeq)).Limit(uint64(limit)).Select())
}

// NextFallbackAt is the earliest max(created, opened)+after over the candidates, per live session:
// a delivery's window opens at the later of its message's creation and its recipient's session's
// opening, its start, or its first endpoint's for a kind that starts with it (windowOpenBy), so for
// each live session the earliest-created message of its candidates (and of its unreachable ones)
// decides, read from its deliveries (earliestCreated), never by walking every message. Two reads
// per live session, the crew's size, each as small as its due mail. Never earlier than FallbackDue
// answers, so the timer never wakes to find nothing due and asks again for the same instant.
func (t *Tx) NextFallbackAt(unreachable, unanswered int64) (int64, bool, error) {
	sessions, err := t.t.sessions.On(t.tx).WithPredicate(live).Select()
	if err != nil {
		return 0, false, err
	}
	var at int64
	has := false
	for _, s := range sessions {
		e, _, err := t.Endpoint(s.ID)
		if err != nil {
			return 0, false, err
		}
		opened := s.Started
		if crew.StartsWithItsEndpoint(e.Kind) {
			opened = max(opened, e.First)
		}
		for _, c := range []struct {
			cond  []dao.Predicate
			after int64
		}{{[]dao.Predicate{fallbackCandidate}, unanswered}, {[]dao.Predicate{fallbackCandidate, unreachableRecipient}, unreachable}} {
			cond := append([]dao.Predicate{dao.Eq(`"mail_delivery"."recipient"`, s.Profile)}, c.cond...)
			created, ok, err := t.earliestCreated(cond...)
			if err != nil {
				return 0, false, err
			}
			if !ok {
				continue
			}
			if due := max(created, opened) + c.after; !has || due < at {
				at, has = due, true
			}
		}
	}
	return at, has, nil
}

// earliestMessagesPerRead bounds one read of earliestCreated's messages by id.
const earliestMessagesPerRead = 500

// earliestCreated is the earliest creation of the messages of the deliveries meeting cond (a
// recipient's, first): read from those deliveries, by mail_delivery_open (recipient, state), then
// their messages by id. Never a walk of mail_message in created order checking each message's
// deliveries: the table keeps every message, so for a recipient with none due that walk read all of
// it, once per live session and kind, inside every mail wait's write.
func (t *Tx) earliestCreated(cond ...dao.Predicate) (int64, bool, error) {
	ds, err := deliveries(t.m.deliveries.On(t.tx).WithPredicate(dao.And(cond...)).Select())
	if err != nil || len(ds) == 0 {
		return 0, false, err
	}
	ids := make([]any, 0, len(ds))
	for _, d := range ds {
		ids = append(ids, d.Message)
	}
	var at int64
	has := false
	for part := range slices.Chunk(ids, earliestMessagesPerRead) {
		msgs, err := t.m.messages.On(t.tx).WithPredicate(dao.In(`"mail_message"."id"`, part)).Select()
		if err != nil {
			return 0, false, err
		}
		for _, m := range msgs {
			if !has || m.Created < at {
				at, has = m.Created, true
			}
		}
	}
	return at, has, nil
}

func (t *Tx) TakeFallbacks(host string, epoch int64) error {
	return t.m.takers.On(t.tx).With(cID, host).SetMap(map[col]any{cFallbacks: epoch}).Update()
}

func (t *Tx) TakesFallbacks(host string) (bool, error) {
	_, ok, err := one(t.m.takers.On(t.tx).With(cID, host).
		WithPredicate(dao.Cmp(dao.T("crew_host", "fallbacks"), dao.OpEq, dao.T("crew_host", "epoch"))).Select())
	return ok, err
}

func (t *Tx) FallbackJob(id string) (mail.FallbackJob, bool, error) {
	r, ok, err := got(t.m.jobs.On(t.tx).With(cID, id).Get())
	if !ok {
		return mail.FallbackJob{}, false, err
	}
	return *r, true, nil
}

func (t *Tx) PutFallbackJob(j mail.FallbackJob) error {
	t.wake = true // a host's mail.fallbacks hears the job (crew.Signals.Wake)
	return t.m.jobs.On(t.tx).SetMap(map[col]any{cID: j.ID, cDelivery: j.Delivery, cMessage: j.Message,
		cRecipient: j.Recipient, cHost: j.Host, cState: j.State, cLease: j.Lease, cLeaseUntil: j.LeaseUntil,
		cTries: j.Tries, cCreated: j.Created, cDoneAt: j.DoneAt, cOutcome: string(j.Outcome)}).Upsert()
}

// FallbackOffers is host's open jobs and its leased ones whose lease lapsed, oldest first, at most
// limit. Each state is its own read by mail_fallback_host (host, state, created): asked as one OR of
// the two, SQLite searched by host alone and read every job the host ever had, done ones included.
func (t *Tx) FallbackOffers(host string, now int64, limit int) ([]mail.FallbackJob, error) {
	if limit <= 0 {
		return nil, errLimit
	}
	open, err := jobs(t.m.jobs.On(t.tx).With(cHost, host).With(cState, mail.JobOpen).
		OrderBy(dao.Asc(byCreated)).Limit(uint64(limit)).Select())
	if err != nil {
		return nil, err
	}
	lapsed, err := jobs(t.m.jobs.On(t.tx).With(cHost, host).With(cState, mail.JobLeased).
		WithPredicate(dao.Lte(`"mail_fallback"."lease_until"`, now)).OrderBy(dao.Asc(byCreated)).Limit(uint64(limit)).Select())
	if err != nil {
		return nil, err
	}
	out := append(open, lapsed...)
	slices.SortFunc(out, func(a, b mail.FallbackJob) int {
		return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID))
	})
	return out[:min(len(out), limit)], nil
}

func (t *Tx) NextFallbackLease(host string) (int64, bool, error) {
	j, ok, err := one(t.m.jobs.On(t.tx).With(cHost, host).With(cState, mail.JobLeased).
		WithPredicate(dao.IsNotNull(`"mail_fallback"."lease_until"`)).OrderBy(dao.Asc(byLease)).Limit(1).Select())
	if err != nil || !ok {
		return 0, false, err
	}
	return *j.LeaseUntil, true, nil
}

func jobs(rows []*mail.FallbackJob, err error) ([]mail.FallbackJob, error) {
	if err != nil {
		return nil, err
	}
	out := make([]mail.FallbackJob, len(rows))
	for i, r := range rows {
		out[i] = *r
	}
	return out, nil
}
