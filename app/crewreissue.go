package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// reissueWait is how long `crew reissue` waits for its host to re-join the slot by default: past
// one renewal (30 s), at which a host joins every running slot without a live session
// (docs/decisions/crew.md#re-issuing-a-bridge).
const reissueWait = 40 * time.Second

// reissueEvery is how often it looks again while it waits.
const reissueEvery = 2 * time.Second

// reissueConfirm is how long it looks for the daemon to hold an endpoint its kind's waker registers
// (agy's bridge registers after it detaches).
const reissueConfirm = 10 * time.Second

// The parts of a session's crew access `crew reissue` answers for.
const (
	reissueSession  = "session"  // its live session and the context naming it: its host's to write
	reissueEndpoint = "endpoint" // how it is woken: its own process's to register
)

// reissueResult is `crew reissue`'s answer: what it re-established, what it found standing, and
// what it could not, each with why. access is the crew access it ended with.
type reissueResult struct {
	Access   *crew.Access `json:"access,omitempty"`
	Reissued []string     `json:"reissued"`
	Kept     []string     `json:"kept"`
	Not      []reissueNot `json:"not"`
}

type reissueNot struct {
	Item string `json:"item"`
	Why  string `json:"why"`
}

// reissuer is what reissue does through the CLI's own paths, apart so a cell can stand in for it.
type reissuer struct {
	// call is one daemon call as the session c (crewCall).
	call func(ctx context.Context, c sessionContext, method string, params []any) (any, error)
	// register registers how a kind's session is woken, from this process, as its own command does.
	register func(ctx context.Context, c sessionContext, kind string) error
	// sleep waits d, or until ctx ends.
	sleep func(ctx context.Context, d time.Duration) error
	now   func() time.Time
}

// reissue re-establishes the calling agent's crew access on the running daemon, as far as the
// agent itself can (docs/decisions/crew.md#re-issuing-a-bridge). A missing or ended session is its
// host's to re-join, into the same context path: reissue waits up to wait for that, reading the
// context again each time. A live session's endpoint, none or failing, it registers again from this
// process; one registered and reached it keeps, so a reissue of a healthy session changes nothing.
// It never makes a credential: only a host does.
func reissue(ctx context.Context, session func() (sessionContext, error), wait time.Duration, r reissuer) (reissueResult, error) {
	res := reissueResult{Reissued: []string{}, Kept: []string{}, Not: []reissueNot{}}
	deadline := r.now().Add(wait)
	waited := false
	var c sessionContext
	var a crew.Access
	for {
		var err error
		c, err = session()
		if err == nil {
			a, err = accessOf(ctx, r, c)
			if err == nil && a.Session == crew.AccessLive {
				break
			}
		}
		why, waitable := reissueWaitable(err, a)
		if !waitable {
			return res, err
		}
		if !r.now().Before(deadline) {
			if a.Session != "" {
				res.Access = &a
			}
			res.Not = append(res.Not, reissueNot{reissueSession, fmt.Sprintf("%s; its host did not re-join this slot within %s (is its host running? `autodoc crew list` shows it)", why, wait)})
			return res, nil
		}
		waited = true
		if err := r.sleep(ctx, reissueEvery); err != nil {
			return res, err
		}
	}
	if waited {
		res.Reissued = append(res.Reissued, reissueSession)
	} else {
		res.Kept = append(res.Kept, reissueSession)
	}
	if a.Endpoint == crew.EndpointRegistered {
		res.Kept = append(res.Kept, reissueEndpoint)
		res.Access = &a
		return res, nil
	}
	if err := r.register(ctx, c, a.Kind); err != nil {
		res.Not = append(res.Not, reissueNot{reissueEndpoint, err.Error()})
		res.Access = &a
		return res, nil
	}
	// a command that returned is not an endpoint registered: agy's bridge detaches before it
	// registers, and any registration can be refused after the command is done. Only the daemon's
	// crew.access saying registered counts, looked at again for up to reissueConfirm.
	for tries := int(reissueConfirm / reissueEvery); ; tries-- {
		after, err := accessOf(ctx, r, c)
		if err != nil {
			why, waitable := reissueWaitable(err, crew.Access{})
			if !waitable { // no daemon, or a context it refuses: nothing to confirm against
				return res, err
			}
			res.Not = append(res.Not, reissueNot{reissueEndpoint, why + " while its endpoint was confirmed"})
			break
		}
		a = after
		if a.Session != crew.AccessLive {
			res.Not = append(res.Not, reissueNot{reissueEndpoint, "its session ended (" + a.Why + ") while its endpoint was confirmed"})
			break
		}
		if a.Endpoint == crew.EndpointRegistered {
			res.Reissued = append(res.Reissued, reissueEndpoint)
			break
		}
		if a.Endpoint == crew.EndpointFailing { // there, but its wakes don't reach it: no wait mends that
			res.Not = append(res.Not, reissueNot{reissueEndpoint, "registered, but its wakes fail (" + a.Why + ")"})
			break
		}
		if tries <= 0 {
			res.Not = append(res.Not, reissueNot{reissueEndpoint, fmt.Sprintf("its %s waker ran, but the daemon has no endpoint for this session after %s (crew list says why)", a.Kind, reissueConfirm)})
			break
		}
		if err := r.sleep(ctx, reissueEvery); err != nil {
			return res, err
		}
	}
	res.Access = &a
	return res, nil
}

// reissueWaitable is why the session isn't live yet and whether its host's re-join may still
// bring it: no context at the path yet, a credential the daemon no longer knows, or an ended
// session. Anything else (several contexts, another agent's, no daemon) ends reissue.
func reissueWaitable(err error, a crew.Access) (string, bool) {
	var re *golibrpc.Error
	switch {
	case err == nil && a.Session == crew.AccessEnded:
		return "its session ended (" + a.Why + ")", true
	case errors.Is(err, errNoContext), errors.Is(err, fs.ErrNotExist):
		return "no session context yet", true
	case errors.As(err, &re) && (re.Code == crew.CodeBadCredential || re.Code == crew.CodeStaleEpoch):
		return "its context names no session the daemon knows", true
	}
	return "", false
}

// accessOf is c's crew access (crew.access).
func accessOf(ctx context.Context, r reissuer, c sessionContext) (crew.Access, error) {
	v, err := r.call(ctx, c, "crew.access", []any{c.Credential, c.Store})
	if err != nil {
		return crew.Access{}, err
	}
	var a crew.Access
	b, err := json.Marshal(jsonOf(v))
	if err == nil {
		err = json.Unmarshal(b, &a)
	}
	return a, err
}

// cliReissuer is reissue through the CLI: crewCall, and each kind's own endpoint command.
func cliReissuer(configPath string) reissuer {
	return reissuer{
		call: crewCall,
		register: func(ctx context.Context, c sessionContext, kind string) error {
			return registerEndpoint(ctx, configPath, c, kind)
		},
		sleep: func(ctx context.Context, d time.Duration) error {
			// NOTE: the pause between reissue's reads of crew.access: the host's re-join and the
			// endpoint's registration announce nothing to this CLI process, so it reads again after d;
			// ctx ends the wait.
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		now: time.Now,
	}
}

// registerEndpoint registers how session c, of kind, is woken, from this process, by that kind's own
// command: Claude's `crew endpoint --claude` (its session id from the environment, the inbox socket
// from this process's ancestors), Codex's `crew endpoint --codex-here --wait 30s` (which starts its
// prompt bridge too), agy's `crew bridge --agy` (which needs agy's own shell: its MCP server has no
// token). OpenCode's plugin registers its own on the session's next message.
func registerEndpoint(ctx context.Context, configPath string, c sessionContext, kind string) error {
	var args []string
	switch kind {
	case "claude":
		_, err := runCrewCommand(ctx, "crew endpoint", []string{"--claude"}, strings.NewReader(""), io.Discard,
			func() (sessionContext, error) { return c, nil })
		return err
	case "codex":
		args = []string{"crew", "endpoint", "--codex-here", "--wait", "30s"}
	case "agy":
		args = []string{"crew", "bridge", "--agy"}
	case "opencode":
		return errors.New("AutoDoc's OpenCode plugin registers it, on the session's next message")
	default:
		return fmt.Errorf("a %s agent has no waker yet", kind)
	}
	var said strings.Builder
	if code := crewMainIn(context.WithoutCancel(ctx), configPath, args, nil, io.Discard, &said); code != 0 {
		return fmt.Errorf("autodoc %s: %s", strings.Join(args, " "), strings.TrimSpace(said.String()))
	}
	return nil
}

// reissuePart names an item as a sentence says it.
var reissuePart = map[string]string{reissueSession: "your session", reissueEndpoint: "how you're woken (your endpoint)"}

// said is res as sentences, one per line: what was re-issued, what stood, and what could not be, each
// with why. A healthy session's says nothing needed re-issuing.
func (res reissueResult) said() string {
	parts := func(items []string) string {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = reissuePart[it]
		}
		return strings.Join(out, " and ")
	}
	var lines []string
	if len(res.Reissued) == 0 && len(res.Not) == 0 {
		lines = append(lines, "Nothing needed re-issuing: "+parts(res.Kept)+" stand as they were, and nothing changed.")
	} else {
		if len(res.Reissued) > 0 {
			lines = append(lines, "Re-issued "+parts(res.Reissued)+".")
		}
		if len(res.Kept) > 0 {
			lines = append(lines, "Kept "+parts(res.Kept)+", as it stood.")
		}
		for _, n := range res.Not {
			lines = append(lines, "Not re-issued, "+reissuePart[n.Item]+": "+n.Why+".")
		}
	}
	return strings.Join(lines, "\n")
}

// crewReissueMain runs `autodoc crew reissue [--wait D] [--json]`: reissue as the session the CLI
// finds, its answer on stdout in sentences (said), or with --json as one line. Exit 0 with an answer,
// 1 when reissue could not run (several contexts, another agent's, no daemon), 2 for usage.
func crewReissueMain(ctx context.Context, configPath string, args []string, stdout, stderr io.Writer) int {
	wait, asJSON := reissueWait, false
	for i := 0; i < len(args); i++ {
		if args[i] == "--json" {
			asJSON = true
			continue
		}
		v, ok := strings.CutPrefix(args[i], "--wait=")
		if args[i] == "--wait" && i+1 < len(args) {
			v, ok, i = args[i+1], true, i+1
		}
		d, err := time.ParseDuration(v)
		if !ok || err != nil || d < 0 {
			fmt.Fprintln(stderr, "usage: autodoc crew reissue [--wait D] [--json]   e.g. autodoc crew reissue --wait 40s")
			return 2
		}
		wait = d
	}
	res, err := reissue(ctx, sessionHere, wait, cliReissuer(configPath))
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
	if !asJSON {
		fmt.Fprintln(stdout, res.said())
		return 0
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	return 0
}
