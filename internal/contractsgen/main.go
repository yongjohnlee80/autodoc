// Command contractsgen writes AGENTS.md's verb and error tables from the contracts this build
// serves (app.Contracts): only the text between the markers, so the guide around them stays
// hand-written. go generate ./rpc runs it; TestAgentsMDIsGenerated fails when the file differs.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/yongjohnlee80/autodoc/app"
)

const (
	start = "<!-- autodoc:contracts:start -->"
	end   = "<!-- autodoc:contracts:end -->"
	note  = "<!-- generated from the verb contracts by go generate ./rpc; autodoc --contracts --format md prints the same. Do not edit. -->"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "contractsgen:", err)
		os.Exit(1)
	}
}

// run rewrites the file -agents names.
func run(args []string) error {
	fs := flag.NewFlagSet("contractsgen", flag.ContinueOnError)
	agents := fs.String("agents", "AGENTS.md", "the file whose tables to rewrite")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return rewrite(*agents, app.Contracts("").Markdown())
}

// rewrite puts tables between path's markers.
func rewrite(path, tables string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := splice(string(b), tables)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if out == string(b) {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

// splice is doc with the text between its markers replaced by tables.
func splice(doc, tables string) (string, error) {
	i, j := strings.Index(doc, start), strings.Index(doc, end)
	if i < 0 || j < i {
		return "", errors.New("no " + start + " … " + end + " block")
	}
	return doc[:i+len(start)] + "\n" + note + "\n\n" + tables + "\n" + doc[j:], nil
}
