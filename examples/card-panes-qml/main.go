// card-panes-qml is AutoDoc's QML example of panes (plugin/decl's Panes and Pane): a reading list
// beside a status pane, run inside the plugin, its frames sent to AutoDoc. Install it as card-go
// is; see plugin.toml.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/autodoc/plugin/decl"
	gdecl "github.com/yongjohnlee80/golib/decl"
	"github.com/yongjohnlee80/golib/parse/qml"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/controls"
)

//go:embed card.qml
var layout []byte

// newReadingList is the card: the layout, the App singleton it reads, and the handler it calls.
// Panes and Pane need no option: every decl card's program has them.
func newReadingList() *decl.Card {
	var c *decl.Card
	books := []string{"Dune", "Emma"}
	add := func(args []qml.SpecValue) error {
		title, err := tuidecl.Arg(args, 0)
		if err != nil {
			return err
		}
		if title = strings.TrimSpace(title); title == "" {
			return nil
		}
		books = append(books, title)
		// a handler runs on the program's loop, where its state may change
		return c.Program().SetMany(map[string]any{
			"App.books":  strings.Join(books, ", "),
			"App.status": fmt.Sprintf("%d books", len(books)), // the status pane is narrow
		})
	}
	c = decl.New(decl.Options{Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", layout),
		tuidecl.Singleton("reading", "1.0", "App"),
		tuidecl.Sources(map[string]any{"App.books": strings.Join(books, ", "), "App.status": fmt.Sprintf("%d books", len(books))}),
		tuidecl.Types(controls.Types()...),
		tuidecl.Handlers(map[string]gdecl.HandlerFunc{"App.add": add}),
	}})
	return c
}

func main() {
	if err := plugin.Serve(context.Background(), newReadingList()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
