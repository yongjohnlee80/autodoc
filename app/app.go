// Package app is AutoDoc's one binary, with its modes as flags (ADR 0203 §4.1), as a function a
// main calls (ADR 0216 §1.1). --serve is the daemon; --ui is the TUI, its client, which starts the
// daemon when nothing answers:
//
//	autodoc --ui [--dev dir] [workspace]
//
// opens the named workspace, or the one the TUI last used. --call is one verb, as JSON, for a shell
// or an AI agent (AGENTS.md):
//
//	autodoc --call search.query '["kb", "a query", {"limit": 5}]'
//
// The Web-UI (--web-ui) follows.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Options is what a main hands Main: what makes its build its own.
type Options struct {
	// Version is the build's, stamped in its main package: --version prints it, the daemon reports
	// it, and the TUI compares it with the installed binary's, so it must be real.
	Version string
}

// Main runs autodoc as its CLI does and returns the process's exit code: 2 for usage, 1 for an
// error. It parses args (without the program's name) with its own FlagSet and never calls os.Exit.
func Main(ctx context.Context, args []string, o Options) int {
	return run(ctx, args, o, os.Stdout, os.Stderr)
}

func run(ctx context.Context, args []string, o Options, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("autodoc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	serve := fs.Bool("serve", false, "run the daemon")
	ui := fs.Bool("ui", false, "run the TUI (starts --serve when nothing answers); a workspace name after the flags opens it")
	dev := fs.String("dev", "", "--ui: read the TUI's QML from this directory, and follow edits to it")
	configPath := fs.String("config", "", "config file (default $XDG_CONFIG_HOME/autodoc/config.toml)")
	showVersion := fs.Bool("version", false, "print the version")
	call := fs.String("call", "", `call a daemon verb and print its result as JSON; its parameters, a JSON array, after the flags (AGENTS.md): --call search.query '["kb", "a query"]'`)
	exportFormat := fs.String("export", "", "export a Markdown file as html or text")
	exportOutput := fs.String("output", "", "--export: destination file (required)")
	exportTheme := fs.String("theme", "light", "--export html: light, dark, sepia, retro or mono")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 && !((*ui || *call != "" || *exportFormat != "") && fs.NArg() == 1) {
		fmt.Fprintln(stderr, "autodoc: unexpected arguments:", fs.Args(), "(only --ui, --call and --export take one argument)")
		return 2
	}
	switch {
	case *showVersion:
		fmt.Fprintln(stdout, "autodoc", o.Version)
	case *serve:
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := runServe(ctx, *configPath, stderr, o); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
	case *call != "":
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return callMain(ctx, *configPath, *call, fs.Arg(0), stdout, stderr)
	case *exportFormat != "":
		if err := exportMain(fs.Arg(0), *exportFormat, *exportOutput, *exportTheme); err != nil {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
	case *ui:
		// SIGTERM only: the terminal's own keys (ctrl-c among them) are the TUI's
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM)
		defer stop()
		if err := runUI(ctx, *configPath, *dev, fs.Arg(0), o); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
	default:
		fs.Usage()
		return 2
	}
	return 0
}
