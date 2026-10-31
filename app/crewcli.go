package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/internal/client"
	"github.com/yongjohnlee80/autodoc/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// The agent's commands (docs/decisions/crew.md#the-session-context,
// docs/decisions/crew.md#the-agent-guide-and-commands): `autodoc crew …` and `autodoc mail …`, each
// one call to the daemon on the session context's credential, its answer printed as JSON. They are
// what the agent guide (crew.guide) tells an agent to run.

// crewContextDir is an agent folder's folder of session contexts (crew.ContextDir), one file per
// session, named by its id.
const crewContextDir = crew.ContextDir

// sessionContext is a session's context file, written by its host before the agent starts
// (docs/decisions/crew.md#the-session-context).
type sessionContext = crew.Context

var (
	errNoContext       = errors.New("no session context: neither $AUTODOC_CREW_CONTEXT nor ./" + crewContextDir + "/ names one; this process was not started as a crew agent")
	errSeveralContexts = errors.New("several session contexts in ./" + crewContextDir + "/ and no $AUTODOC_CREW_CONTEXT to say which is yours: run as the agent your host started, never with a guess")
	errContextMismatch = errors.New("the session contexts disagree: $AUTODOC_CREW_CONTEXT names a session ./" + crewContextDir + "/ does not hold; you may be running with another agent's context, so stop and tell the user")
)

// findSessionContext is the session context (docs/decisions/crew.md#the-session-context): the file
// $AUTODOC_CREW_CONTEXT names (envFile). Without one, the working directory's ./.autodoc-crew/ when
// it holds exactly one; several are refused, never one guessed. With both, a working directory
// whose folder holds contexts, none of them the env's session, is refused: the env may be another
// agent's.
func findSessionContext(envFile, cwd string) (sessionContext, error) {
	here, err := folderContexts(filepath.Join(cwd, crewContextDir))
	if err != nil {
		return sessionContext{}, err
	}
	if envFile != "" {
		c, err := readSessionContext(envFile)
		if err != nil {
			return sessionContext{}, fmt.Errorf("$AUTODOC_CREW_CONTEXT: %w", err)
		}
		if len(here) > 0 && !slices.Contains(here, c.Session) {
			return sessionContext{}, errContextMismatch
		}
		return c, nil
	}
	switch len(here) {
	case 0:
		return sessionContext{}, errNoContext
	case 1:
		return readSessionContext(filepath.Join(cwd, crewContextDir, here[0]))
	}
	return sessionContext{}, errSeveralContexts
}

// folderContexts is the sessions whose contexts dir holds: its regular files, a dot file (a write
// in progress) aside. None when dir is no folder.
func folderContexts(dir string) ([]string, error) {
	if fi, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) || (err == nil && !fi.IsDir()) {
		return nil, nil
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func readSessionContext(path string) (sessionContext, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return sessionContext{}, err
	}
	var c sessionContext
	if err := json.Unmarshal(b, &c); err != nil {
		return sessionContext{}, fmt.Errorf("%s: %w", path, err)
	}
	if c.Socket == "" || c.Credential == "" || c.Session == "" {
		return sessionContext{}, fmt.Errorf("%s: a session context needs its socket, session and credential", path)
	}
	return c, nil
}

// handle is a delivery's id as `mail receive` prints it: the delivery and the lease it holds,
// so ack, renew and reply need nothing kept between commands.
func handle(delivery, lease string) string { return delivery + ":" + lease }

func parseHandle(h string) (delivery, lease string, err error) {
	d, l, ok := strings.Cut(h, ":")
	if !ok || d == "" || l == "" {
		return "", "", fmt.Errorf("%q is not a delivery id as `autodoc mail receive` prints it", h)
	}
	return d, l, nil
}

// crewCommand is one `autodoc crew|mail` subcommand: its usage, and the call it makes from its
// flags and arguments.
type crewCommand struct {
	usage   string
	example string // one line an agent can run as it is, or with its own id and text
	call    func(c sessionContext, fs *flag.FlagSet, args []string, stdin io.Reader) (method string, params []any, shape func(any) any, err error)
	flags   func(fs *flag.FlagSet)
}

// crewCommands are the subcommands, by "domain verb".
var crewCommands = map[string]crewCommand{
	"crew whoami": {usage: "who this session is: its codename, scope, session and store",
		example: "autodoc crew whoami",
		call: func(c sessionContext, _ *flag.FlagSet, _ []string, _ io.Reader) (string, []any, func(any) any, error) {
			return "crew.whoami", []any{c.Credential, c.Store}, nil, nil
		}},
	"crew list": {usage: "the addressbook: every codename, its scope, presence and reachability",
		example: "autodoc crew list",
		call: func(sessionContext, *flag.FlagSet, []string, io.Reader) (string, []any, func(any) any, error) {
			return "crew.list", nil, nil, nil
		}},
	"crew endpoint": {usage: "--claude | --codex-thread ID | --codex-here [--since T] | --opencode SESSION | <kind> <locator JSON>: register how this session is woken (--claude: Claude Code's SessionStart hook runs it; --codex-thread: a Codex agent, with its $CODEX_THREAD_ID; --codex-here: this folder's Codex session, found on Codex's app-server, which AutoDoc runs as a Codex agent starts; --opencode: AutoDoc's OpenCode plugin runs it)",
		example: "autodoc crew endpoint --codex-thread \"$CODEX_THREAD_ID\"",
		flags: func(fs *flag.FlagSet) {
			fs.Bool("claude", false, "this Claude Code session's inbox socket, found from the hook's input and its process")
			fs.String("codex-thread", "", "this Codex session's thread ($CODEX_THREAD_ID), on the app-server its TUI is attached to")
			fs.Bool("codex-here", false, "this folder's Codex session: the newest thread on Codex's app-server whose folder is this one")
			fs.Int64("since", 0, "with --codex-here: a thread made at or after this Unix time (seconds), not an earlier session's")
			fs.String("opencode", "", "this OpenCode session (AutoDoc's OpenCode plugin runs it, from the session's own opencode)")
		},
		call: func(c sessionContext, fs *flag.FlagSet, args []string, stdin io.Reader) (string, []any, func(any) any, error) {
			if fs.Lookup("claude").Value.(flag.Getter).Get().(bool) {
				if len(args) != 0 {
					return "", nil, nil, errors.New("crew endpoint --claude takes no arguments")
				}
				if f, ok := stdin.(*os.File); ok {
					if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeCharDevice != 0 {
						stdin = nil // a terminal: no hook input to wait for
					}
				}
				loc, err := claudeLocator(stdin, os.Getenv, os.Getuid())
				if err != nil {
					return "", nil, nil, err
				}
				return "crew.endpoint", []any{c.Credential, "claude", loc}, nil, nil
			}
			if thread := strFlag(fs, "codex-thread"); thread != "" {
				if len(args) != 0 {
					return "", nil, nil, errors.New("crew endpoint --codex-thread takes no arguments")
				}
				loc, err := codexLocator(thread, os.Getenv)
				if err != nil {
					return "", nil, nil, err
				}
				return "crew.endpoint", []any{c.Credential, "codex", loc}, nil, nil
			}
			if fs.Lookup("codex-here").Value.(flag.Getter).Get().(bool) {
				if len(args) != 0 {
					return "", nil, nil, errors.New("crew endpoint --codex-here takes no arguments")
				}
				thread, err := codexThreadOfThisFolder(fs.Lookup("since").Value.(flag.Getter).Get().(int64))
				if err != nil {
					return "", nil, nil, err
				}
				loc, err := codexLocator(thread, os.Getenv)
				if err != nil {
					return "", nil, nil, err
				}
				return "crew.endpoint", []any{c.Credential, "codex", loc}, nil, nil
			}
			if session := strFlag(fs, "opencode"); session != "" {
				if len(args) != 0 {
					return "", nil, nil, errors.New("crew endpoint --opencode takes no arguments")
				}
				// the plugin's process holds the session's own context: never one found by folder
				if os.Getenv("AUTODOC_CREW_CONTEXT") == "" {
					return "", nil, nil, errors.New("crew endpoint --opencode needs $AUTODOC_CREW_CONTEXT, the session's own context: the plugin runs it from its session's opencode")
				}
				loc, err := opencodeLocator(session, os.Getppid(), crew.OpenCodeSocketDirs(os.Getuid()))
				if err != nil {
					return "", nil, nil, err
				}
				return "crew.endpoint", []any{c.Credential, "opencode", loc}, nil, nil
			}
			if len(args) != 2 {
				return "", nil, nil, errors.New("crew endpoint takes --claude, --codex-thread ID, or a kind and a locator")
			}
			return "crew.endpoint", []any{c.Credential, args[0], args[1]}, nil, nil
		}},
	"crew bridge": {usage: "--agy: an Antigravity session's bridge, run once from the agent's shell: it detaches, registers how the session is woken, and registers again whenever the daemon restarts",
		example: "autodoc crew bridge --agy",
		flags: func(fs *flag.FlagSet) {
			fs.Bool("agy", false, "an Antigravity session's bridge, from the agent's shell")
			fs.Bool("foreground", false, "run here, not detached (what the detached bridge runs)")
			fs.String("session", "", "the session the bridge was started for (the detached bridge's): another is refused")
		}}, // run by crewBridgeMain: it stays, so it is no one call
	"crew plugin": {usage: "--opencode [--print-env] [--dir D]: write AutoDoc's OpenCode plugin into AutoDoc's data folder and print its path, or with --print-env the OPENCODE_CONFIG_CONTENT that loads it (for a host's agent launch; nothing is written into a project)",
		example: "autodoc crew plugin --opencode --print-env",
		flags: func(fs *flag.FlagSet) {
			fs.Bool("opencode", false, "OpenCode's plugin")
			fs.Bool("print-env", false, "print OPENCODE_CONFIG_CONTENT=<json>, which loads it, instead of its path")
			fs.String("dir", "", "the plugin's folder (default AutoDoc's data folder, never a project's)")
		}}, // run by crewPluginMain: it needs no session, so it is no call
	"crew setup": {usage: "--kind K [--host window|nvim] [--program P] [--autodoc CMD] [--env K=V]…: print a crew agent's setup as JSON (argv to append, env to merge after the profile's, user-level files, prompts, notices) for a host's agent launch; the profile's env is read, never printed back",
		example: "autodoc crew setup --kind claude --host nvim --autodoc /path/to/autodoc",
		flags: func(fs *flag.FlagSet) {
			fs.String("kind", "", "the agent's crew kind")
			fs.String("host", crew.HostNvim, "nvim or window")
			fs.String("program", "", "the CLI the agent's command runs")
			fs.String("autodoc", "autodoc", "the command the hooks run AutoDoc with")
			fs.Var(envFlag{}, "env", "K=V of the profile's environment (repeatable)")
		}}, // run by crewSetupMain: it needs no session, so it is no call
	"crew claude-settings": {usage: "[--claude PATH] [--autodoc CMD] [--prompts]: print a crew Claude agent's settings for `claude --settings`, only the hooks the installed Claude Code knows (for a host's agent launch; nothing is written), or with --prompts whether they report its permission prompts, which its crew.register says",
		example: "autodoc crew claude-settings --claude ~/.local/bin/claude > crew-claude.json",
		flags: func(fs *flag.FlagSet) {
			fs.String("claude", "claude", "the Claude Code to write them for: a path, or a name looked up on PATH")
			fs.String("autodoc", "autodoc", "the command the hooks run AutoDoc with, as a shell reads it")
			fs.Bool("prompts", false, "print whether the settings report the permission prompts, instead of the settings")
		}}, // run by crewClaudeSettingsMain: it needs no session, so it is no call
	"crew attention": {usage: "raise <permission|blocked> [text] | clear | --claude-hook: tell the user this session waits on them, or no longer does (--claude-hook: Claude Code's hooks run it, with their input, for its permission prompts)",
		example: "autodoc crew attention raise permission \"waiting for your OK to run the migration\"",
		call: func(c sessionContext, _ *flag.FlagSet, args []string, _ io.Reader) (string, []any, func(any) any, error) {
			switch {
			case len(args) >= 2 && args[0] == "raise":
				return "crew.attention", []any{c.Credential, "raise", args[1], strings.Join(args[2:], " ")}, nil, nil
			case len(args) == 1 && args[0] == "clear":
				return "crew.attention", []any{c.Credential, "clear"}, nil, nil
			}
			return "", nil, nil, errors.New("crew attention takes raise <permission|blocked> [text], or clear")
		}},
	"mail receive": {usage: "lease your next deliveries, at most 8; run it until more is false",
		example: "autodoc mail receive",
		call: func(c sessionContext, _ *flag.FlagSet, _ []string, _ io.Reader) (string, []any, func(any) any, error) {
			return "mail.receive", []any{c.Credential}, withHandles, nil
		}},
	"mail inbox": {usage: "[--limit N]: a preview of your waiting mail; leases nothing",
		example: "autodoc mail inbox --limit 5",
		flags:   func(fs *flag.FlagSet) { fs.Int("limit", 20, "items to show, at most 1000; 0 counts only") },
		call: func(c sessionContext, fs *flag.FlagSet, _ []string, _ io.Reader) (string, []any, func(any) any, error) {
			return "mail.inbox", []any{c.Credential, int64(intFlag(fs, "limit"))}, nil, nil
		}},
	"mail ack": {usage: "<id>: done with a delivery",
		example: "autodoc mail ack <id from mail receive>",
		call:    leaseCall("mail.ack")},
	"mail renew": {usage: "<id>: keep a delivery's lease 10 more minutes, for long work",
		example: "autodoc mail renew <id from mail receive>",
		call:    leaseCall("mail.renew")},
	"mail reply": {usage: "<id> [--subject S] --body TEXT|-: answer a request (and ack it); safe to repeat",
		example: "autodoc mail reply <id from mail receive> --body \"Done: the tests pass.\"",
		flags:   bodyFlags,
		call: func(c sessionContext, fs *flag.FlagSet, args []string, stdin io.Reader) (string, []any, func(any) any, error) {
			if len(args) != 1 {
				return "", nil, nil, errors.New("mail reply takes the request's id")
			}
			d, l, err := parseHandle(args[0])
			if err != nil {
				return "", nil, nil, err
			}
			body, err := bodyOf(fs, stdin)
			if err != nil {
				return "", nil, nil, err
			}
			// one response per request: the delivery is the key, so a retry after a lost answer
			// gets that response back
			return "mail.reply", []any{c.Credential, d, l, body, "reply:" + d, strFlag(fs, "subject")}, nil, nil
		}},
	"mail send": {usage: "<to>... [--kind request] [--subject S] [--ttl 1h] [--key K] --body TEXT|-: send mail; to is a codename, name@scope, user or session:<id>",
		example: "autodoc mail send tifa-lockhart --kind request --subject \"Review\" --body \"Please review the plan in docs/plan.md.\"",
		flags: func(fs *flag.FlagSet) {
			bodyFlags(fs)
			fs.String("kind", "", "message (the default) or request")
			fs.Duration("ttl", 0, "how long until it expires, rounded up to whole seconds (0 never)")
			fs.String("key", "", "the idempotency key (default: the content's digest, so an exact repeat is one send)")
		},
		call: func(c sessionContext, fs *flag.FlagSet, args []string, stdin io.Reader) (string, []any, func(any) any, error) {
			if len(args) == 0 {
				return "", nil, nil, errors.New("mail send takes at least one recipient")
			}
			body, err := bodyOf(fs, stdin)
			if err != nil {
				return "", nil, nil, err
			}
			kind, subject := strFlag(fs, "kind"), strFlag(fs, "subject")
			ttl := fs.Lookup("ttl").Value.(flag.Getter).Get().(time.Duration)
			if ttl < 0 {
				return "", nil, nil, fmt.Errorf("--ttl %v is negative: give how long until it expires, or 0 for never", ttl)
			}
			key := strFlag(fs, "key")
			if key == "" {
				key = sendKey(args, kind, subject, body, ttl)
			}
			to := make([]any, len(args))
			for i, a := range args {
				to[i] = a
			}
			return "mail.send", []any{c.Credential, to, body, key, kind, subject, ttlSeconds(ttl)}, nil, nil
		}},
}

func leaseCall(method string) func(sessionContext, *flag.FlagSet, []string, io.Reader) (string, []any, func(any) any, error) {
	return func(c sessionContext, _ *flag.FlagSet, args []string, _ io.Reader) (string, []any, func(any) any, error) {
		if len(args) != 1 {
			return "", nil, nil, fmt.Errorf("%s takes one delivery id", strings.Replace(method, ".", " ", 1))
		}
		d, l, err := parseHandle(args[0])
		if err != nil {
			return "", nil, nil, err
		}
		return method, []any{c.Credential, d, l}, nil, nil
	}
}

func bodyFlags(fs *flag.FlagSet) {
	fs.String("subject", "", "the subject")
	fs.String("body", "", "the body, or - to read it from standard input")
}

func bodyOf(fs *flag.FlagSet, stdin io.Reader) (string, error) {
	body := strFlag(fs, "body")
	if body != "-" {
		return body, nil
	}
	b, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	return string(b), err
}

func strFlag(fs *flag.FlagSet, name string) string { return fs.Lookup(name).Value.String() }

func intFlag(fs *flag.FlagSet, name string) int {
	return fs.Lookup(name).Value.(flag.Getter).Get().(int)
}

// ttlSeconds is a non-negative --ttl as mail.send's whole seconds, rounded up: a sub-second ttl
// is one second, never 0, which would mean the message never expires. A negative one is refused
// before it gets here.
func ttlSeconds(ttl time.Duration) int64 {
	q, r := ttl/time.Second, ttl%time.Second
	if r > 0 {
		q++ // no overflow: q is at most MaxInt64/1e9
	}
	return int64(q)
}

// sendKey is a send's default idempotency key: its content's digest, so the same command run
// twice is one send while the message is kept.
func sendKey(to []string, kind, subject, body string, ttl time.Duration) string {
	h := sha256.New()
	for _, s := range append(append([]string{}, to...), kind, subject, body) {
		_ = binary.Write(h, binary.BigEndian, uint64(len(s)))
		h.Write([]byte(s))
	}
	_ = binary.Write(h, binary.BigEndian, int64(ttl))
	return "cli:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// withHandles gives each received delivery the id the other commands take: delivery and lease.
func withHandles(v any) any {
	res, ok := v.(map[string]any)
	if !ok {
		return v
	}
	items, _ := res["deliveries"].([]any)
	for _, it := range items {
		item, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if d, ok := item["delivery"].(map[string]any); ok {
			id, _ := d["id"].(string)
			lease, _ := d["lease"].(string)
			item["id"] = handle(id, lease)
		}
	}
	return res
}

// isCrewCommand reports whether args are an `autodoc crew|mail` subcommand, not the flag modes.
func isCrewCommand(args []string) bool {
	return len(args) > 0 && (args[0] == "crew" || args[0] == "mail")
}

// crewCommandArgs is args as crewMain takes them, when they are an agent's `autodoc crew|mail …`:
// as typed, or after a leading --config, the way an agent's bin/autodoc runs this build
// (`autodoc --config <file> "$@"`, tui/agent.go's agentWrapper), and that config's path ("" when
// none is given). The session's context names the daemon: the config is read only by the commands
// that keep a file in its data folder (crew plugin, crew claude-settings). ok false is any other
// command line.
func crewCommandArgs(args []string) (rest []string, configPath string, ok bool) {
	switch {
	case len(args) > 0 && (args[0] == "--config" || args[0] == "-config"):
		if len(args) < 2 {
			return nil, "", false
		}
		configPath, args = args[1], args[2:]
	case len(args) > 0 && (strings.HasPrefix(args[0], "--config=") || strings.HasPrefix(args[0], "-config=")):
		_, configPath, _ = strings.Cut(args[0], "=")
		args = args[1:]
	}
	if !isCrewCommand(args) {
		return nil, "", false
	}
	return args, configPath, true
}

// isHelp is a request for help in place of a command.
func isHelp(arg string) bool { return arg == "--help" || arg == "-h" || arg == "help" }

// crewHelp prints domain's commands (crew or mail), each with its usage and an example.
func crewHelp(w io.Writer, domain string) {
	fmt.Fprintf(w, "usage: autodoc %s <command> …  (each command's --help shows its flags)\n", domain)
	for _, name := range slices.Sorted(maps.Keys(crewCommands)) {
		if d, _, _ := strings.Cut(name, " "); d == domain {
			c := crewCommands[name]
			fmt.Fprintf(w, "\n  autodoc %s %s\n    e.g. %s\n", name, c.usage, c.example)
		}
	}
}

// crewMain is crewMainIn under the default config.
func crewMain(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return crewMainIn(ctx, "", args, stdin, stdout, stderr)
}

// crewMainIn runs `autodoc crew|mail <verb> …` and is its exit code: 0 with the answer on stdout; 1
// with the daemon's refusal as {"error": {code, message}} on stderr, or any other failure as text;
// 2 for usage. configPath is the --config it was given ("" for the default config).
func crewMainIn(ctx context.Context, configPath string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "usage: autodoc crew|mail <command> …")
		for _, name := range slices.Sorted(maps.Keys(crewCommands)) {
			fmt.Fprintf(stderr, "  autodoc %s %s\n", name, crewCommands[name].usage)
		}
		return 2
	}
	// `autodoc mail`, `autodoc mail --help`: the domain's commands, each with an example, as an
	// answer (stdout, 0), so an agent finds every command in one call
	if len(args) == 1 || (len(args) == 2 && isHelp(args[1])) {
		crewHelp(stdout, args[0])
		return 0
	}
	if len(args) < 2 {
		return usage()
	}
	name := args[0] + " " + args[1]
	if name == "crew bridge" {
		return crewBridgeMain(ctx, args[2:], stdout, stderr)
	}
	if name == "crew attention" && slices.Contains(args[2:], "--claude-hook") {
		return crewClaudeHookMain(stdin, stderr) // Claude's hooks run it: it reads their input, and prints nothing
	}
	cmd, ok := crewCommands[name]
	if !ok {
		fmt.Fprintf(stderr, "autodoc: no command %q\n", name)
		return usage()
	}
	if name == "crew plugin" {
		return crewPluginMain(configPath, args[2:], os.Getenv, stdout, stderr)
	}
	if name == "crew claude-settings" {
		return crewClaudeSettingsMain(configPath, args[2:], stdout, stderr)
	}
	if name == "crew setup" {
		return crewSetupMain(configPath, args[2:], os.Environ(), stdout, stderr)
	}
	fs := flag.NewFlagSet("autodoc "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	if cmd.flags != nil {
		cmd.flags(fs)
	}
	if err := fs.Parse(interleaved(args[2:], fs)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// the flags were printed to stderr; the command's line and example are the answer
			fmt.Fprintf(stdout, "autodoc %s %s\n  e.g. %s\n", name, cmd.usage, cmd.example)
			return 0
		}
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	c, err := findSessionContext(os.Getenv("AUTODOC_CREW_CONTEXT"), cwd)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	method, params, shape, err := cmd.call(c, fs, fs.Args(), stdin)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 2
	}
	res, err := crewCall(ctx, c, method, params)
	if err != nil {
		var re *golibrpc.Error
		if errors.As(err, &re) {
			b, _ := json.Marshal(map[string]any{"error": &CallError{Code: re.Code, Message: re.Message}})
			fmt.Fprintln(stderr, string(b))
		} else {
			fmt.Fprintln(stderr, "autodoc:", err)
		}
		return 1
	}
	res = jsonOf(res)
	if shape != nil {
		res = shape(res)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	return 0
}

// crewCall makes one call to the daemon the context names: never another, and never a spawn, so
// an agent's command can't start a daemon its host doesn't use.
func crewCall(ctx context.Context, c sessionContext, method string, params []any) (any, error) {
	session := client.NewSession(c.Socket, nil).As(rpc.CLIName).WithInfo(cliWho())
	if err := session.Connect(ctx); err != nil {
		return nil, err
	}
	defer session.Close()
	return session.Call(ctx, method, params...)
}

// interleaved puts args' flags before its positional arguments, so `mail send gold --body x`
// parses as written: the standard flag package stops at the first positional one.
func interleaved(args []string, fs *flag.FlagSet) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			rest = append(rest, args[i+1:]...)
			i = len(args)
		case len(a) > 1 && a[0] == '-':
			flags = append(flags, a)
			name, _, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if f := fs.Lookup(name); f != nil && !inline && !isBoolFlag(f) && i+1 < len(args) {
				i++
				flags = append(flags, args[i]) // its value, even "-" (read standard input)
			}
		default:
			rest = append(rest, a)
		}
	}
	return append(append(flags, "--"), rest...)
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}
