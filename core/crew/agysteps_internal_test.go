package crew

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// TestAgyStepsBoundIsTheWholeAnswers: the bound is on the whole answer, not on where the decoder
// stopped: an answer whose object is complete within the bound but which goes on past it is
// refused, whatever follows; one that ends exactly at the bound, or within it, is read.
func TestAgyStepsBoundIsTheWholeAnswers(t *testing.T) {
	body := `{"steps":[{"status":"CORTEX_STEP_STATUS_WAITING"}]}`
	limit := int64(len(body) + 8)
	for name, c := range map[string]struct {
		answer string
		ok     bool
	}{
		"within the bound":                   {body, true},
		"padded to exactly the bound":        {body + strings.Repeat(" ", 8), true},
		"padded past the bound, then a byte": {body + strings.Repeat(" ", 8) + "x", false},
		"padded past the bound with spaces":  {body + strings.Repeat(" ", 20), false},
		"cut by the bound inside the object": {`{"steps":[{"status":"` + strings.Repeat("A", int(limit)) + `"}]}`, false},
	} {
		n, last, err := agyStepsWithin(strings.NewReader(c.answer), limit)
		if c.ok && (err != nil || n != 1 || last.Status != "CORTEX_STEP_STATUS_WAITING") {
			t.Errorf("%s: %d steps, last %q, %v; want the one waiting step", name, n, last.Status, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: %d steps, no error; want the answer refused", name, n)
		}
	}
}

// failingAfter gives r's bytes, then err in place of io.EOF: a body cut short of its declared length,
// or a connection that broke.
type failingAfter struct {
	r   io.Reader
	err error
}

func (f failingAfter) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, f.err
	}
	return n, err
}

// TestAgyStepsAtTheBoundNeedsTheBodysOwnEnd: an answer that fills the bound exactly is whole only
// if the body then ends; a read error there (cut short of its length, a reset) refuses it, as one
// before the bound would.
func TestAgyStepsAtTheBoundNeedsTheBodysOwnEnd(t *testing.T) {
	body := `{"steps":[{"status":"CORTEX_STEP_STATUS_WAITING"}]}` + "  "
	limit := int64(len(body))
	if _, _, err := agyStepsWithin(failingAfter{strings.NewReader(body), io.EOF}, limit); err != nil {
		t.Errorf("an answer that ends at the bound: %v; want it read", err)
	}
	for _, cut := range []error{io.ErrUnexpectedEOF, errors.New("connection reset by peer")} {
		if n, _, err := agyStepsWithin(failingAfter{strings.NewReader(body), cut}, limit); err == nil {
			t.Errorf("an answer at the bound, then %v: %d steps, no error; want it refused", cut, n)
		}
	}
}
