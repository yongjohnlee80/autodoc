package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/internal/client"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
)

// restartClientName is the name a restart's hello gives: not the TUI's, which a daemon admits only
// at its own protocol, while this one declares whatever protocol the daemon speaks.
const restartClientName = "autodoc-restart"

// restartExitWait bounds waiting for the stopped daemon's process to go before this build starts.
const restartExitWait = client.RestartWait

// restarter is a restart's moving parts; the tests replace the spawn and the wait.
type restarter struct {
	spawn    func() (string, error)
	waitGone func(ctx context.Context, pid int64) bool
}

// runRestart is --print-endpoint --restart: the daemon serving this config's store stops, and this
// build starts in its place, then prints its endpoint as --print-endpoint does. AutoVim's
// maintenance and its protocol-mismatch offer run it, so the plugin never stops or spawns a daemon
// itself. Whatever protocol the running daemon speaks, it is stopped over a connection that
// declares that protocol, with sys.hello and sys.shutdown alone (frozen across protocols), as the
// TUI's restart stops an older backend. A handoff holds the other clients back from spawning
// meanwhile, so the build that serves next is this one. With nothing serving, it only starts.
func runRestart(ctx context.Context, configPath string, out io.Writer) error {
	e, err := endpoint.Load(configPath)
	if err != nil {
		return err
	}
	r := restarter{
		spawn:    func() (string, error) { return spawnFor(e) },
		waitGone: func(ctx context.Context, pid int64) bool { return client.WaitGone(ctx, pid, restartExitWait) },
	}
	return r.run(ctx, e, out)
}

func (r restarter) run(ctx context.Context, e *endpoint.Endpoints, out io.Writer) error {
	// the handoff below is written in the state directory
	if err := e.MakeStateDir(); err != nil {
		return err
	}
	p, err := e.Paths()
	if err != nil {
		return err
	}
	session := client.NewSession(p.Socket, r.spawn).UseHandoffs(p.StateDir).ForStore(p.Socket, p.Store)
	defer session.Close()

	addr := endpoint.Holder(ctx, p.Store, p.Socket)
	info, perr := endpoint.Probe(ctx, addr, p.Store)
	var mismatch *client.StoreMismatchError
	if errors.As(perr, &mismatch) {
		return perr
	}
	if perr == nil {
		// until this build answers, another client only dials; a handoff that cannot be written
		// fails open, as the TUI's does
		_ = session.WriteHandoff()
		defer session.RemoveHandoff()
		if err := stopDaemon(ctx, addr, info.Protocol); err != nil {
			return fmt.Errorf("restart: stopping autodoc %s at %s: %w", info.Version, addr, err)
		}
		if info.PID > 0 && !r.waitGone(ctx, info.PID) {
			return fmt.Errorf("restart: autodoc %s (pid %d) has not stopped after %s", info.Version, info.PID, restartExitWait)
		}
	}
	if err := session.Connect(ctx); err != nil {
		var mismatch *client.MismatchError
		if errors.As(err, &mismatch) {
			return fmt.Errorf("restart: the daemon that answers is not this build: %w", err)
		}
		return err
	}
	_, err = fmt.Fprintf(out, "unix\t%s\n", session.Address())
	return err
}

// spawnFor starts e's daemon, as SpawnServe does, in e's state directory.
func spawnFor(e *endpoint.Endpoints) (string, error) {
	dir, err := e.StateDir()
	if err != nil {
		return "", err
	}
	return client.SpawnServe(e.ConfigPath, dir)
}

// stopDaemon asks the daemon at addr to shut down, over a connection declaring its own protocol.
func stopDaemon(ctx context.Context, addr string, protocol int64) error {
	cli, err := golibrpc.Dial(ctx, addr, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		return err
	}
	defer cli.Close()
	if _, err := cli.Call(ctx, "sys.hello", map[string]any{"protocol": protocol, "name": restartClientName}); err != nil {
		return fmt.Errorf("hello at protocol %d: %w", protocol, err)
	}
	_, err = cli.Call(ctx, "sys.shutdown")
	return err
}
