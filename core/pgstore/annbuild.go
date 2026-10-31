package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	acclock "github.com/yongjohnlee80/autodoc/core/clock"
	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
)

// The ANN indexes' lifecycle: one builder at a time for the table (the rag_ann_builder row),
// every write a builder makes checked against its owner and generation, recovery that fences a
// lapsed owner before it touches anything, and retirement once no tenant uses a model. Requests
// never wait on it: building is this loop's alone.

const (
	annLease       = 2 * time.Minute  // a claim's lease, renewed while the build runs
	annRenew       = 30 * time.Second // how often a build renews it
	annMaxAttempts = 5                // failures before a model's index stays failed for an operator
	annBackoffBase = time.Minute      // the first retry's wait, doubled per failure
	annBackoffMax  = time.Hour
)

// errFenced is an ownership-checked write that matched no builder row: another owner (a
// recovery) took the row, and the writer stops.
var errFenced = errors.New("pgstore: the ANN builder was fenced")

// annJob is one claim of the builder row: the model, and the owner and generation every write
// of it is checked against.
type annJob struct {
	model, owner string
	gen          int64
	t            dao.VectorType
	replace      bool // the model's index serves (replacing): its replacement is built under annNextName
}

// token is the session mark of the job's builder (postgres.MarkSession): its owner and
// generation, so a recoverer can find the backend and nothing else.
func (j annJob) token() string {
	return strings.TrimPrefix(j.owner, "recovering:") + ":" + strconv.FormatInt(j.gen, 10)
}

func (a *ANN) now() time.Time { return time.Now().UTC() }

func (a *ANN) opts(ctx context.Context) []dao.QueryOption {
	if a.hook != nil {
		return []dao.QueryOption{dao.WithQueryContext(ctx), dao.WithHooks(a.hook)}
	}
	return []dao.QueryOption{dao.WithQueryContext(ctx)}
}

func (a *ANN) locked(ctx context.Context) []dao.QueryOption {
	return append(a.opts(ctx), dao.WithRowLock(dao.LockForUpdate))
}

// builderRow reads the builder row in tx, locked: every D5 transaction takes it first.
func (a *ANN) builderRow(ctx context.Context, tx *dao.Transaction) (*annBuilderRow, error) {
	return a.rwBuilder.On(tx, a.locked(ctx)...).With(abID, 1).Get()
}

// owned runs more in one transaction after an ownership-checked update of the builder row (set,
// matched by job's owner and generation). A row that doesn't match (the builder was fenced)
// changes nothing, neither row, and answers errFenced.
func (a *ANN) owned(ctx context.Context, job annJob, set func(dao.DAO[*annBuilderRow, annBuilderField, int]) dao.DAO[*annBuilderRow, annBuilderField, int], more func(tx *dao.Transaction) error) error {
	return dao.RunTx(ctx, func(tx *dao.Transaction) error {
		d := set(a.rwBuilder.On(tx, a.opts(ctx)...).With(abID, 1).With(abOwner, job.owner).With(abGen, job.gen))
		n, err := d.(dao.AffectedUpdater).UpdateAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errFenced
		}
		if more != nil {
			return more(tx)
		}
		return nil
	})
}

// freed is set's builder row with no model, no backend and no lease.
func freed(d dao.DAO[*annBuilderRow, annBuilderField, int]) dao.DAO[*annBuilderRow, annBuilderField, int] {
	return d.Set(abModel, nil).Set(abPID, nil).Set(abStart, nil).Set(abToken, "").Set(abLease, nil)
}

// Run is the builder and maintenance loop: a Tick at once, then one every interval, until ctx
// ends. report is told each tick's error; nothing stops the loop but ctx.
func (a *ANN) Run(ctx context.Context, every time.Duration, report func(error)) {
	for {
		if err := a.Tick(ctx); err != nil && ctx.Err() == nil && report != nil {
			report(err)
		}
		// NOTE: the maintenance tick of the ANN builder, a background loop: a lapsed lease, a
		// wanted index and a retired model are found by looking again, never by a signal, since
		// another server's crash sends none.
		if !acclock.Sleep(ctx, every) {
			return
		}
	}
}

// Tick is one round of maintenance: recover a lapsed builder, check the ready indexes (their
// definition too, the first time), retire the models no tenant uses, then claim and build one
// wanted index. Its errors are joined; one step's failure doesn't skip the others.
func (a *ANN) Tick(ctx context.Context) error {
	var errs []error
	if err := a.recoverLapsed(ctx); err != nil {
		errs = append(errs, fmt.Errorf("recovery: %w", err))
	}
	if err := a.wantNamed(ctx); err != nil {
		errs = append(errs, fmt.Errorf("wanting the tenants' models: %w", err))
	}
	if err := a.verifyReady(ctx); err != nil {
		errs = append(errs, fmt.Errorf("checking the ready indexes: %w", err))
	}
	if err := a.retire(ctx); err != nil {
		errs = append(errs, fmt.Errorf("retiring: %w", err))
	}
	job, err := a.claim(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("claiming: %w", err))
	} else if job != nil {
		if err := a.build(ctx, *job); err != nil && !errors.Is(err, errFenced) {
			errs = append(errs, fmt.Errorf("building %s: %w", job.model, err))
		}
	}
	return errors.Join(errs...)
}

// wantNamed wants the index of every model a tenant has active or targeted that has no row: a
// deployment's from before approximate search, one whose Want was lost after its models were
// written, or one an operator cleared to retry. The builder row is held first, as in every D5
// transaction; a row Want inserts meanwhile is kept as it is.
func (a *ANN) wantNamed(ctx context.Context) error {
	return dao.RunTx(ctx, func(tx *dao.Transaction) error {
		if _, err := a.builderRow(ctx, tx); err != nil {
			return err
		}
		metas, err := a.rwMeta.On(tx, a.opts(ctx)...).Select(mActive, mTarget)
		if err != nil {
			return err
		}
		rows, err := a.rwIndex.On(tx, a.opts(ctx)...).Select(aiModel)
		if err != nil {
			return err
		}
		have := map[string]bool{"": true}
		for _, r := range rows {
			have[r.Model] = true
		}
		for _, m := range metas {
			for _, model := range []string{m.Active, m.Target} {
				t, reason := annType(model)
				if have[model] || reason != "" {
					continue
				}
				have[model] = true
				d := a.rwIndex.On(tx, a.opts(ctx)...).Set(aiModel, model).Set(aiName, annIndexName(model)).
					Set(aiDims, t.Dims).Set(aiState, annWanted)
				if err := d.(dao.SelectiveUpserter[annIndexField]).UpsertOnly(aiModel); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// claim takes the builder row for one wanted index, or a failed one whose retry is due, when the
// row is free: a new generation, this run as owner, a lease, the model's row building. nil is
// nothing to do (the row busy, or nothing wanted).
func (a *ANN) claim(ctx context.Context) (*annJob, error) {
	var job *annJob
	err := dao.RunTx(ctx, func(tx *dao.Transaction) error {
		b, err := a.builderRow(ctx, tx)
		if err != nil || b.Model != nil {
			return err // busy, or lapsed: recoverLapsed's
		}
		now := a.now()
		// a failed index, or one replacing (which serves meanwhile), is due once its backoff ends
		due := dao.Or(dao.Eq(qcol(tANNIndex, string(aiState)), annWanted),
			dao.And(dao.In(qcol(tANNIndex, string(aiState)), []any{annFailed, annReplacing}),
				dao.Lt(qcol(tANNIndex, string(aiAttempts)), annMaxAttempts),
				dao.Or(dao.IsNull(qcol(tANNIndex, string(aiNextAt))), dao.Lte(qcol(tANNIndex, string(aiNextAt)), now))))
		rows, err := a.rwIndex.On(tx, a.locked(ctx)...).WithPredicate(due).Limit(1).Select(aiModel, aiState)
		if err != nil || len(rows) == 0 {
			return err
		}
		model := rows[0].Model
		t, reason := annType(model)
		if reason != "" {
			return a.rwIndex.On(tx, a.opts(ctx)...).With(aiModel, model).
				Set(aiState, annFailed).Set(aiAttempts, annMaxAttempts).Set(aiError, reason).Set(aiUpdated, now).Update()
		}
		j := annJob{model: model, owner: a.run, gen: b.Generation + 1, t: t, replace: rows[0].State == annReplacing}
		if err := a.rwBuilder.On(tx, a.opts(ctx)...).With(abID, 1).Set(abModel, model).Set(abOwner, j.owner).
			Set(abGen, j.gen).Set(abLease, now.Add(a.leaseFor)).Set(abPID, nil).Set(abStart, nil).Set(abToken, "").Update(); err != nil {
			return err
		}
		if j.replace {
			job = &j // its row stays replacing: the index serves until the replacement is swapped in
			return nil
		}
		if err := a.rwIndex.On(tx, a.opts(ctx)...).With(aiModel, model).Set(aiState, annBuilding).Set(aiUpdated, now).Update(); err != nil {
			return err
		}
		job = &j
		return nil
	})
	return job, err
}

// build makes job's index on a session of its own, registered first (the backend recovery would
// cancel), renewing the lease while CREATE INDEX CONCURRENTLY runs, and completes it: ready when
// the index is valid and its definition is the declared one, else failed with a backoff. A valid
// index of the name already matching the declaration is adopted, never rebuilt; a replacing one is
// rebuilt under a second name while it serves (replaceIndex), and stays replacing if that fails;
// any other is dropped first. Fenced at any write, it stops and changes nothing.
func (a *ANN) build(ctx context.Context, job annJob) (err error) {
	p, err := postgres.PinSessionConn(ctx, a.conn)
	if err != nil {
		return a.fail(ctx, job, err)
	}
	defer p.Discard()
	backend, err := postgres.MarkSession(ctx, p, job.token())
	if err != nil {
		return a.fail(ctx, job, err)
	}
	if err := a.owned(ctx, job, func(d dao.DAO[*annBuilderRow, annBuilderField, int]) dao.DAO[*annBuilderRow, annBuilderField, int] {
		return d.Set(abPID, backend.PID).Set(abStart, backend.Start).Set(abToken, backend.Token)
	}, nil); err != nil {
		return err // fenced: the DDL never starts
	}
	// the lease is renewed from here on, through the drop of an older index as well as the
	// CREATE: either can wait on another transaction for longer than a lease
	buildCtx, cancel := context.WithCancel(ctx)
	renewed := make(chan error, 1)
	go func() { renewed <- a.renew(buildCtx, job, cancel) }()
	err = a.makeIndex(buildCtx, job, p)
	cancel()
	if rerr := <-renewed; errors.Is(rerr, errFenced) {
		return errFenced // recovery took the row; it drops what this left
	}
	return err
}

// makeIndex is build's work on its registered session p, under the renewed lease: the declared
// index adopted or made, then completed.
func (a *ANN) makeIndex(ctx context.Context, job annJob, p postgres.PinnedConn) error {
	spec, err := annSpec(job.model, job.t, a.cfg)
	if err != nil {
		return a.fail(ctx, job, err)
	}
	ref, err := postgres.ReferenceDefinition(ctx, p, spec)
	if err != nil {
		return a.fail(ctx, job, err)
	}
	st, err := postgres.ReadIndexState(ctx, a.conn, "", spec.Name)
	if err != nil {
		return a.fail(ctx, job, err)
	}
	if job.replace && st.Exists && st.Valid && st.Def != ref {
		if st, err = a.replaceIndex(ctx, job, p, spec, ref); err != nil {
			return a.fail(ctx, job, err)
		}
	}
	if !(st.Exists && st.Valid && st.Def == ref) {
		if st.Exists {
			if err := postgres.DropIndexConcurrently(ctx, p, "", spec.Name, postgres.BuildOptions{}); err != nil {
				return a.fail(ctx, job, err)
			}
		}
		if err := postgres.CreateIndexConcurrently(ctx, p, spec, postgres.BuildOptions{}); err != nil {
			return a.fail(ctx, job, err)
		}
		if st, err = postgres.ReadIndexState(ctx, a.conn, "", spec.Name); err != nil {
			return a.fail(ctx, job, err)
		}
	}
	if !st.Exists || !st.Valid || st.Def != ref {
		return a.fail(ctx, job, fmt.Errorf("the index built is not the one declared: %+v, want %+v (valid %v)", st.Def, ref, st.Valid))
	}
	def, _ := json.Marshal(ref)
	return a.owned(ctx, job, freed, func(tx *dao.Transaction) error {
		return a.rwIndex.On(tx, a.opts(ctx)...).With(aiModel, job.model).Set(aiState, annReady).Set(aiDef, string(def)).
			Set(aiAttempts, 0).Set(aiNextAt, nil).Set(aiError, "").Set(aiUpdated, a.now()).Update()
	})
}

// replaceIndex replaces the model's index, which serves, with spec's while it keeps serving
// (docs/enterprise/server-config.md): the replacement built concurrently under annNextName (a leftover of
// an earlier attempt dropped first), the old index dropped concurrently once the replacement is
// valid and the declared one (the replacement serves the searches from then on), and the
// replacement renamed. It answers the index's state under its own name.
func (a *ANN) replaceIndex(ctx context.Context, job annJob, p postgres.PinnedConn, spec postgres.IndexSpec, ref postgres.IndexDefinition) (postgres.IndexState, error) {
	next := spec
	next.Name = annNextName(job.model)
	if err := postgres.DropIndexConcurrently(ctx, p, "", next.Name, postgres.BuildOptions{}); err != nil {
		return postgres.IndexState{}, err
	}
	if err := postgres.CreateIndexConcurrently(ctx, p, next, postgres.BuildOptions{}); err != nil {
		return postgres.IndexState{}, err
	}
	nst, err := postgres.ReadIndexState(ctx, a.conn, "", next.Name)
	if err != nil {
		return postgres.IndexState{}, err
	}
	if !nst.Exists || !nst.Valid || nst.Def != ref {
		return postgres.IndexState{}, fmt.Errorf("the replacement built is not the one declared: %+v, want %+v (valid %v)", nst.Def, ref, nst.Valid)
	}
	if err := postgres.DropIndexConcurrently(ctx, p, "", spec.Name, postgres.BuildOptions{}); err != nil {
		return postgres.IndexState{}, err
	}
	if err := postgres.RenameIndex(ctx, p, "", next.Name, spec.Name, postgres.BuildOptions{}); err != nil {
		return postgres.IndexState{}, err
	}
	return postgres.ReadIndexState(ctx, a.conn, "", spec.Name)
}

// finishSwap finishes a replacement stopped between its two last steps: with the model's own index
// gone (own) and the one under the second name valid and the declared one, it renames the
// replacement and answers its definition, as rag_ann_index.def holds it. Anything else is left as
// it is (swapped false).
func (a *ANN) finishSwap(ctx context.Context, p postgres.PinnedConn, model string, own postgres.IndexState) (swapped bool, def string, err error) {
	if own.Exists {
		return false, "", nil
	}
	next, err := postgres.ReadIndexState(ctx, a.conn, "", annNextName(model))
	if err != nil || !next.Exists || !next.Valid {
		return false, "", err
	}
	t, reason := annType(model)
	if reason != "" {
		return false, "", nil
	}
	spec, err := annSpec(model, t, a.cfg)
	if err != nil {
		return false, "", err
	}
	ref, err := postgres.ReferenceDefinition(ctx, p, spec)
	if err != nil || next.Def != ref {
		return false, "", err
	}
	if err := postgres.RenameIndex(ctx, p, "", annNextName(model), annIndexName(model), postgres.BuildOptions{}); err != nil {
		return false, "", err
	}
	b, _ := json.Marshal(ref)
	return true, string(b), nil
}

// renew extends job's lease every annRenew until ctx ends. Fenced, it cancels the build (stop)
// and answers errFenced.
func (a *ANN) renew(ctx context.Context, job annJob, stop context.CancelFunc) error {
	for {
		// NOTE: the build's lease renewal: CREATE INDEX CONCURRENTLY runs for minutes on one
		// statement, and the lease must outlive it while this server lives, by a renewal on a
		// clock, since nothing else happens meanwhile to renew it on.
		if !acclock.Sleep(ctx, a.renewEvery) {
			return nil
		}
		err := a.owned(ctx, job, func(d dao.DAO[*annBuilderRow, annBuilderField, int]) dao.DAO[*annBuilderRow, annBuilderField, int] {
			return d.Set(abLease, a.now().Add(a.leaseFor))
		}, nil)
		if errors.Is(err, errFenced) {
			stop()
			return err
		}
	}
}

// fail completes job as failed with cause: the model's row failed, its attempts counted and its
// retry backed off (1 min × 2^attempts, at most an hour), the builder row freed, all
// ownership-checked. An index left by the failure is dropped first, under the same check. It
// answers cause (or errFenced).
func (a *ANN) fail(ctx context.Context, job annJob, cause error) error {
	if p, err := postgres.PinSessionConn(ctx, a.conn); err == nil {
		if a.stillOwns(ctx, job) {
			// a replacement's failure leaves the index that serves: only its second name goes
			name := annIndexName(job.model)
			if job.replace {
				name = annNextName(job.model)
			}
			_ = postgres.DropIndexConcurrently(ctx, p, "", name, postgres.BuildOptions{})
		}
		p.Discard()
	}
	state := annFailed
	if job.replace {
		state = annReplacing // it still serves; the replacement is retried after the backoff
	}
	err := a.owned(ctx, job, freed, func(tx *dao.Transaction) error {
		r, err := a.rwIndex.On(tx, a.locked(ctx)...).With(aiModel, job.model).Get(aiAttempts)
		if err != nil {
			return err
		}
		attempts := r.Attempts + 1
		wait := min(annBackoffBase<<min(attempts-1, 10), annBackoffMax)
		return a.rwIndex.On(tx, a.opts(ctx)...).With(aiModel, job.model).Set(aiState, state).Set(aiAttempts, attempts).
			Set(aiNextAt, a.now().Add(wait)).Set(aiError, cause.Error()).Set(aiUpdated, a.now()).Update()
	})
	if errors.Is(err, errFenced) {
		return err
	}
	return errors.Join(cause, err)
}

// stillOwns reports whether job still holds the builder row: the check a deferred cleanup (a
// drop outside any transaction) makes before it acts.
func (a *ANN) stillOwns(ctx context.Context, job annJob) bool {
	b, err := a.rwBuilder.DAO(a.opts(ctx)...).With(abID, 1).Get()
	return err == nil && b.Owner == job.owner && b.Generation == job.gen
}

// recoverLapsed takes a builder row whose lease lapsed: it fences the old owner at once (a new
// generation, owner recovering:<this run>), then cancels the old backend and, a tick later,
// terminates it if it still runs, doing nothing else while it lives. Once it is gone, the invalid
// index is dropped and the builder row freed, under the recoverer's own generation. The model's row
// is wanted again, but for a replacement: one that still has its own index serving stays replacing
// (due again as it is), and one stopped between the old index's drop and the rename is finished, the
// replacement renamed and the model ready (finishSwap).
func (a *ANN) recoverLapsed(ctx context.Context) error {
	var rec *annJob
	var old postgres.Backend
	var resumed bool
	err := dao.RunTx(ctx, func(tx *dao.Transaction) error {
		b, err := a.builderRow(ctx, tx)
		if err != nil || b.Model == nil {
			return err
		}
		mine := b.Owner == "recovering:"+a.run
		if !mine && (b.Lease == nil || b.Lease.After(a.now())) {
			return nil // a live claim
		}
		if b.PID != nil && b.Start != nil {
			old = postgres.Backend{PID: *b.PID, Start: *b.Start, Token: b.Token}
		}
		if mine {
			rec, resumed = &annJob{model: *b.Model, owner: b.Owner, gen: b.Generation}, true
			return nil
		}
		j := annJob{model: *b.Model, owner: "recovering:" + a.run, gen: b.Generation + 1}
		if err := a.rwBuilder.On(tx, a.opts(ctx)...).With(abID, 1).Set(abOwner, j.owner).Set(abGen, j.gen).
			Set(abLease, a.now().Add(a.leaseFor)).Update(); err != nil {
			return err
		}
		rec = &j
		return nil
	})
	if err != nil || rec == nil {
		return err
	}
	if old.PID != 0 {
		alive, err := postgres.BackendAlive(ctx, a.conn, old)
		if err != nil {
			return err
		}
		if alive {
			// a first pass cancels the old CREATE; a resumed one, a tick later, terminates it
			if resumed {
				_, err = postgres.TerminateBackend(ctx, a.conn, old)
			} else {
				_, err = postgres.CancelBackend(ctx, a.conn, old)
			}
			if err == nil {
				err = a.owned(ctx, *rec, func(d dao.DAO[*annBuilderRow, annBuilderField, int]) dao.DAO[*annBuilderRow, annBuilderField, int] {
					return d.Set(abLease, a.now().Add(a.leaseFor))
				}, nil)
			}
			return err
		}
	}
	p, err := postgres.PinSessionConn(ctx, a.conn)
	if err != nil {
		return err
	}
	defer p.Discard()
	if !a.stillOwns(ctx, *rec) {
		return nil
	}
	// an invalid index is the lapsed build's leftover; a valid one is kept for the next build to
	// adopt, or to drop if its definition isn't the declared one
	st, err := postgres.ReadIndexState(ctx, a.conn, "", annIndexName(rec.model))
	if err != nil {
		return err
	}
	if st.Exists && !st.Valid {
		if err := postgres.DropIndexConcurrently(ctx, p, "", annIndexName(rec.model), postgres.BuildOptions{}); err != nil {
			return err
		}
	}
	// a lapsed replacement: one that dropped the old index and left the replacement, valid and the
	// declared one, under its second name, stopped one step short of the swap, which is finished here
	// (the rename) and the model ready; any other leftover of the second name goes
	swapped, def, err := a.finishSwap(ctx, p, rec.model, st)
	if err != nil {
		return err
	}
	if !swapped {
		if err := postgres.DropIndexConcurrently(ctx, p, "", annNextName(rec.model), postgres.BuildOptions{}); err != nil {
			return err
		}
	}
	serving := st.Exists && st.Valid // the index of the model's own name, which a replacing row serves from
	err = a.owned(ctx, *rec, freed, func(tx *dao.Transaction) error {
		if swapped {
			return a.rwIndex.On(tx, a.opts(ctx)...).With(aiModel, rec.model).Set(aiState, annReady).Set(aiDef, def).
				Set(aiAttempts, 0).Set(aiNextAt, nil).Set(aiError, "").Set(aiUpdated, a.now()).Update()
		}
		r, err := a.rwIndex.On(tx, a.locked(ctx)...).With(aiModel, rec.model).Get(aiState)
		if err != nil || (r.State == annReplacing && serving) {
			return err // replacing with its index in place: due again as it is, serving meanwhile
		}
		// no index of its own to serve from: wanted again, and its searches exact meanwhile
		return a.rwIndex.On(tx, a.opts(ctx)...).With(aiModel, rec.model).Set(aiState, annWanted).Set(aiUpdated, a.now()).Update()
	})
	if errors.Is(err, errFenced) {
		return nil
	}
	return err
}

// verifyReady checks every ready index: one missing or invalid is wanted again, and searches treat
// it as exact from that moment; one valid but (on the first tick of this run) built otherwise than
// declared, as after an upgrade that changed the build parameters, is replacing: it serves while
// the builder makes its replacement under a second name and swaps it in (replaceIndex).
func (a *ANN) verifyReady(ctx context.Context) error {
	rows, err := a.rwIndex.DAO(a.opts(ctx)...).With(aiState, annReady).Select(aiModel)
	if err != nil {
		return err
	}
	first := !a.checked.Swap(true)
	for _, r := range rows {
		st, err := postgres.ReadIndexState(ctx, a.conn, "", annIndexName(r.Model))
		if err != nil {
			return err
		}
		good := st.Exists && st.Valid
		next := annWanted
		if good && first {
			t, reason := annType(r.Model)
			if reason != "" {
				good = false
			} else if good, err = a.matches(ctx, r.Model, t, st.Def); err != nil {
				return err
			} else if !good {
				next = annReplacing // valid, built otherwise: it serves while it is replaced
			}
		}
		if good {
			continue
		}
		if err := dao.RunTx(ctx, func(tx *dao.Transaction) error {
			if _, err := a.builderRow(ctx, tx); err != nil {
				return err
			}
			return a.rwIndex.On(tx, a.opts(ctx)...).With(aiModel, r.Model).With(aiState, annReady).
				Set(aiState, next).Set(aiAttempts, 0).Set(aiNextAt, nil).Set(aiUpdated, a.now()).Update()
		}); err != nil {
			return err
		}
	}
	if first {
		return a.verifyReplacing(ctx)
	}
	return nil
}

// verifyReplacing checks every replacing row, on the first tick of a run: a replacing index serves
// from its own name while its replacement is built, so one whose own index is gone or invalid (an
// index dropped by hand, a restore without it) serves nothing, and is wanted again, its searches
// exact meanwhile. A row the builder holds is left to it: its replacement may be between the old
// index's drop and the rename, which the build or its recovery finishes (finishSwap).
func (a *ANN) verifyReplacing(ctx context.Context) error {
	rows, err := a.rwIndex.DAO(a.opts(ctx)...).With(aiState, annReplacing).Select(aiModel)
	if err != nil {
		return err
	}
	for _, r := range rows {
		st, err := postgres.ReadIndexState(ctx, a.conn, "", annIndexName(r.Model))
		if err != nil {
			return err
		}
		if st.Exists && st.Valid {
			continue
		}
		if err := dao.RunTx(ctx, func(tx *dao.Transaction) error {
			b, err := a.builderRow(ctx, tx)
			if err != nil || (b.Model != nil && *b.Model == r.Model) {
				return err
			}
			return a.rwIndex.On(tx, a.opts(ctx)...).With(aiModel, r.Model).With(aiState, annReplacing).
				Set(aiState, annWanted).Set(aiAttempts, 0).Set(aiNextAt, nil).Set(aiUpdated, a.now()).Update()
		}); err != nil {
			return err
		}
	}
	return nil
}

// matches reports whether def is model's declared index definition (its reference build).
func (a *ANN) matches(ctx context.Context, model string, t dao.VectorType, def postgres.IndexDefinition) (bool, error) {
	spec, err := annSpec(model, t, a.cfg)
	if err != nil {
		return false, err
	}
	p, err := postgres.PinSessionConn(ctx, a.conn)
	if err != nil {
		return false, err
	}
	defer p.Discard()
	ref, err := postgres.ReferenceDefinition(ctx, p, spec)
	return err == nil && ref == def, err
}

// retire drops the index of each model no tenant has active or targeted and of which no
// embedding remains: marked retiring in a transaction holding both rows, dropped concurrently,
// and its row deleted only if still retiring (an attach meanwhile set it back to wanted, and the
// builder makes it again). A model the builder holds is left alone.
func (a *ANN) retire(ctx context.Context) error {
	rows, err := a.rwIndex.DAO(a.opts(ctx)...).WithPredicate(dao.NotIn(qcol(tANNIndex, string(aiState)), []any{annBuilding})).Select(aiModel, aiState)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var retiring bool
		err := dao.RunTx(ctx, func(tx *dao.Transaction) error {
			b, err := a.builderRow(ctx, tx)
			if err != nil || (b.Model != nil && *b.Model == r.Model) {
				return err
			}
			cur, err := a.rwIndex.On(tx, a.locked(ctx)...).With(aiModel, r.Model).Get(aiState)
			if err != nil || cur.State == annBuilding {
				return err
			}
			if cur.State != annRetiring {
				used, err := a.rwMeta.On(tx, a.opts(ctx)...).WithPredicate(dao.Or(dao.Eq(qcol(tMeta, string(mActive)), r.Model),
					dao.Eq(qcol(tMeta, string(mTarget)), r.Model))).Exists()
				if err != nil || used {
					return err
				}
				left, err := a.rwEmb.On(tx, a.opts(ctx)...).With(eModel, r.Model).Exists()
				if err != nil || left {
					return err
				}
				if err := a.rwIndex.On(tx, a.opts(ctx)...).With(aiModel, r.Model).Set(aiState, annRetiring).Set(aiUpdated, a.now()).Update(); err != nil {
					return err
				}
			}
			retiring = true
			return nil
		})
		if err != nil || !retiring {
			if err != nil {
				return err
			}
			continue
		}
		p, err := postgres.PinSessionConn(ctx, a.conn)
		if err != nil {
			return err
		}
		err = postgres.DropIndexConcurrently(ctx, p, "", annIndexName(r.Model), postgres.BuildOptions{})
		if err == nil {
			err = postgres.DropIndexConcurrently(ctx, p, "", annNextName(r.Model), postgres.BuildOptions{})
		}
		p.Discard()
		if err != nil {
			return err
		}
		if err := a.rwIndex.DAO(a.opts(ctx)...).With(aiModel, r.Model).With(aiState, annRetiring).Delete(); err != nil {
			return err
		}
	}
	return nil
}
