//go:build !server

package app

import (
	"context"
	"errors"
	"io"

	"github.com/yongjohnlee80/golib/logger"
)

// errNoServer is --server on a build without the enterprise server: one built without the server
// tag, which leaves out Postgres and the enterprise packages.
var errNoServer = errors.New("this autodoc was built without the enterprise server: `make build-server` builds it, " +
	"and each release has a linux-<arch>-server archive (docs/enterprise/operations.md)")

func serverMode(_ context.Context, _ string, _, _ bool, _ logger.Severity, _, _ io.Writer, _ string, failed func(error, int) int) int {
	return failed(errNoServer, 1)
}
