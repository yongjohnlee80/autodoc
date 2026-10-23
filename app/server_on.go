//go:build server

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"github.com/yongjohnlee80/autodoc/enterprise/config"
)

// serverMode is --server: the enterprise search server on the server.toml cfg names, or with
// health, a probe of the running one. A refused configuration exits 2, any other failure 1.
func serverMode(ctx context.Context, cfg string, check, health bool, stdout, stderr io.Writer, version string, failed func(error, int) int) int {
	if cfg == "" {
		fmt.Fprintln(stderr, "autodoc: --server needs its configuration: autodoc --server [--check | --health] server.toml")
		return 2
	}
	if health {
		return probeHealth(ctx, cfg, stdout, stderr)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err := runServer(ctx, cfg, check, stdout, stderr, version)
	switch {
	case err == nil || errors.Is(err, context.Canceled):
		return 0
	case config.IsRefusal(err):
		return failed(err, 2)
	}
	return failed(err, 1)
}
