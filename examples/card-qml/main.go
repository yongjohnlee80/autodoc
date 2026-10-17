// card-qml is AutoDoc's QML example (plugin/decl): the People card written as a golib QML layout,
// run inside the plugin, its frames sent to AutoDoc. Install it as card-go is; see plugin.toml.
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

// newCard is the People card: the layout, the App singleton it reads, and the one handler it calls.
func newCard() *decl.Card {
	var c *decl.Card
	names := []string{"Ann", "Bob"}
	add := func(args []qml.SpecValue) error {
		name, err := tuidecl.Arg(args, 0)
		if err != nil {
			return err
		}
		if name = strings.TrimSpace(name); name == "" {
			return nil
		}
		names = append(names, name)
		// a handler runs on the program's loop, where its state may change
		return c.Program().SetMany(map[string]any{
			"App.names":  strings.Join(names, ", "),
			"App.status": "added " + name,
		})
	}
	c = decl.New(decl.Options{Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", layout),
		tuidecl.Singleton("people", "1.0", "App"),
		tuidecl.Sources(map[string]any{"App.names": strings.Join(names, ", "), "App.status": ""}),
		tuidecl.Types(controls.Types()...),
		tuidecl.Handlers(map[string]gdecl.HandlerFunc{"App.add": add}),
	}})
	return c
}

func main() {
	if err := plugin.Serve(context.Background(), newCard()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
