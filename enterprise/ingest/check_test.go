package ingest

import (
	"errors"
	"strings"
	"testing"
)

// The order check confirms, on the live source, that resuming the view from any row's cursor
// returns the next row: across page boundaries and inside a run of tied timestamps. The sample
// view passes over more than a page of tied rows; a view ordered by its JSON column, by id, or by
// id descending within a timestamp, and one whose WHERE compares updated_at alone, are refused on
// "process". The mutants, named here: CheckOrder without its resume probes (the timestamp-only
// WHERE and the descending tie pass), and readPage without its order guard (the JSON and id
// orders pass the traversal).
func TestTheOrderCheckRefusesAViewNotOrderedByItsCursor(t *testing.T) {
	f := newFixture(t)
	for id := 11; id <= 18; id++ { // more than two pages sharing one updated_at
		f.track(id, "A", "tied", 5)
	}
	f.track(30, "A", "early", 1) // an id order that is not the time order
	f.track(2, "A", "late", 9)
	upto := at(100)

	rep, err := f.source(nil).CheckOrder(f.ctx, f.business, upto)
	if err != nil {
		t.Fatalf("the sample view: %v", err)
	}
	if rep.Rows != 10 || rep.Ties != 7 || rep.Boundaries == 0 || len(rep.Warnings) != 0 || rep.Capped {
		t.Errorf("the sample view's report: %+v", rep)
	}

	for name, edit := range map[string][2]string{
		"by its JSON column": {"ORDER BY GREATEST(t.updated_at, a.updated_at), t.id", "ORDER BY 1, t.id"},
		"by id":              {"ORDER BY GREATEST(t.updated_at, a.updated_at), t.id", "ORDER BY t.id"},
		"ids descending":     {"ORDER BY GREATEST(t.updated_at, a.updated_at), t.id", "ORDER BY GREATEST(t.updated_at, a.updated_at), t.id DESC"},
		"timestamp alone": {"WHERE (GREATEST(t.updated_at, a.updated_at), t.id) > ($1, $2)",
			"WHERE GREATEST(t.updated_at, a.updated_at) > $1 AND ($2::bigint IS NULL OR $2::bigint IS NOT NULL)"},
		"inclusive": {"> ($1, $2)", ">= ($1, $2)"},
	} {
		t.Run(name, func(t *testing.T) {
			src := f.source(func(v string) string { return strings.Replace(v, edit[0], edit[1], 1) })
			_, err := src.CheckOrder(f.ctx, f.business, upto)
			var ve *ViewError
			if !errors.As(err, &ve) || ve.Field != "process" {
				t.Fatalf("CheckOrder: %v, want a refusal on process", err)
			}
		})
	}
}

// With no tied timestamps the id order cannot be exercised, and with no rows nothing can: the
// check passes and says so. A row with no updated_at is refused: the cursor could not pass it.
func TestTheOrderCheckSaysWhatItCouldNotExercise(t *testing.T) {
	f := newFixture(t)
	src := f.source(nil)
	rep, err := src.CheckOrder(f.ctx, f.business, at(100))
	if err != nil || rep.Rows != 0 || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "no rows") {
		t.Errorf("an empty source: %+v, %v", rep, err)
	}
	f.track(1, "A", "one", 1)
	f.track(2, "A", "two", 2)
	f.track(3, "A", "three", 3)
	f.track(4, "A", "four", 4)
	rep, err = src.CheckOrder(f.ctx, f.business, at(100))
	if err != nil || rep.Rows != 4 || rep.Boundaries != 1 || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "share an updated_at") {
		t.Errorf("no ties: %+v, %v", rep, err)
	}
	keyless := f.source(func(v string) string {
		return strings.Replace(v, "'updated_at', GREATEST(t.updated_at, a.updated_at))", "'stamp', GREATEST(t.updated_at, a.updated_at))", 1)
	})
	var ve *ViewError
	if _, err := keyless.CheckOrder(f.ctx, f.business, at(100)); !errors.As(err, &ve) || !strings.Contains(err.Error(), "updated_at") {
		t.Errorf("rows with no updated_at: %v", err)
	}
}
