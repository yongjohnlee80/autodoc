// Package deployments is the store's schema as SQL scripts, one directory per
// engine, compiled into the binary (docs/ops/schema-scripts.md).
//
//	sqlite/000001_update_initialize_tables.sql   the baseline: no revert
//	sqlite/000002_update_<slug>.sql              a change …
//	sqlite/000002_revert_<slug>.sql              … and its undo
//
// A second engine is a second directory holding the same names, so every
// engine's ledger names the same scripts; a script with no work on one engine
// is a comment saying why. Numbers are dense and never reused. A released
// script never changes: its digest is recorded when it is applied, and a test
// holds the list of released digests.
package deployments

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"

	gsql "github.com/yongjohnlee80/golib/parse/sql"
)

//go:embed sqlite/*.sql
var files embed.FS

// Engine names an engine; its scripts are in the directory of that name.
type Engine string

const SQLite Engine = "sqlite"

// Engines are the engines the store has scripts for.
var Engines = []Engine{SQLite}

// lexer is how the engine's SQL splits into statements: for SQLite, a
// trigger's BEGIN … END body is part of its CREATE TRIGGER. An engine with
// other constructs (dollar-quoted bodies, nested comments) sets them here.
func (e Engine) lexer() gsql.SQL { return gsql.SQL{TriggerBodies: true} }

// Kind is what a script does.
type Kind string

const (
	Update Kind = "update"
	Revert Kind = "revert"
)

// Script is one file.
type Script struct {
	Number int
	Kind   Kind
	Slug   string
	// Name is the file name, which the ledger records: 000001_update_initialize_tables.sql.
	Name string
	// Body is the file as written; SHA256 its digest, hex.
	Body   []byte
	SHA256 string
	engine Engine
}

// Statement is one statement of a script, and where it starts in the file.
type Statement struct {
	Text string
	Line int
}

var nameRE = regexp.MustCompile(`^(\d{6})_(update|revert)_([a-z0-9_]+)\.sql$`)

// Scripts are an engine's scripts, updates and reverts, by number then kind
// (update first). A file not named NNNNNN_(update|revert)_<slug>.sql is an
// error: a script the runner could not place would silently never run.
func Scripts(eng Engine) ([]Script, error) { return scriptsIn(files, eng) }

// scriptsIn is Scripts over any file tree laid out as the embedded one is.
func scriptsIn(fsys fs.FS, eng Engine) ([]Script, error) {
	dir := string(eng)
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("deployments: no scripts for engine %q: %w", dir, err)
	}
	var out []Script
	for _, e := range entries {
		m := nameRE.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("deployments: %s/%s is not named NNNNNN_(update|revert)_<slug>.sql", dir, e.Name())
		}
		n, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		out = append(out, Script{Number: n, Kind: Kind(m[2]), Slug: m[3], Name: e.Name(),
			Body: body, SHA256: hex.EncodeToString(sum[:]), engine: eng})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Number != out[j].Number {
			return out[i].Number < out[j].Number
		}
		return out[i].Kind == Update && out[j].Kind == Revert
	})
	return out, nil
}

// Updates are an engine's update scripts, in the order they apply.
func Updates(eng Engine) ([]Script, error) { return ofKind(files, eng, Update) }

// RevertOf is the revert for update script number n, if it has one; the
// baseline, 000001, never does.
func RevertOf(eng Engine, n int) (Script, bool, error) { return revertIn(files, eng, n) }

func revertIn(fsys fs.FS, eng Engine, n int) (Script, bool, error) {
	reverts, err := ofKind(fsys, eng, Revert)
	if err != nil {
		return Script{}, false, err
	}
	for _, s := range reverts {
		if s.Number == n {
			return s, true, nil
		}
	}
	return Script{}, false, nil
}

func ofKind(fsys fs.FS, eng Engine, k Kind) ([]Script, error) {
	all, err := scriptsIn(fsys, eng)
	if err != nil {
		return nil, err
	}
	var out []Script
	for _, s := range all {
		if s.Kind == k {
			out = append(out, s)
		}
	}
	return out, nil
}

// Statements splits the script into statements with its engine's lexical
// rules (a semicolon inside a string or a comment is not a boundary), each
// with the line it starts on, for an error that names where in the file it
// failed.
func (s Script) Statements() ([]Statement, error) {
	stmts, err := s.engine.lexer().Parse(s.Body)
	if err != nil {
		return nil, fmt.Errorf("deployments: %s/%s: %w", s.engine, s.Name, err)
	}
	// A comment never begins a statement, so a script that is only comments
	// (one with no work on this engine, saying why) runs nothing.
	out := make([]Statement, 0, len(stmts))
	for _, st := range stmts {
		out = append(out, Statement{Text: st.Text, Line: st.Pos.Line})
	}
	return out, nil
}
