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

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// Options is what a main hands Main: what makes its build its own.
type Options struct {
	// Version is the build's, stamped in its main package: --version prints it, the daemon reports
	// it, and the TUI compares it with the installed binary's, so it must be real.
	Version string
	// Chunkers are the build's own chunkers, by lower-case extension with its dot (".go"): a file
	// of one is indexed by it, as kind.Registered (ADR 0216 §1.2-1.3). Main refuses a malformed or
	// built-in extension, a Pro format's, and a version outside document.indexer's charset. nil:
	// none, the community build.
	Chunkers map[string]search.Chunker
	// Deriver makes text of the build's Pro document formats (ADR 0216 §1.8), its formats and their
	// identities read once, at entry: a file of one is indexed from its derived text, and doc.read
	// serves that text, read-only. nil: none.
	Deriver Deriver
	// Rank is the build's search ranking (ADR 0215 §3): its own ranker, the texts the ranker reads,
	// and whether a search answers ranked or not at all. The zero value: the stored ranker models,
	// over the chunks' texts, falling back to recall order.
	Rank Rank
}

// Rank is a build's search ranking.
type Rank struct {
	// Ranker is the build's own ranker: probed at startup, then the only one in use for the
	// daemon's life, and not persisted; the stored selection is kept, unchangeable, for a build
	// that does not supply one. nil: the stored ranker models.
	Ranker rank.Ranker
	// Window is how many candidates Ranker ranks, 10 to 100; 0: the default (40). It needs Ranker:
	// the stored models' window is the preference ranker.window.
	Window int
	// Texts are the texts the ranker reads for the hits, in any format. nil: each hit's chunk, its
	// breadcrumb then its body.
	Texts rank.TextSource
	// Required answers ranked or not at all: a worded search that asks for the ranker (or runs
	// every stage) and that the ranker cannot rank is refused, never answered in recall order. A
	// search that leaves the ranker out is answered, unranked.
	Required bool
}

// check refuses a Rank a build cannot mean: a window outside the bounds, or one with no ranker.
func (r Rank) check() error {
	switch {
	case r.Window != 0 && r.Ranker == nil:
		return errors.New("Options.Rank.Window needs Options.Rank.Ranker: the stored rankers' window is the preference ranker.window")
	case r.Window != 0 && (r.Window < rank.MinWindow || r.Window > rank.MaxWindow):
		return fmt.Errorf("Options.Rank.Window %d is outside %d to %d", r.Window, rank.MinWindow, rank.MaxWindow)
	}
	return nil
}

// Deriver and Derived are a build's text derivation (core/registrations).
type (
	Deriver = registrations.Deriver
	Derived = registrations.Derived
)

// build is a build as the modes run it: Options validated and frozen once, at entry.
type build struct {
	version string
	reg     *registrations.Table
	rank    Rank
}

// Main runs autodoc as its CLI does and returns the process's exit code: 2 for usage, 1 for an
// error. It parses args (without the program's name) with its own FlagSet and never calls os.Exit.
func Main(ctx context.Context, args []string, o Options) int {
	return run(ctx, args, o, os.Stdout, os.Stderr)
}

func run(ctx context.Context, args []string, o Options, stdout, stderr io.Writer) int {
	reg, err := registrations.New(o.Chunkers, o.Deriver)
	if err == nil {
		err = o.Rank.check()
	}
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	b := build{version: o.Version, reg: reg, rank: o.Rank}
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
	exportBase := fs.String("base", "", "--export html: the directory relative links are read from, written as file: URLs (default the source's directory)")
	printEndpoint := fs.Bool("print-endpoint", false, "print the daemon's endpoint as one line, unix<TAB><socket>: the config's, or the one serving its store")
	ensure := fs.Bool("ensure", false, "--print-endpoint: start the daemon first when nothing answers")
	restart := fs.Bool("restart", false, "--print-endpoint: stop the daemon serving this store and start this build in its place")
	compact := fs.Bool("compact", false, "with the daemon stopped: reclaim unused models' vectors and rewrite the store (VACUUM), after a free-space check")
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
	if (*ensure || *restart) && !*printEndpoint {
		fmt.Fprintln(stderr, "autodoc: --ensure and --restart go with --print-endpoint")
		return 2
	}
	if *exportFormat == "" {
		stray := ""
		fs.Visit(func(f *flag.Flag) {
			if stray == "" && (f.Name == "output" || f.Name == "theme" || f.Name == "base") {
				stray = f.Name
			}
		})
		if stray != "" {
			fmt.Fprintf(stderr, "autodoc: --%s is for --export\n", stray)
			return 2
		}
	}
	switch {
	case *showVersion:
		fmt.Fprintln(stdout, "autodoc", b.version)
	case *compact:
		if err := runCompact(ctx, *configPath, stdout); err != nil {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
	case *printEndpoint:
		run := func() error { return runPrintEndpoint(ctx, *configPath, *ensure, stdout) }
		if *restart {
			run = func() error { return runRestart(ctx, *configPath, stdout) }
		}
		if err := run(); err != nil {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
	case *serve:
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := runServe(ctx, *configPath, stderr, b); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "autodoc:", err)
			if errors.Is(err, errAlreadyServing) {
				return 0 // this store's daemon is serving: nothing is wrong
			}
			return 1
		}
	case *call != "":
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return callMain(ctx, *configPath, *call, fs.Arg(0), stdout, stderr)
	case *exportFormat != "":
		if err := exportMain(fs.Arg(0), *exportFormat, *exportOutput, *exportTheme, *exportBase); err != nil {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
	case *ui:
		// SIGTERM only: the terminal's own keys (ctrl-c among them) are the TUI's
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM)
		defer stop()
		if err := runUI(ctx, *configPath, *dev, fs.Arg(0), b); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
	default:
		fs.Usage()
		return 2
	}
	return 0
}
