// Command autodoc is AutoDoc's one binary, with its modes as flags (ADR 0203 §4.1). --serve is the
// daemon; --ui is the TUI, its client, which starts the daemon when nothing answers:
//
//	autodoc --ui [--dev dir] [workspace]
//
// opens the named workspace, or the one the TUI last used. The Web-UI (--web-ui) follows.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// version is stamped at build time (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	serve := flag.Bool("serve", false, "run the daemon")
	ui := flag.Bool("ui", false, "run the TUI (starts --serve when nothing answers); a workspace name after the flags opens it")
	dev := flag.String("dev", "", "--ui: read the TUI's QML from this directory, and follow edits to it")
	configPath := flag.String("config", "", "config file (default $XDG_CONFIG_HOME/autodoc/config.toml)")
	showVersion := flag.Bool("version", false, "print the version")
	flag.Parse()
	if flag.NArg() > 0 && !(*ui && flag.NArg() == 1) {
		fmt.Fprintln(os.Stderr, "autodoc: unexpected arguments:", flag.Args(), "(only --ui takes one: a workspace's name, after the flags)")
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
