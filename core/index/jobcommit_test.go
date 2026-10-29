package index

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
)

// jobUpserts counts the job rows the indexer's store writes, commit by commit.
type jobUpserts struct {
	dao.NopHook
	mu        sync.Mutex
	open      int   // since the last commit
	perCommit []int // each commit's
}

func (c *jobUpserts) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	if q.Op == dao.OpUpsert && q.Table == "index_job" {
		c.mu.Lock()
		c.open++
		c.mu.Unlock()
	}
	return nil
}

func (c *jobUpserts) committed(time.Duration) {
	c.mu.Lock()
	c.perCommit = append(c.perCommit, c.open)
	c.open = 0
	c.mu.Unlock()
}

// TestAScansTouchesAreWrittenOverSeveralCommits (AP-8): thousands of paths touched at once (a
// scan's) have their job rows written a bounded number per commit, beyond the batch's own, so no
// one transaction holds the store's only writer for all of them; and every one is indexed.
func TestAScansTouchesAreWrittenOverSeveralCommits(t *testing.T) {
	defer func(n int) { jobsPerCommit = n }(jobsPerCommit)
	jobsPerCommit = 3
	const batchSize, files = 2, 30
	c := &jobUpserts{}
	opts := options{BatchSize: batchSize, onCommit: c.committed}
	e := newEnv(t, opts)
	e.hook = c
	e.open(opts)
	var paths []string
	for i := range files {
		p := fmt.Sprintf("d%02d.md", i)
		paths = append(paths, p)
		e.write(p, fmt.Sprintf("# D%02d\n\nbody\n", i))
	}
	for _, p := range paths {
		e.ix.Touch(p)
	}
	for _, p := range paths {
		e.indexedAt(p)
	}
	c.mu.Lock()
	per := append([]int(nil), c.perCommit...)
	c.mu.Unlock()
	total := 0
	for i, n := range per {
		total += n
		if n > jobsPerCommit+batchSize {
			t.Errorf("commit %d wrote %d job rows, want at most %d (%d beyond its batch of at most %d): %v",
				i, n, jobsPerCommit+batchSize, jobsPerCommit, batchSize, per)
		}
	}
	if total < files {
		t.Errorf("%d job rows written for %d touched paths: %v", total, files, per)
	}
	if n := e.pendingJobs(); n != 0 {
		t.Errorf("%d jobs left after every path was indexed", n)
	}
}

// TestACommitThatWritesNoDocumentKeepsTheCodes: a commit that wrote no document (a touch's job
// rows alone, a read that failed) publishes a new header over the same codes, not a reload of every
// document's.
func TestACommitThatWritesNoDocumentKeepsTheCodes(t *testing.T) {
	var rebuilds atomic.Int32
	p := newFake("m", "a")
	e := newEnv(t, options{Provider: p, onRebuild: func() { rebuilds.Add(1) }})
	e.put("a.md", "# A\n\nalpha\n", "b.md", "# B\n\nbeta\n")
	e.ready()
	before := rebuilds.Load()
	e.fault.failOpen("c.md", errors.New("disk on fire"))
	e.write("c.md", "# C\n\ngamma\n")
	e.ix.Touch("c.md")
	e.eventually("c.md's failure recorded", func() bool {
		_, attempts, _ := e.job("c.md")
		return attempts >= 1
	})
	if n := rebuilds.Load() - before; n != 0 {
		t.Errorf("%d publications reloaded every document's codes after commits that wrote none", n)
	}
}
