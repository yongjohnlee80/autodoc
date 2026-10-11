package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/yongjohnlee80/autodoc/internal/client"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// runPrintEndpoint is --print-endpoint: one line, "unix\t<address>", for a client that dials the
// daemon itself (AutoVim's Lua) and must find it as this binary does, not by rules of its own: the
// config's socket, or the socket of the daemon already serving this config's store (its
// lease-info, confirmed by a probe). With ensure it first makes sure a daemon answers, starting one
// through the same Session.Connect the TUI and --call use, restart handoffs and all; a daemon of
// another protocol answers too, and the client's own hello finds the mismatch. A daemon that
// answers for another store is refused either way (client.StoreMismatchError).
func runPrintEndpoint(ctx context.Context, configPath string, ensure bool, out io.Writer) error {
	e, err := endpoint.Load(configPath)
	if err != nil {
		return err
	}
	addr := endpoint.Holder(ctx, e.Store, e.Socket)
	if !ensure {
		// what answers on the socket must be this store's daemon: a config that changed its
		// data_dir but kept its socket would otherwise send its client to the old store
		var mismatch *client.StoreMismatchError
		if _, err := endpoint.Probe(ctx, addr, e.Store); errors.As(err, &mismatch) {
			return err
		}
	}
	if ensure {
		session := client.NewSession(e.Socket, func() (string, error) { return client.SpawnServe(e.ConfigPath, e.StateDir) }).As(rpc.CLIName).
			UseHandoffs(e.StateDir).ForStore(e.Socket, e.Store)
		var mismatch *client.MismatchError
		if err := session.Connect(ctx); err != nil && !errors.As(err, &mismatch) && session.Stale() == nil {
			return err
		}
		addr = session.Address()
		session.Close()
	}
	_, err = fmt.Fprintf(out, "unix\t%s\n", addr)
	return err
}
