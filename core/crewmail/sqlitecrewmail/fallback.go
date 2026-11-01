package sqlitecrewmail

import (
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
// recipient live, and its scope's host taking fallbacks in its current epoch.
var fallbackCandidate = dao.And(
	dao.Eq(`"mail_delivery"."state"`, mail.Queued),
	dao.Eq(`"mail_delivery"."attempts"`, 0),
	dao.Eq(`"mail_delivery"."lease"`, ""),
	store.Correlated(true, "mail_fallback", "delivery", "mail_delivery", "id"),
	liveRecipient(),
	store.Correlated(false, "crew_profile", "id", "mail_delivery", "recipient",
		store.Correlated(false, "crew_host", "id", "crew_profile", "scope",
			dao.Cmp(dao.T("crew_host", "fallbacks"), dao.OpEq, dao.T("crew_host", "epoch")))),
)

// liveRecipient is a delivery whose recipient has a live session meeting conds.
func liveRecipient(conds ...dao.Predicate) dao.Predicate {
	return store.Correlated(false, "crew_session", "profile", "mail_delivery", "recipient",
		append([]dao.Predicate{dao.IsNull(`"crew_session"."ended"`)}, conds...)...)
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

func (t *Tx) FallbackDue(unreachableBefore, unansweredBefore int64, limit int) ([]mail.Delivery, error) {
	if limit <= 0 {
		return nil, errLimit
	}
	return deliveries(t.m.deliveries.On(t.tx).WithPredicate(fallbackCandidate).
		WithPredicate(dao.Or(sentBy(unansweredBefore), dao.And(sentBy(unreachableBefore), unreachableRecipient))).
		OrderBy(dao.Asc(bySeq)).Limit(uint64(limit)).Select())
}

// NextFallbackAt reads the earliest-created message of a candidate, and of an unreachable one:
// one ordered, one-row read each, from mail_message by its own column.
func (t *Tx) NextFallbackAt(unreachable, unanswered int64) (int64, bool, error) {
	var at int64
	has := false
	for _, c := range []struct {
		cond  []dao.Predicate
		after int64
	}{{[]dao.Predicate{fallbackCandidate}, unanswered}, {[]dao.Predicate{fallbackCandidate, unreachableRecipient}, unreachable}} {
		msg, ok, err := one(t.m.messages.On(t.tx).
			WithPredicate(store.Correlated(false, "mail_delivery", "message", "mail_message", "id", c.cond...)).
			OrderBy(dao.Asc(byCreated)).Limit(1).Select())
		if err != nil {
			return 0, false, err
		}
		if ok && (!has || msg.Created+c.after < at) {
			at, has = msg.Created+c.after, true
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

func (t *Tx) FallbackOffers(host string, now int64, limit int) ([]mail.FallbackJob, error) {
	if limit <= 0 {
		return nil, errLimit
	}
	rows, err := t.m.jobs.On(t.tx).With(cHost, host).WithPredicate(dao.Or(dao.Eq(`"mail_fallback"."state"`, mail.JobOpen),
		dao.And(dao.Eq(`"mail_fallback"."state"`, mail.JobLeased), dao.Lte(`"mail_fallback"."lease_until"`, now)))).
		OrderBy(dao.Asc(byCreated)).Limit(uint64(limit)).Select()
	return jobs(rows, err)
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
