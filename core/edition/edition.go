package edition

import (
	"fmt"

	"github.com/yongjohnlee80/golib/errs"
)

// Databases offers a workspace's source and destination databases.
//
// TODO: on until the Community and Pro split is decided; then the Community build sets it false.
const Databases = true

// ErrDatabases is a database setting refused by a build without Databases; it is
// errs.ErrUnsupported.
var ErrDatabases = fmt.Errorf("%w: this edition has no database settings", errs.ErrUnsupported)
