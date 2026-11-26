package plugin

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// The registration's names are every system's: a plugin's code that handles AddBuffer's answers
// builds wherever the SDK does, though only Linux registers a buffer. This cell has no build tag,
// so each system's vet compiles it.
func TestTheRegistrationsNamesAreEverySystems(t *testing.T) {
	var s *Surface
	answers := func(ctx context.Context, d SurfaceDMABuf) error {
		if _, err := s.AddBuffer(ctx, d); err != nil {
			return err
		}
		_, err := s.AddedBuffer(ctx)
		return err
	}
	_ = answers
	err := fmt.Errorf("registering: %w", &SurfaceRefused{Reason: "budget"})
	var refused *SurfaceRefused
	if !errors.As(err, &refused) || refused.Reason != "budget" {
		t.Errorf("a wrapped refusal: %v", err)
	}
	for _, e := range []error{ErrSurfaceRetired, ErrSurfaceKind, ErrSurfaceBuffers, ErrSurfaceClosed} {
		if e == nil || errors.As(e, &refused) {
			t.Errorf("%v: want an error that is no refusal", e)
		}
	}
	if MaxAddedBuffers != 3 {
		t.Errorf("MaxAddedBuffers is %d", MaxAddedBuffers)
	}
}
