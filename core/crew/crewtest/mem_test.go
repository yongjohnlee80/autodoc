package crewtest_test

import (
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
)

// TestMemConforms: the in-memory Store keeps the crew state machines as every engine must.
func TestMemConforms(t *testing.T) {
	crewtest.Run(t, crewtest.Harness{New: func(_ *testing.T, now func() time.Time) crew.Store {
		return crewtest.NewMem(now)
	}})
}
