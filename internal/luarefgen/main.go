// Command luarefgen writes the Lua reference's tables that the script engine documents itself
// (script.EditorDocs, script.TextDocs): docs/lua/reference.md's editor-object and text tables,
// only the text between each block's markers, so the guide around them stays hand-written. go
// generate ./tui runs it; TestTheLuaReferenceIsGenerated fails when the file differs.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/yongjohnlee80/autodoc/script"
)

// global is the table AutoDoc's scripts reach it through (tui/scripts.go).
const global = "autodoc"

const note = "<!-- generated from the script package's own tables by go generate ./tui. Do not edit. -->"

// block is one generated table of the reference: its markers' name and its text.
type block struct{ name, text string }

// blocks are the reference's generated tables.
func blocks() []block {
	return []block{
		{"ed", table("Method", script.EditorDocs())},
		{"text", table("Function", script.TextDocs(global))},
	}
}

func markers(name string) (start, end string) {
	return "<!-- autodoc:lua-" + name + ":start -->", "<!-- autodoc:lua-" + name + ":end -->"
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "luarefgen:", err)
		os.Exit(1)
	}
}

// run rewrites the file -ref names.
func run(args []string) error {
	fs := flag.NewFlagSet("luarefgen", flag.ContinueOnError)
	ref := fs.String("ref", "docs/lua/reference.md", "the reference whose tables to rewrite")
	if err := fs.Parse(args); err != nil {
		return err
	}
	b, err := os.ReadFile(*ref)
	if err != nil {
		return err
	}
	out, err := generate(string(b))
	if err != nil {
		return fmt.Errorf("%s: %w", *ref, err)
	}
	if out == string(b) {
		return nil
	}
	return os.WriteFile(*ref, []byte(out), 0o644)
}

// generate is doc with every block's table put between its markers.
func generate(doc string) (string, error) {
	for _, b := range blocks() {
		var err error
		if doc, err = splice(doc, b.name, b.text); err != nil {
			return "", err
		}
	}
	return doc, nil
}

// splice is doc with the text between name's markers replaced by text.
func splice(doc, name, text string) (string, error) {
	start, end := markers(name)
	i, j := strings.Index(doc, start), strings.Index(doc, end)
	if i < 0 || j < i {
		return "", errors.New("no " + start + " … " + end + " block")
	}
	return doc[:i+len(start)] + "\n" + note + "\n\n" + text + "\n" + doc[j:], nil
}

// table is docs as a Markdown table, its first column headed first.
func table(first string, docs []script.Doc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "| %s | Returns | Notes |\n|---|---|---|\n", first)
	for _, d := range docs {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", d.Call, d.Returns, d.Notes)
	}
	return b.String()
}
