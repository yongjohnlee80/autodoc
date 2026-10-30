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

// The agent's commands (ADR 1793271500 §4.5, §4.11): `autodoc crew …` and `autodoc mail …`, each
// one call to the daemon on the session context's credential, its answer printed as JSON. They are
// what the agent guide (crew.guide) tells an agent to run.

// crewContextDir is an agent folder's folder of session contexts (crew.ContextDir), one file per
// session, named by its id.
const crewContextDir = crew.ContextDir

// sessionContext is a session's context file, written by its host before the agent starts (§4.5).
type sessionContext = crew.Context

var (
	errNoContext       = errors.New("no session context: neither $AUTODOC_CREW_CONTEXT nor ./" + crewContextDir + "/ names one; this process was not started as a crew agent")
	errSeveralContexts = errors.New("several session contexts in ./" + crewContextDir + "/ and no $AUTODOC_CREW_CONTEXT to say which is yours: run as the agent your host started, never with a guess")
	errContextMismatch = errors.New("the session contexts disagree: $AUTODOC_CREW_CONTEXT names a session ./" + crewContextDir + "/ does not hold; you may be running with another agent's context, so stop and tell the user")
)

// findSessionContext is the session context (§4.5): the file $AUTODOC_CREW_CONTEXT names (envFile).
// Without one, the working directory's ./.autodoc-crew/ when it holds exactly one; several are
// refused, never one guessed. With both, a working directory whose folder holds contexts, none of
// them the env's session, is refused: the env may be another agent's.
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
	usage string
	call  func(c sessionContext, fs *flag.FlagSet, args []string, stdin io.Reader) (method string, params []any, shape func(any) any, err error)
	flags func(fs *flag.FlagSet)
}

// crewCommands are the subcommands, by "domain verb".
var crewCommands = map[string]crewCommand{
	"crew whoami": {usage: "who this session is: its codename, scope, session and store",
		call: func(c sessionContext, _ *flag.FlagSet, _ []string, _ io.Reader) (string, []any, func(any) any, error) {
			return "crew.whoami", []any{c.Credential, c.Store}, nil, nil
		}},
	"crew list": {usage: "the addressbook: every codename, its scope, presence and reachability",
		call: func(sessionContext, *flag.FlagSet, []string, io.Reader) (string, []any, func(any) any, error) {
			return "crew.list", nil, nil, nil
		}},
	"crew endpoint": {usage: "--claude | --codex-thread ID | <kind> <locator JSON>: register how this session is woken (--claude: Claude Code's SessionStart hook runs it; --codex-thread: a Codex agent, with its $CODEX_THREAD_ID)",
		flags: func(fs *flag.FlagSet) {
			fs.Bool("claude", false, "this Claude Code session's inbox socket, found from the hook's input and its process")
			fs.String("codex-thread", "", "this Codex session's thread ($CODEX_THREAD_ID), on the app-server its TUI is attached to")
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
			if len(args) != 2 {
				return "", nil, nil, errors.New("crew endpoint takes --claude, --codex-thread ID, or a kind and a locator")
			}
			return "crew.endpoint", []any{c.Credential, args[0], args[1]}, nil, nil
		}},
	"crew bridge": {usage: "--agy: an Antigravity session's bridge, run once from the agent's shell: it detaches, registers how the session is woken, and registers again whenever the daemon restarts",
		flags: func(fs *flag.FlagSet) {
			fs.Bool("agy", false, "an Antigravity session's bridge, from the agent's shell")
			fs.Bool("foreground", false, "run here, not detached (what the detached bridge runs)")
			fs.String("session", "", "the session the bridge was started for (the detached bridge's): another is refused")
		}}, // run by crewBridgeMain: it stays, so it is no one call
	"crew attention": {usage: "raise <permission|blocked> [text] | clear: tell the user this session waits on them, or no longer does",
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
		call: func(c sessionContext, _ *flag.FlagSet, _ []string, _ io.Reader) (string, []any, func(any) any, error) {
			return "mail.receive", []any{c.Credential}, withHandles, nil
		}},
	"mail inbox": {usage: "[--limit N]: a preview of your waiting mail; leases nothing",
		flags: func(fs *flag.FlagSet) { fs.Int("limit", 20, "items to show, at most 1000; 0 counts only") },
		call: func(c sessionContext, fs *flag.FlagSet, _ []string, _ io.Reader) (string, []any, func(any) any, error) {
			return "mail.inbox", []any{c.Credential, int64(intFlag(fs, "limit"))}, nil, nil
		}},
	"mail ack": {usage: "<id>: done with a delivery",
		call: leaseCall("mail.ack")},
	"mail renew": {usage: "<id>: keep a delivery's lease 10 more minutes, for long work",
		call: leaseCall("mail.renew")},
	"mail reply": {usage: "<id> [--subject S] --body TEXT|-: answer a request (and ack it); safe to repeat",
		flags: bodyFlags,
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

// crewMain runs `autodoc crew|mail <verb> …` and is its exit code: 0 with the answer on stdout; 1
// with the daemon's refusal as {"error": {code, message}} on stderr, or any other failure as text;
// 2 for usage.
func crewMain(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "usage: autodoc crew|mail <command> …")
		for _, name := range slices.Sorted(maps.Keys(crewCommands)) {
			fmt.Fprintf(stderr, "  autodoc %s %s\n", name, crewCommands[name].usage)
		}
		return 2
	}
	if len(args) < 2 {
		return usage()
	}
	name := args[0] + " " + args[1]
	if name == "crew bridge" {
		return crewBridgeMain(ctx, args[2:], stdout, stderr)
	}
	cmd, ok := crewCommands[name]
	if !ok {
		fmt.Fprintf(stderr, "autodoc: no command %q\n", name)
		return usage()
	}
	fs := flag.NewFlagSet("autodoc "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	if cmd.flags != nil {
		cmd.flags(fs)
	}
	if err := fs.Parse(interleaved(args[2:], fs)); err != nil {
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
