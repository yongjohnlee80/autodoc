package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/yongjohnlee80/autodoc/internal/client"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// workspacesCheck is --check-workspaces' report: every workspace whose root holds one of AutoDoc's
// own folders or lies inside one (docs/decisions/contract.md#a-root-holding-autodocs-own-folders).
type workspacesCheck struct {
	OK         bool               `json:"ok"`
	Workspaces []flaggedWorkspace `json:"workspaces"`
}

// flaggedWorkspace is one workspace of the report: its name, root, and the folders, as
// workspace.list names them.
type flaggedWorkspace struct {
	Name        string           `json:"name"`
	Root        string           `json:"root"`
	AutodocDirs []map[string]any `json:"autodoc_dirs"`
}

// errNoDaemon is --check-workspaces with no daemon serving the store: the daemon names the folders,
// so there is nothing to check against; it is never started for a check.
var errNoDaemon = errors.New("no daemon serves this store: start one (autodoc --print-endpoint --ensure), then check again")

// errWorkspacesFlagged is a check that found a workspace holding or inside AutoDoc's own folders:
// the report names them.
var errWorkspacesFlagged = errors.New("a workspace's root holds or lies inside AutoDoc's own folders: move the root, or narrow it")

// runCheckWorkspaces is --check-workspaces, --check-store's twin for the workspaces: the daemon
// serving the store lists them (workspace.list), and each one whose root holds AutoDoc's config,
// state or data folder, or lies inside one, is reported, as JSON. errWorkspacesFlagged when one is,
// after its report; errNoDaemon when no daemon serves the store.
func runCheckWorkspaces(ctx context.Context, configPath string, out io.Writer) error {
	e, err := endpoint.Load(configPath)
	if err != nil {
		return err
	}
	p, err := e.Paths()
	if err != nil {
		return err
	}
	addr, ok := endpoint.Verified(ctx, p.Store) // never started for a check: the one serving the store, or none
	if !ok {
		return errNoDaemon
	}
	session := client.NewSession(addr, nil).As(rpc.CLIName).WithInfo(cliWho()).ForStore(p.Socket, p.Store)
	if err := session.Connect(ctx); err != nil {
		return fmt.Errorf("%w (%v)", errNoDaemon, err)
	}
	defer session.Close()
	res, err := session.Call(ctx, "workspace.list")
	if err != nil {
		return err
	}
	report := workspacesCheck{Workspaces: []flaggedWorkspace{}}
	for _, w := range asWorkspaces(jsonOf(res)) {
		if len(w.AutodocDirs) > 0 {
			report.Workspaces = append(report.Workspaces, w)
		}
	}
	report.OK = len(report.Workspaces) == 0
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(report); err != nil {
		return err
	}
	if !report.OK {
		return errWorkspacesFlagged
	}
	return nil
}

// asWorkspaces reads workspace.list's answer (as jsonOf gives it) into the report's rows.
func asWorkspaces(v any) []flaggedWorkspace {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []flaggedWorkspace
	_ = json.Unmarshal(b, &out)
	return out
}
