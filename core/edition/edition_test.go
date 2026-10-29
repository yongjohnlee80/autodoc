package edition_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/edition"
)

// ErrDatabases is the unsupported kind a caller tests for, and says what this edition lacks.
func TestErrDatabasesIsUnsupported(t *testing.T) {
	if !errors.Is(edition.ErrDatabases, errs.ErrUnsupported) {
		t.Errorf("ErrDatabases is not errs.ErrUnsupported: %v", edition.ErrDatabases)
	}
	if !strings.Contains(edition.ErrDatabases.Error(), "no database settings") {
		t.Errorf("ErrDatabases says %q", edition.ErrDatabases)
	}
}
