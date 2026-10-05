package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
	"github.com/yongjohnlee80/autodoc/rpc"
	"github.com/yongjohnlee80/autodoc/tui"
)

// runPrintEndpoint is --print-endpoint: one line, "unix\t<address>", for a client that dials the
// daemon itself (AutoVim's Lua) and must find it as this binary does, not by rules of its own: the
// config's socket, or the socket of the daemon already serving this config's store (its
// lease-info, confirmed by a probe). With ensure it first makes sure a daemon answers, starting one
// through the same Session.Connect the TUI and --call use, restart handoffs and all; a daemon of
// another protocol answers too, and the client's own hello finds the mismatch. A daemon that
// answers for another store is refused either way (tui.StoreMismatchError).
func runPrintEndpoint(ctx context.Context, configPath string, ensure bool, out io.Writer) error {
	if configPath == "" {
		var err error
		if configPath, err = config.DefaultPath(); err != nil {
			return err
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	sock, err := cfg.Server.SocketPath()
	if err != nil {
		return err
	}
	storePath, err := cfg.Server.StorePath()
	if err != nil {
		return err
	}
	addr := endpoint.Holder(ctx, storePath, sock)
	if !ensure {
		// what answers on the socket must be this store's daemon: a config that changed its
		// data_dir but kept its socket would otherwise send its client to the old store
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		info, perr := rpc.Probe(probeCtx, "unix", addr)
		cancel()
		if perr == nil && info.StoreID != "" {
			if wanted, _ := store.Identity(storePath); info.StoreID != wanted {
				return &tui.StoreMismatchError{Addr: addr, StorePath: storePath, Served: info.StoreID, Wanted: wanted}
			}
		}
	}
	if ensure {
		stateDir, err := cfg.Server.StateDirPath()
		if err != nil {
			return err
		}
		session := tui.NewSession(sock, func() (string, error) { return tui.SpawnServe(configPath, stateDir) }).
			UseHandoffs(stateDir).ForStore(sock, storePath)
		var mismatch *tui.MismatchError
		if err := session.Connect(ctx); err != nil && !errors.As(err, &mismatch) && session.Stale() == nil {
			return err
		}
		addr = session.Address()
		session.Close()
	}
	_, err = fmt.Fprintf(out, "unix\t%s\n", addr)
	return err
}
