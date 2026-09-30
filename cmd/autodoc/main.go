// Command autodoc is AutoDoc's one binary, with its modes as flags (ADR 0203 §4.1). --serve is the
// daemon; --ui is the TUI, its client, which starts the daemon when nothing answers:
//
//	autodoc --ui [--dev dir] [workspace]
//
// opens the named workspace, or the one the TUI last used. --call is one verb, as JSON, for a shell
// or an AI agent (AGENTS.md):
//
//	autodoc --call search.query '["kb", "a query", {"limit": 5}]'
//
// The Web-UI (--web-ui) follows.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
)

// version is stamped at build time (-ldflags "-X main.version=…"). Unstamped, as
// `go install github.com/yongjohnlee80/autodoc/cmd/autodoc@v0.1.0` builds it, it is the module's
// version the build recorded. The TUI compares it with the installed binary's, so it must be real.
var version = "dev"

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		version = moduleVersion(version, info)
	}
}

// moduleVersion is stamped, unless the build stamped nothing and recorded the module's version.
func moduleVersion(stamped string, info *debug.BuildInfo) string {
	if v := info.Main.Version; stamped == "dev" && v != "" && v != "(devel)" {
		return v
	}
	return stamped
}

func main() {
	serve := flag.Bool("serve", false, "run the daemon")
	ui := flag.Bool("ui", false, "run the TUI (starts --serve when nothing answers); a workspace name after the flags opens it")
	dev := flag.String("dev", "", "--ui: read the TUI's QML from this directory, and follow edits to it")
	configPath := flag.String("config", "", "config file (default $XDG_CONFIG_HOME/autodoc/config.toml)")
	showVersion := flag.Bool("version", false, "print the version")
	call := flag.String("call", "", `call a daemon verb and print its result as JSON; its parameters, a JSON array, after the flags (AGENTS.md): --call search.query '["kb", "a query"]'`)
	flag.Parse()
	if flag.NArg() > 0 && !((*ui || *call != "") && flag.NArg() == 1) {
		fmt.Fprintln(os.Stderr, "autodoc: unexpected arguments:", flag.Args(), "(only --ui and --call take one: a workspace's name; the call's parameters)")
		os.Exit(2)
	}
	switch {
	case *showVersion:
		fmt.Println("autodoc", version)
	case *serve:
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := runServe(ctx, *configPath, os.Stderr); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "autodoc:", err)
			stop()
			os.Exit(1)
		}
	case *call != "":
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		code := callMain(ctx, *configPath, *call, flag.Arg(0), os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	case *ui:
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()
		if err := runUI(ctx, *configPath, *dev, flag.Arg(0)); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "autodoc:", err)
			stop()
			os.Exit(1)
		}
	default:
		flag.Usage()
		os.Exit(2)
	}
}
