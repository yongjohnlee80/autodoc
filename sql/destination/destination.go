// Package destination is the schema of a workspace's index in a destination database: SQL
// scripts, one directory per engine, compiled into the binary and applied by golib dao/deploy to
// the destination, with a ledger of their own (ADR 0214 §7).
//
//	postgres/000001_update_initialize_index.sql   the baseline: no revert
//
// The local store's scripts are sql/deployments; these are the tables a workspace's index needs
// when it lives elsewhere. The same rules hold: a released script never changes, and a set that is
// not dense and paired is refused.
package destination

import (
	"embed"
	"io/fs"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/deploy"
)

//go:embed postgres/*.sql
var files embed.FS

// Engines are the engines a destination can be: each is a directory, named for its dialect.
var Engines = []string{dao.DialectPostgres}

// Ledger is the table in the destination recording which scripts are applied. It is not the
// local store's: a destination may be shared, and its ledger is its own.
const Ledger = "autodoc_schema"

// FS is the script tree.
func FS() fs.FS { return files }

// Runner applies the destination's scripts.
func Runner() *deploy.Runner { return deploy.New(files, deploy.Ledger(Ledger)) }
