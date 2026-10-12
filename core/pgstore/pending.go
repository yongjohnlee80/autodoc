package pgstore

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/golib/dao"
)

// Text is one unembedded chunk text: the hash a vector is stored under, and the text to make it
// from — the chunk's Embed when it has one, else Breadcrumb + "\n" + Body, as the chunk's identity
// hash is defined over.
type Text struct {
	TextHash [32]byte
	Text     string
}

// PendingKey is a chunk's place in Pending's order: its document's id and its ordinal, the chunk
// table's own key, so a scan resumed from one reads on through that key's index from where it
// stopped. The zero key is the start.
type PendingKey struct {
	Doc int64
	Ord int
}

// IsZero reports whether k is the start.
func (k PendingKey) IsZero() bool { return k == PendingKey{} }

// PendingQuery is what Pending looks for.
type PendingQuery struct {
	// Model is the model the texts have no vector under.
	Model string
	// After is where the scan resumes: the key a previous call returned, or the zero key.
	After PendingKey
	// Unready reads only the chunks of documents not ready for semantic search, through an index
	// that holds those documents alone, so a tenant whose documents are almost all ready costs only
	// what is not. It is right for the tenant's active model: a document is ready under it only
	// when every one of its chunks has a vector, so every pending text is an unready document's.
	// It is wrong for a target model a switch fills, whose texts ready documents hold too.
	Unready bool
	// Skip are hashes not to list: the texts a caller has set aside.
	Skip map[[32]byte]bool
	// Limit is the most texts listed.
	Limit int
}

// pendingPage is how many chunks one statement of Pending reads, and pendingDocs how many unready
// documents (variables, so a test can take smaller steps).
var (
	pendingPage = 256
	pendingDocs = 64
)

// pendingHook observes every statement Pending runs. It is nil in production; a test sets it to
// read what each statement read, through its plan.
var pendingHook dao.Hook

// opts are the query options of Pending's statements: its context, and pendingHook when set.
func pendingOpts(ctx context.Context) []dao.QueryOption {
	if pendingHook != nil {
		return []dao.QueryOption{dao.WithQueryContext(ctx), dao.WithHooks(pendingHook)}
	}
	return []dao.QueryOption{dao.WithQueryContext(ctx)}
}

// Pending lists up to q.Limit distinct text hashes of the tenant's chunks after q.After with no
// vector under q.Model, except the ones q.Skip names, each with its embed text, in (doc, ord)
// order: an embedding worker's input. next is the key to resume from, the last chunk the scan
// read; the zero key when the scan reached the end, and a caller that began part way looks again
// from the start for what changed behind it.
//
// The vector's absence is a NOT EXISTS on the embedding table's key (tenant, model, text_hash),
// decided in PostgreSQL, so neither the tenant's vectors nor its embedded chunks are read into
// Go; and the scan resumes at After through the chunk table's key, so a caller paging with next
// reads each chunk about once however many calls the tenant takes. A hash repeated within the
// call is listed once; one listed by an earlier call has its vector by the next, or is the
// caller's to skip.
func (s *Store) Pending(ctx context.Context, q PendingQuery) (texts []Text, next PendingKey, err error) {
	if q.Limit <= 0 {
		return nil, q.After, nil
	}
	p := &pendingScan{s: s, q: q, cur: q.After, seen: map[[32]byte]bool{}}
	if q.Unready {
		err = p.unready(ctx)
	} else {
		err = p.all(ctx)
	}
	if err != nil {
		return nil, q.After, fmt.Errorf("pgstore: pending: %w", err)
	}
	if !p.full {
		return p.out, PendingKey{}, nil
	}
	return p.out, p.cur, nil
}

// pendingScan is one call of Pending as it reads.
type pendingScan struct {
	s    *Store
	q    PendingQuery
	cur  PendingKey // the last chunk read
	out  []Text
	seen map[[32]byte]bool
	full bool // the limit was met: the scan stops at cur
}

// missing is the predicate of a chunk with no vector under model: a probe of the embedding
// table's key (tenant, model, text_hash) for each chunk the scan reads.
//
// The OFFSET 0 keeps it a probe. PostgreSQL turns a plain NOT EXISTS (and one with a LIMIT, which
// it drops) into an anti-join, and when its statistics say the model's vectors are few (a tenant
// whose first fill has just begun, before autovacuum has counted them) it reads every vector of
// the model on each call and filters them, which grows with the tenant. A subquery with an
// OFFSET is never pulled up: it stays a subplan run for each chunk read, one index lookup each,
// whatever the statistics say.
func missing(model string) dao.Predicate {
	return dao.Raw(`NOT EXISTS (SELECT 1 FROM `+tEmb+` WHERE `+qcol(tEmb, string(eTenant))+` = `+qcol(tChunk, string(cTenant))+
		` AND `+qcol(tEmb, string(eModel))+` = ? AND `+qcol(tEmb, string(eHash))+` = `+qcol(tChunk, string(cHash))+` OFFSET 0)`, model)
}

// after is the predicate of a chunk after k in (doc, ord) order: a row comparison on the chunk
// key's columns, which the key's index answers as a range.
func after(k PendingKey) dao.Predicate {
	return dao.Raw(`(`+qcol(tChunk, string(cDoc))+`, `+qcol(tChunk, string(cOrd))+`) > (?, ?)`, k.Doc, k.Ord)
}

// notReady is the predicate of a document not ready, written as the partial indexes on
// rag_document are (000003), so the planner uses them whatever a statement's parameters.
func notReady() dao.Predicate { return dao.Raw("NOT " + qcol(tDoc, string(dReady))) }

// all scans every chunk of the tenant from cur, a page at a time.
func (p *pendingScan) all(ctx context.Context) error {
	for {
		d := p.s.rw.chunk.DAO(pendingOpts(ctx)...).With(cTenant, p.s.tenant).WithPredicate(missing(p.q.Model))
		if !p.cur.IsZero() {
			d = d.WithPredicate(after(p.cur))
		}
		rows, err := d.OrderBy(dao.Asc(cByDoc), dao.Asc(cByOrd)).Limit(uint64(pendingPage)).Select(cDoc, cOrd, cHash, cEmb, cCrumb, cBody)
		if err != nil {
			return err
		}
		if p.take(rows) || len(rows) < pendingPage {
			return nil
		}
	}
}

// unready scans the chunks of the tenant's unready documents from cur: a page of the documents in
// id order, through the partial index of unready ones, then their pending chunks.
func (p *pendingScan) unready(ctx context.Context) error {
	first := true
	for {
		d := p.s.rw.doc.DAO(pendingOpts(ctx)...).With(dTenant, p.s.tenant).WithPredicate(notReady())
		switch {
		case first && !p.cur.IsZero():
			d = d.WithPredicate(dao.Gte(string(dID), p.cur.Doc)) // the document the scan stopped in
		case !first:
			d = d.WithPredicate(dao.Gt(string(dID), p.cur.Doc))
		}
		first = false
		docs, err := d.OrderBy(dao.Asc(dByID)).Limit(uint64(pendingDocs)).Select(dID)
		if err != nil {
			return err
		}
		if len(docs) == 0 {
			return nil
		}
		ids := make([]any, len(docs))
		for i, r := range docs {
			ids[i] = r.ID
		}
		c := p.s.rw.chunk.DAO(pendingOpts(ctx)...).With(cTenant, p.s.tenant).
			WithPredicate(dao.In(qcol(tChunk, string(cDoc)), ids)).WithPredicate(missing(p.q.Model))
		if !p.cur.IsZero() {
			c = c.WithPredicate(after(p.cur))
		}
		rows, err := c.OrderBy(dao.Asc(cByDoc), dao.Asc(cByOrd)).Select(cDoc, cOrd, cHash, cEmb, cCrumb, cBody)
		if err != nil {
			return err
		}
		if p.take(rows) {
			return nil
		}
		// every pending chunk of these documents is read: the next page begins after the last
		p.cur = PendingKey{Doc: docs[len(docs)-1].ID, Ord: maxOrd}
		if len(docs) < pendingDocs {
			return nil
		}
	}
}

// maxOrd is past any chunk's ordinal (an integer column).
const maxOrd = 1<<31 - 1

// take adds rows' texts to the scan, moving cur past each row, and reports whether the limit is
// met: cur then stays on the last text taken, and the rest are the next call's.
func (p *pendingScan) take(rows []*chunkRow) bool {
	for _, r := range rows {
		p.cur = PendingKey{Doc: r.Doc, Ord: r.Ord}
		var h [32]byte
		copy(h[:], r.TextHash)
		if p.seen[h] || p.q.Skip[h] {
			continue
		}
		p.seen[h] = true
		p.out = append(p.out, Text{TextHash: h, Text: r.EmbedText()})
		if len(p.out) == p.q.Limit {
			p.full = true
			return true
		}
	}
	return false
}
