//go:build server

package store

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMeasureACopy measures a copy at a store's shape (AUTODOC_MEASURE_COPY): "catalog" is a
// 56,000-document catalog's (168,000 chunks, each with a 1024-dimension embedding), "1m"
// is a million chunks (100,000 documents, no embeddings). It logs the copy's time, the process's
// peak RSS during it, and the WAL PostgreSQL wrote for it.
func TestMeasureACopy(t *testing.T) {
	shape := os.Getenv("AUTODOC_MEASURE_COPY")
	var docs, chunks, dims int
	switch shape {
	case "catalog":
		docs, chunks, dims = 56_000, 168_000, 1024
	case "1m":
		docs, chunks, dims = 100_000, 1_000_000, 0
	default:
		t.Skip("set AUTODOC_MEASURE_COPY=catalog or 1m to measure a copy")
	}
	ctx := context.Background()
	dst, src := copyStores(t)
	began := time.Now()
	tx, err := src.w.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO workspace (id, name, root, created_at, updated_at) VALUES (1, 'kb', '/kb', 0, 0)`)
	if dims > 0 {
		exec(`INSERT INTO model (workspace_id, fp, provider, name, dims, active) VALUES (1, 'fp', 'p', 'm', ?, 1)`, dims)
	}
	body := strings.Repeat("words of a section that a chunk holds, ", 20) // ~800 bytes
	f32 := make([]byte, 4*dims)
	bits := make([]byte, (dims+7)/8)
	rng := rand.New(rand.NewPCG(1, 2))
	for d := 1; d <= docs; d++ {
		exec(`INSERT INTO document (id, workspace_id, path, version, active_gen, indexer, indexed_at) VALUES (?, 1, ?, 'v', 1, 'x', 0)`,
			d, fmt.Sprintf("d/%06d.md", d))
	}
	for c := 1; c <= chunks; c++ {
		h := sha256.Sum256(binary.BigEndian.AppendUint64(nil, uint64(c)))
		exec(`INSERT INTO chunk (id, workspace_id, doc_id, hash, text_hash, gen_from, ord, breadcrumb, body, title, tags, byte_start, byte_end)
			VALUES (?, 1, ?, ?, ?, 1, ?, 'Section', ?, 'Title', '', 0, 800)`, c, 1+(c-1)%docs, h[:], h[:], c, body)
		if dims > 0 {
			// random, as a model's floats are: PostgreSQL would compress zeros to nothing
			for k := 0; k+8 <= len(f32); k += 8 {
				binary.LittleEndian.PutUint64(f32[k:], rng.Uint64())
			}
			for k := range bits {
				bits[k] = byte(rng.Uint64())
			}
			exec(`INSERT INTO embedding (workspace_id, text_hash, model_fp, bits, f32) VALUES (1, ?, 'fp', ?, ?)`, h[:], bits, f32)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Clean(src.path))
	t.Logf("measure %s: the source (%d documents, %d chunks, %d-dimension embeddings) filled in %s, %d MB", shape, docs, chunks, dims,
		time.Since(began).Round(time.Second), fi.Size()>>20)

	var lsn0 string
	if err := scanOne(ctx, dst.w, `SELECT pg_current_wal_lsn()::text`, []any{&lsn0}); err != nil {
		t.Fatal(err)
	}
	resetPeakRSS(t)
	began = time.Now()
	rep, err := CopySQLite(ctx, dst, src)
	took := time.Since(began)
	if err != nil {
		t.Fatal(err)
	}
	peak := peakRSS(t)
	var wal int64
	if err := scanOne(ctx, dst.w, `SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), $1::pg_lsn)::bigint`, []any{&wal}, lsn0); err != nil {
		t.Fatal(err)
	}
	var rows int64
	for _, tb := range rep.Tables {
		rows += tb.Rows
	}
	t.Logf("measure %s: copied %d rows in %s (analyze included); peak RSS %d MB; WAL written %d MB", shape, rows,
		took.Round(100*time.Millisecond), peak>>20, wal>>20)
}

// resetPeakRSS starts the process's peak RSS (VmHWM) again from its current RSS.
func resetPeakRSS(t *testing.T) {
	t.Helper()
	if err := os.WriteFile("/proc/self/clear_refs", []byte("5"), 0); err != nil {
		t.Logf("the peak RSS is the process's whole run's: %v", err)
	}
}

// peakRSS is the process's peak RSS since resetPeakRSS, in bytes.
func peakRSS(t *testing.T) int64 {
	t.Helper()
	f, err := os.Open("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if v, ok := strings.CutPrefix(s.Text(), "VmHWM:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return kb << 10
		}
	}
	t.Fatal("no VmHWM in /proc/self/status")
	return 0
}
