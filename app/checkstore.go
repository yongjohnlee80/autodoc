package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
)

// storeCheck is --check-store's report: the store, who checked it, and what was found.
type storeCheck struct {
	Store     string `json:"store"`
	CheckedBy string `json:"checked_by"` // "daemon" when the daemon serving the store checked it, else "this process"
	OK        bool   `json:"ok"`
	store.CheckReport
}

// errStoreProblems is a check that found problems: the report says which.
var errStoreProblems = errors.New("the store check found problems")

// runCheckStore is --check-store (AM-4): the store's whole check, written as JSON. With no daemon
// serving the store, this process opens it to be checked (store.OpenForCheck: no schema script runs
// and nothing is written, so a damaged store is still reported on); with one serving it, that daemon
// checks it (sys.check), so no window is stopped for it. A check that finds problems is
// errStoreProblems, after its report. PostgreSQL stores are their database server's to check.
func runCheckStore(ctx context.Context, configPath string, out io.Writer) error {
	e, err := endpoint.Load(configPath)
	if err != nil {
		return err
	}
	storePath, err := e.Store()
	if err != nil {
		return err
	}
	res := storeCheck{Store: storePath, CheckedBy: "this process"}
	db, err := store.OpenForCheck(ctx, storePath)
	switch {
	case errors.Is(err, store.ErrBusy):
		res.CheckedBy = "daemon"
		if res.CheckReport, err = checkThroughDaemon(ctx, configPath); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		res.CheckReport, err = db.Check(ctx)
		_ = db.Close()
		if err != nil {
			return err
		}
	}
	res.OK = res.CheckReport.OK()
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		return err
	}
	if !res.OK {
		return errStoreProblems
	}
	return nil
}

// checkThroughDaemon is sys.check on the daemon serving the store.
func checkThroughDaemon(ctx context.Context, configPath string) (store.CheckReport, error) {
	var buf bytes.Buffer
	var r store.CheckReport
	if err := runCall(ctx, configPath, "sys.check", "", &buf); err != nil {
		var ce *CallError
		if errors.As(err, &ce) && ce.Code == golibrpc.CodeMethodNotFound {
			return r, fmt.Errorf("the daemon serving the store is older than this autodoc and has no sys.check: stop it (--call sys.shutdown), then --check-store again")
		}
		return r, err
	}
	if err := json.Unmarshal(buf.Bytes(), &r); err != nil {
		return r, fmt.Errorf("sys.check's answer: %w", err)
	}
	return r, nil
}
