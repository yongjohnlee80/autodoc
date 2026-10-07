package index

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// INDEX.STATUS, ONCE PER COMMIT (ADR 1791329335 §2.5) — a status is computed in one read transaction,
// which reads the workspace's commit_seq, the store's counts, the coverage of the model measured and
// whether any document is not ready; it is kept under that seq and model. Every writer transaction
// bumps commit_seq, so a later call at the same seq gets the same answer from one row read, and an
// idle workspace polled every second (the drawer, the TUI) costs nothing more. The change log's head
// and oldest retained seq are read on every call: gc prunes the log without a commit.
//
// One computation runs at a time. A call that waited for one reads commit_seq again, and computes
// when it has moved past the result's: no caller is answered for a superseded commit.

// statusSnap is one computed status: the store's part, and the coverage of model fp ("" without a
// provider), as of commit seq.
type statusSnap struct {
	seq     int64
	fp      string
	st      Status
	texts   int64 // distinct texts of the alive chunks
	missing int64 // those with no vector under fp
	unready bool  // a document is not semantically ready
}

// statusSnapshot is the status at the current commit: the kept one when nothing was committed since,
// else a new computation.
func (x *Indexer) statusSnapshot(ctx context.Context, fp string) (statusSnap, error) {
	x.statusWaiting.Add(1)
	select {
	case x.statusTurn <- struct{}{}:
		x.statusWaiting.Add(-1)
	case <-ctx.Done():
		x.statusWaiting.Add(-1)
		return statusSnap{}, ctx.Err()
	}
	defer func() { <-x.statusTurn }()
	s := x.store
	var seq int64
	var bounds Status
	if err := s.read(ctx, func(tx *store.Tx) error {
		var err error
		if seq, err = s.commitSeq(tx); err != nil {
			return err
		}
		return s.logBounds(tx, &bounds)
	}); err != nil {
		return statusSnap{}, err
	}
	if c := x.statusCache; c != nil && c.seq == seq && c.fp == fp {
		out := *c
		out.st.Cursor, out.st.OldestRetained = bounds.Cursor, bounds.OldestRetained
		return out, nil
	}
	snap, err := s.statusSnap(ctx, fp, x.opts.betweenStatusReads)
	if err != nil {
		return snap, err
	}
	x.statusCache = &snap
	if x.opts.onStatusCompute != nil {
		x.opts.onStatusCompute()
	}
	return snap, nil
}

// statusSnap computes a status in one read transaction. With a model, the alive chunks are listed
// once, for their count and for the coverage. between, when set (tests only), runs after the counts
// and before the coverage: a commit there is in no part of this snapshot.
func (s *Store) statusSnap(ctx context.Context, fp string, between func()) (statusSnap, error) {
	out := statusSnap{fp: fp}
	err := s.read(ctx, func(tx *store.Tx) error {
		var err error
		if out.seq, err = s.commitSeq(tx); err != nil {
			return err
		}
		if err := s.logBounds(tx, &out.st); err != nil {
			return err
		}
		if err := s.counts(tx, &out.st); err != nil {
			return err
		}
		if between != nil {
			between()
		}
		if fp == "" {
			n, err := alive(s.sc.Chunks(tx)).Count()
			out.st.Chunks = int64(n)
			return err
		}
		rows, err := alive(s.sc.Chunks(tx)).Select(store.ChunkTextHash)
		if err != nil {
			return err
		}
		out.st.Chunks = int64(len(rows))
		have, err := s.embeddedTexts(tx, fp)
		if err != nil {
			return err
		}
		texts := make(map[string]bool, len(rows))
		for _, r := range rows {
			h := string(r.TextHash)
			if !texts[h] {
				texts[h] = true
				if !have[h] {
					out.missing++
				}
			}
		}
		out.texts = int64(len(texts))
		out.unready, err = s.sc.Documents(tx).With(store.DocSemanticReady, int64(0)).Exists()
		return err
	})
	return out, err
}
