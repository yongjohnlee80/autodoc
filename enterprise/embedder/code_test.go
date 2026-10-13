package embedder

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/yongjohnlee80/golib/search/embed"

	"github.com/yongjohnlee80/autodoc/enterprise/opcode"
)

// sqlErr is a database error, as pgx's carries its SQLSTATE.
type sqlErr struct{}

func (sqlErr) Error() string    { return "ERROR: something (SQLSTATE 57P01)" }
func (sqlErr) SQLState() string { return "57P01" }

// A worker's failure is coded by its kind: the provider's three, the index's database (a SQLSTATE,
// or the network under it), a call's own deadline or cancellation as other, anything else other.
func TestAFailureIsCodedByItsKind(t *testing.T) {
	for _, c := range []struct {
		err  error
		want opcode.Code
	}{
		{fmt.Errorf("x: %w", embed.ErrUnauthorized), opcode.ProviderRefused},
		{fmt.Errorf("x: %w", embed.ErrUnreachable), opcode.ProviderUnreachable},
		{fmt.Errorf("x: %w", embed.ErrRejected), opcode.ProviderRejected},
		{fmt.Errorf("pgstore: embed: %w", sqlErr{}), opcode.Database},
		{fmt.Errorf("pgstore: %w", &net.OpError{Op: "dial", Err: errors.New("refused")}), opcode.Database},
		{context.DeadlineExceeded, opcode.Other},
		{context.Canceled, opcode.Other},
		{errors.New("something else"), opcode.Other},
	} {
		if got := providerCode(c.err); got != c.want {
			t.Errorf("%v: %q, want %q", c.err, got, c.want)
		}
	}
}
