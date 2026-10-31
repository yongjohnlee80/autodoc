package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/chunk"
	"github.com/yongjohnlee80/golib/search/embed"
)

// Approximate semantic search: one HNSW expression index per embedding model, typed by the
// model's dimensions, built and retired by the server (annbuild.go), and a two-stage search over
// it, approximate candidates through the index, then their exact order.

// ANNConfig is the approximate search's settings.
type ANNConfig struct {
	// Threshold is how many embeddings of its model a tenant has before its searches go through
	// the model's index; below it they are exact.
	Threshold int
	// MaxScanTuples and ScanMemMultiplier bound one search's HNSW scan (pgvector's
	// hnsw.max_scan_tuples and hnsw.scan_mem_multiplier). A scan that hits them comes back
	// short, and the search answers exactly instead.
	MaxScanTuples     int
	ScanMemMultiplier float64
	// EfFactor sets hnsw.ef_search to EfFactor × k (at least 100, at most pgvector's 1000): the
	// scan's candidate list, wider than k so a tenant sharing the index with others still finds
	// its own nearest. 0 is 1.
	EfFactor int
}

// DefaultANNConfig is the server's defaults: approximate from 50,000 embeddings of a model, a
// scan of at most 1,000 tuples past the first ef_search, twice the memory of work_mem.
//
// The budget is what a filtered search spends before it gives up and answers exactly, and a
// selective filter mostly does give up: at 1,000,000 embeddings under a 1% filter, 40,000 tuples
// cost 7.6× the exact search, 1,000 cost 1.4×, with the unfiltered search at 0.09× either way
// (the measured sweep, TestANN_BudgetSweep). An unfiltered search's first ef_search candidates
// don't count against it.
func DefaultANNConfig() ANNConfig {
	return ANNConfig{Threshold: 50000, MaxScanTuples: 1000, ScanMemMultiplier: 2}
}

// annType is the vector type a model's index casts its embeddings to, from its fingerprint's
// dimensions: vector(d) to 2,000, halfvec(d) to 4,000 (HNSW's limits). reason says why a model
// has none, and its searches stay exact.
func annType(model string) (t dao.VectorType, reason string) {
	m, ok := embed.ParseFingerprint(model)
	switch {
	case !ok:
		return t, "not a model fingerprint"
	case m.Dims < 1:
		return t, "the model reports no dimensions"
	case m.Dims <= 2000:
		return dao.VectorType{Kind: dao.VectorFloat32, Dims: m.Dims}, ""
	case m.Dims <= 4000:
		return dao.VectorType{Kind: dao.VectorFloat16, Dims: m.Dims}, ""
	}
	return t, fmt.Sprintf("the model has %d dimensions; an HNSW index takes 4,000 at most", m.Dims)
}

// annIndexName is the index of model's embeddings: a hash of the fingerprint, since index names
// are bounded and a fingerprint is not an identifier.
func annIndexName(model string) string {
	h := sha256.Sum256([]byte(model))
	return "rag_embedding_hnsw_" + hex.EncodeToString(h[:8])
}

// annSpec is model's index, declared: the embeddings cast to t, cosine, the rows of model only.
func annSpec(model string, t dao.VectorType) (postgres.IndexSpec, error) {
	el, err := postgres.HNSWIndexElement("vec", dao.Cosine, t)
	if err != nil {
		return postgres.IndexSpec{}, err
	}
	return postgres.IndexSpec{Table: tEmb, Name: annIndexName(model), Method: "hnsw", Element: el,
		WhereEq: []postgres.EqText{{Column: "model", Value: model}}}, nil
}

// The states of a model's index (rag_ann_index.state, 000005).
const (
	annWanted   = "wanted"
	annBuilding = "building"
	annReady    = "ready"
	annFailed   = "failed"
	annRetiring = "retiring"
)

// The modes a model's search is in, as the status reports them.
const (
	ModeExact       = "exact"
	ModeANN         = "ann"
	ModeANNBuilding = "ann-building"
	ModeANNFailed   = "ann-failed"
)

// --- the two tables (000005) ----------------------------------------------------------

const (
	tANNIndex   = "rag_ann_index"
	tANNBuilder = "rag_ann_builder"
)

type annIndexRow struct {
	Model, Name, Def, State, Error string
	Dims, Attempts                 int
	NextAt                         *time.Time
}

type annIndexField string

const (
	aiModel    annIndexField = "model"
	aiName     annIndexField = "name"
	aiDims     annIndexField = "dims"
	aiDef      annIndexField = "def"
	aiState    annIndexField = "state"
	aiAttempts annIndexField = "attempts"
	aiNextAt   annIndexField = "next_at"
	aiError    annIndexField = "error"
	aiUpdated  annIndexField = "updated"
)

type annIndexSchema = dao.Schema[*annIndexRow, annIndexField, string, string]

func newANNIndexSchema(conn dao.DataConn) *annIndexSchema {
	type o = dao.Option[*annIndexRow, annIndexField, string, string]
	return dao.New(conn,
		o(dao.Table[*annIndexRow, annIndexField, string, string](tANNIndex)),
		o(dao.ID[*annIndexRow, annIndexField, string, string](aiModel)),
		o(dao.Fields[*annIndexRow, annIndexField, string, string](map[annIndexField]dao.Field[*annIndexRow]{
			aiModel:    {Expr: dao.T(tANNIndex, aiModel), Scan: func(r *annIndexRow) any { return &r.Model }},
			aiName:     {Expr: dao.T(tANNIndex, aiName), Scan: func(r *annIndexRow) any { return &r.Name }},
			aiDims:     {Expr: dao.T(tANNIndex, aiDims), Scan: func(r *annIndexRow) any { return &r.Dims }},
			aiDef:      {Expr: dao.T(tANNIndex, aiDef), Scan: func(r *annIndexRow) any { return &r.Def }},
			aiState:    {Expr: dao.T(tANNIndex, aiState), Scan: func(r *annIndexRow) any { return &r.State }},
			aiAttempts: {Expr: dao.T(tANNIndex, aiAttempts), Scan: func(r *annIndexRow) any { return &r.Attempts }},
			aiNextAt:   {Expr: dao.T(tANNIndex, aiNextAt), Scan: func(r *annIndexRow) any { return &r.NextAt }},
			aiError:    {Expr: dao.T(tANNIndex, aiError), Scan: func(r *annIndexRow) any { return &r.Error }},
			aiUpdated:  {Expr: dao.T(tANNIndex, aiUpdated)},
		})),
		o(dao.Default[*annIndexRow, annIndexField, string, string](aiModel, aiName, aiDims, aiDef, aiState, aiAttempts, aiNextAt, aiError)),
		o(dao.Conflict[*annIndexRow, annIndexField, string, string](aiModel)),
	)
}

type annBuilderRow struct {
	Model        *string
	Owner, Token string
	Generation   int64
	PID          *int32
	Start, Lease *time.Time
}

type annBuilderField string

const (
	abID    annBuilderField = "id"
	abModel annBuilderField = "model"
	abOwner annBuilderField = "owner"
	abGen   annBuilderField = "generation"
	abPID   annBuilderField = "backend_pid"
	abStart annBuilderField = "backend_start"
	abToken annBuilderField = "backend_token"
	abLease annBuilderField = "lease_until"
)

type annBuilderSchema = dao.Schema[*annBuilderRow, annBuilderField, string, int]

func newANNBuilderSchema(conn dao.DataConn) *annBuilderSchema {
	type o = dao.Option[*annBuilderRow, annBuilderField, string, int]
	return dao.New(conn,
		o(dao.Table[*annBuilderRow, annBuilderField, string, int](tANNBuilder)),
		o(dao.ID[*annBuilderRow, annBuilderField, string, int](abID)),
		o(dao.Fields[*annBuilderRow, annBuilderField, string, int](map[annBuilderField]dao.Field[*annBuilderRow]{
			abID:    {Expr: dao.T(tANNBuilder, abID)},
			abModel: {Expr: dao.T(tANNBuilder, abModel), Scan: func(r *annBuilderRow) any { return &r.Model }},
			abOwner: {Expr: dao.T(tANNBuilder, abOwner), Scan: func(r *annBuilderRow) any { return &r.Owner }},
			abGen:   {Expr: dao.T(tANNBuilder, abGen), Scan: func(r *annBuilderRow) any { return &r.Generation }},
			abPID:   {Expr: dao.T(tANNBuilder, abPID), Scan: func(r *annBuilderRow) any { return &r.PID }},
			abStart: {Expr: dao.T(tANNBuilder, abStart), Scan: func(r *annBuilderRow) any { return &r.Start }},
			abToken: {Expr: dao.T(tANNBuilder, abToken), Scan: func(r *annBuilderRow) any { return &r.Token }},
			abLease: {Expr: dao.T(tANNBuilder, abLease), Scan: func(r *annBuilderRow) any { return &r.Lease }},
		})),
		o(dao.Default[*annBuilderRow, annBuilderField, string, int](abModel, abOwner, abGen, abPID, abStart, abToken, abLease)),
	)
}

// --- the server's ANN -------------------------------------------------------------------

// ANN is the server's approximate search, shared by every tenant's Store on one connection
// (WithANN): the per-model indexes' rows, their builder (Run), and the candidate schemas, one per
// vector type. Without one, a Store's searches are exact.
type ANN struct {
	cfg  ANNConfig
	conn dao.DataConn
	run  string // this server run's owner name for the builder row

	rwIndex   *annIndexSchema
	rwBuilder *annBuilderSchema
	roIndex   *annIndexSchema
	rwMeta    *metaSchema
	rwEmb     *embSchema

	mu        sync.Mutex
	cand      map[dao.VectorType]*chunkSchema // snapshot schemas with an ANN sort key for the type
	underfill map[string]*atomic.Int64        // per model: candidate stages that came back short

	hook    dao.Hook    // the builder's statements, observed; nil in production
	checked atomic.Bool // the ready indexes' definitions were checked once this run (verifyReady)

	// leaseFor and renewEvery are a claim's lease and how often a build renews it (annLease,
	// annRenew; variables so a cell can shorten them)
	leaseFor, renewEvery time.Duration
}

// NewANN is the approximate search on conn (pgstore's pool), with cfg's settings. run names this
// server run as the builder row's owner: unique per process start.
func NewANN(conn dao.DataConn, cfg ANNConfig, run string) *ANN {
	return &ANN{cfg: cfg, conn: conn, run: run,
		rwIndex: newANNIndexSchema(committedConn{conn}), rwBuilder: newANNBuilderSchema(committedConn{conn}),
		roIndex: newANNIndexSchema(snapshotConn{conn}), rwMeta: newMetaSchema(committedConn{conn}), rwEmb: newEmbSchema(committedConn{conn}),
		cand: map[dao.VectorType]*chunkSchema{}, underfill: map[string]*atomic.Int64{},
		leaseFor: annLease, renewEvery: annRenew}
}

// candidates is t's candidate schema: the chunk schema with an ANN sort key over the embeddings
// cast to t, built once per type.
func (a *ANN) candidates(t dao.VectorType) *chunkSchema {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.cand[t]
	if s == nil {
		s = newChunkSchemaWith(snapshotConn{a.conn}, t)
		a.cand[t] = s
	}
	return s
}

func (a *ANN) underfillOf(model string) *atomic.Int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.underfill[model]
	if c == nil {
		c = new(atomic.Int64)
		a.underfill[model] = c
	}
	return c
}

// Want records that a tenant set model active or target: its index is wanted, unless it is
// already building or ready. A retiring index is wanted again. A model with no index type (more
// than 4,000 dimensions) records nothing: its searches stay exact.
func (a *ANN) Want(ctx context.Context, model string) error {
	if model == "" {
		return nil
	}
	t, reason := annType(model)
	if reason != "" {
		return nil
	}
	_, err := a.rwIndex.DAO(dao.WithQueryContext(ctx)).Set(aiModel, model).Set(aiName, annIndexName(model)).
		Set(aiDims, t.Dims).Set(aiState, annWanted).Insert()
	if !errors.Is(err, dao.ErrDuplicate) {
		return err
	}
	return dao.RunTx(ctx, func(tx *dao.Transaction) error {
		r, err := a.rwIndex.On(tx, dao.WithQueryContext(ctx), dao.WithRowLock(dao.LockForUpdate)).With(aiModel, model).Get(aiState)
		if err != nil || r.State != annRetiring {
			return err
		}
		return a.rwIndex.On(tx, dao.WithQueryContext(ctx)).With(aiModel, model).Set(aiState, annWanted).Update()
	})
}

// ANNStatus is a model's index as the server reports it.
type ANNStatus struct {
	Model, Mode, Reason string
	// Underfill counts this server run's candidate stages that came back short of k, so the
	// search answered exactly: a selective filter, a small share of a shared index, or a scan
	// that hit its budget.
	Underfill int64
}

// Status is every recorded model's index: its mode (ann when ready, ann-building while wanted or
// building, ann-failed with the error, exact while retiring) and the underfill count.
func (a *ANN) Status(ctx context.Context) ([]ANNStatus, error) {
	rows, err := a.rwIndex.DAO(dao.WithQueryContext(ctx)).Select()
	if err != nil {
		return nil, err
	}
	out := make([]ANNStatus, 0, len(rows))
	for _, r := range rows {
		s := ANNStatus{Model: r.Model, Underfill: a.underfillOf(r.Model).Load()}
		switch r.State {
		case annReady:
			s.Mode = ModeANN
		case annWanted, annBuilding:
			s.Mode = ModeANNBuilding
		case annFailed:
			s.Mode, s.Reason = ModeANNFailed, r.Error
		default:
			s.Mode, s.Reason = ModeExact, "the index is being retired"
		}
		out = append(out, s)
	}
	return out, nil
}

// --- the two-stage search --------------------------------------------------------------

// annSettings are the candidate stage's transaction-local settings for k candidates.
func (a *ANN) annSettings(k int) dao.QueryOption {
	return dao.WithLocalSettings(
		postgres.PlanCacheMode("force_custom_plan"),
		postgres.HNSWIterativeScan("relaxed_order"),
		postgres.HNSWEfSearch(min(max(k*max(a.cfg.EfFactor, 1), 100), 1000)),
		postgres.HNSWMaxScanTuples(a.cfg.MaxScanTuples),
		postgres.HNSWScanMemMultiplier(a.cfg.ScanMemMultiplier),
	)
}

// candidateCount is k for n hits: four times n, at least 100.
func candidateCount(n int) int { return max(4*n, 100) }

// semanticANN is Semantic through model's index, when it may answer: the index ready and the
// tenant past the threshold. ok false is "answer exactly": the index can't serve, or the candidate
// stage came back short of k (counted as underfill). Both statements run in the View's one
// snapshot.
func (v *View) semanticANN(ctx context.Context, model string, vec []float32, f search.Filter, n int) (out []search.Candidate[int64], ok bool, err error) {
	a := v.s.ann
	t, reason := annType(model)
	if reason != "" {
		return nil, false, nil
	}
	r, err := a.roIndex.On(v.tx, v.opts(ctx)...).With(aiModel, model).Get(aiState)
	if errors.Is(err, dao.ErrNoRows) || (err == nil && r.State != annReady) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	past, err := v.s.ro.emb.On(v.tx, v.opts(ctx)...).With(eTenant, v.s.tenant).With(eModel, model).
		Offset(uint64(a.cfg.Threshold - 1)).Limit(1).Select(eHash)
	if err != nil || len(past) == 0 {
		return nil, false, err
	}

	k := candidateCount(n)
	d := v.sectioned(ctx, a.candidates(t).On(v.tx, append(v.opts(ctx), a.annSettings(k))...).With(cTenant, v.s.tenant)).
		Join(joinDoc, joinEmb).
		WithPredicate(dao.Eq(qcol(tEmb, "model"), model)).
		WithPredicate(dao.Eq(qcol(tDoc, string(dReady)), true))
	for _, p := range v.where(f) {
		d = d.WithPredicate(p)
	}
	cands, err := d.OrderBy(dao.AscBy(cByANN, postgres.Vector(vec))).Limit(uint64(k)).Select(cDoc, cOrd)
	if err != nil {
		return nil, false, err
	}
	if len(cands) < k {
		a.underfillOf(model).Add(1)
		return nil, false, nil
	}

	keys := make([][]any, len(cands))
	for i, c := range cands {
		keys[i] = []any{c.Doc, c.Ord}
	}
	rows, err := v.retrieved(ctx).Join(joinDoc, joinEmb).
		WithPredicate(dao.Eq(qcol(tEmb, "model"), model)).
		WithPredicate(dao.KeysIn([]dao.KeyColumn{{Col: qcol(tChunk, string(cDoc)), Elem: dao.ArrayInt64}, {Col: qcol(tChunk, string(cOrd)), Elem: dao.ArrayInt32}}, keys)).
		OrderBy(dao.AscBy(cByNearest, postgres.Vector(vec)), dao.Asc(cByPath), dao.Asc(cByOrd)).Limit(uint64(n)).Select(hitFields()...)
	if err != nil {
		return nil, false, err
	}
	out = make([]search.Candidate[int64], 0, len(rows))
	for _, r := range rows {
		out = append(out, candidate(r, chunk.Snippet(r.Body)))
	}
	return out, true, nil
}

// ANNMode is model's search mode for this tenant, and why: exact below the threshold or with no
// index type; otherwise the index's mode (ANNStatus). A Store without an ANN is exact.
func (s *Store) ANNMode(ctx context.Context, model string) (mode, reason string, err error) {
	if s.ann == nil {
		return ModeExact, "approximate search is off", nil
	}
	if _, reason := annType(model); reason != "" {
		return ModeExact, reason, nil
	}
	past, err := s.rw.emb.DAO(dao.WithQueryContext(ctx)).With(eTenant, s.tenant).With(eModel, model).
		Offset(uint64(s.ann.cfg.Threshold - 1)).Limit(1).Select(eHash)
	if err != nil {
		return "", "", err
	}
	if len(past) == 0 {
		return ModeExact, fmt.Sprintf("fewer than %d embeddings of the model", s.ann.cfg.Threshold), nil
	}
	r, err := s.ann.rwIndex.DAO(dao.WithQueryContext(ctx)).With(aiModel, model).Get(aiState, aiError)
	switch {
	case errors.Is(err, dao.ErrNoRows):
		return ModeANNBuilding, "", nil
	case err != nil:
		return "", "", err
	}
	switch r.State {
	case annReady:
		return ModeANN, "", nil
	case annFailed:
		return ModeANNFailed, r.Error, nil
	case annRetiring:
		return ModeExact, "the index is being retired", nil
	}
	return ModeANNBuilding, "", nil
}
