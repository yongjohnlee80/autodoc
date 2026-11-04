package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crewmail/sqlitecrewmail"
)

// reissueAgyLoc is an agy endpoint's locator: one whose check needs no live process, so a cell
// registers it as the agent's own process would.
const reissueAgyLoc = `{"address":"127.0.0.1:4000","conversation":"c-1"}`

// reissueRig is reissue against the served daemon d: the real crew.access, a register that stands in
// for the agent's own process (an agy crew.endpoint) and counts its calls, and a clock that a sleep
// moves on, running hostJoins at each sleep (the host's re-join, as a renewal would bring it).
type reissueRig struct {
	r         reissuer
	registers int
	sleeps    int
}

func newReissueRig(d *crewDaemon, hostJoins func(sleep int)) *reissueRig {
	rr := &reissueRig{}
	now := time.Unix(1_800_000_000, 0)
	rr.r = reissuer{
		call: crewCall,
		register: func(ctx context.Context, c sessionContext, kind string) error {
			rr.registers++
			_, err := crewCall(ctx, c, "crew.endpoint", []any{c.Credential, kind, reissueAgyLoc, ""})
			return err
		},
		sleep: func(_ context.Context, dur time.Duration) error {
			rr.sleeps++
			now = now.Add(dur)
			if hostJoins != nil {
				hostJoins(rr.sleeps)
			}
			return nil
		},
		now: func() time.Time { return now },
	}
	return rr
}

// listed is codename's addressbook row on d.
func listed(t *testing.T, d *crewDaemon, codename string) crew.Entry {
	t.Helper()
	es, err := d.deps.crew.List(d.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Codename == codename {
			return e
		}
	}
	t.Fatalf("%s is not in the addressbook: %+v", codename, es)
	return crew.Entry{}
}

// TestReissueBringsASlotJoinedLateItsEndpoint: a slot whose host joins it only after it started (no
// context at its path, then one) gets its session once the host writes it, and its endpoint from
// its own process: crew list then has it reachable, which a slot that only asked who it is (today's
// whoami) never is.
func TestReissueBringsASlotJoinedLateItsEndpoint(t *testing.T) {
	d := newCrewDaemon(t)
	var path string
	rr := newReissueRig(d, func(sleep int) {
		if sleep == 2 { // the host's re-join, two looks later
			path = d.agentOfKind("tifa", "agent-1", "store-1", "agy")
		}
	})
	session := func() (sessionContext, error) {
		if path == "" {
			return sessionContext{}, fs.ErrNotExist // the slot's context path, not written yet
		}
		return readSessionContext(path)
	}
	if hasRow(t, d, "tifa") {
		t.Fatal("tifa is listed before its host joined it")
	}
	res, err := reissue(d.ctx, session, reissueWait, rr.r)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Reissued, []string{reissueSession, reissueEndpoint}) || len(res.Kept) != 0 || len(res.Not) != 0 {
		t.Fatalf("reissue of a slot joined late: %+v, want its session and endpoint reissued", res)
	}
	if res.Access == nil || res.Access.Session != crew.AccessLive || res.Access.Endpoint != crew.EndpointRegistered {
		t.Fatalf("its access after: %+v, want live, registered", res.Access)
	}
	if e := listed(t, d, "tifa"); !e.Reachable || e.Why != "" {
		t.Fatalf("crew list after reissue: %+v, want tifa reachable", e)
	}
}

// hasRow is whether codename has an addressbook row on d.
func hasRow(t *testing.T, d *crewDaemon, codename string) bool {
	t.Helper()
	es, err := d.deps.crew.List(d.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(es, func(e crew.Entry) bool { return e.Codename == codename })
}

// TestReissueWaitsOutAnEndedSessionForItsHostsRejoin: a context whose session ended (its
// credential still matches that session) waits for its host's re-join, which writes a new session
// to the same path, and then registers that session's endpoint.
func TestReissueWaitsOutAnEndedSessionForItsHostsRejoin(t *testing.T) {
	d := newCrewDaemon(t)
	path := d.agentOfKind("tifa", "agent-1", "store-1", "agy")
	old, err := readSessionContext(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.deps.crew.Exit(d.ctx, "autodoc:aaaa", d.run, d.ep, old.Session, crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	rr := newReissueRig(d, func(sleep int) {
		if sleep == 1 {
			path = d.agentOfKind("tifa", "agent-2", "store-1", "agy")
		}
	})
	res, err := reissue(d.ctx, func() (sessionContext, error) { return readSessionContext(path) }, reissueWait, rr.r)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Reissued, []string{reissueSession, reissueEndpoint}) || res.Access == nil || res.Access.Session != crew.AccessLive {
		t.Fatalf("reissue over an ended session: %+v, want the re-joined session and its endpoint", res)
	}
	if rr.sleeps != 1 {
		t.Errorf("%d looks waited, want 1: the ended session is waited out, not answered", rr.sleeps)
	}
}

// TestReissueOfAHealthySessionChangesNothing: a live session whose endpoint is registered keeps both:
// no registration (which would raise its generation and reset its wake state), no wait.
func TestReissueOfAHealthySessionChangesNothing(t *testing.T) {
	d := newCrewDaemon(t)
	path := d.agentOfKind("tifa", "agent-1", "store-1", "agy")
	c, err := readSessionContext(path)
	if err != nil {
		t.Fatal(err)
	}
	g, err := d.deps.crew.SetEndpoint(d.ctx, c.Credential, "agy", reissueAgyLoc)
	if err != nil {
		t.Fatal(err)
	}
	rr := newReissueRig(d, nil)
	res, err := reissue(d.ctx, func() (sessionContext, error) { return readSessionContext(path) }, reissueWait, rr.r)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Kept, []string{reissueSession, reissueEndpoint}) || len(res.Reissued) != 0 || len(res.Not) != 0 {
		t.Fatalf("reissue of a healthy session: %+v, want both kept", res)
	}
	if rr.registers != 0 || rr.sleeps != 0 {
		t.Errorf("%d registrations and %d waits, want none", rr.registers, rr.sleeps)
	}
	a, err := d.deps.crew.AccessOf(d.ctx, c.Credential, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Generation != g {
		t.Errorf("the endpoint's generation is %d after reissue, want %d (unchanged)", a.Generation, g)
	}
}

// TestReissueSaysWhyWhenItsHostNeverJoins: with no context at the slot's path for the whole wait,
// reissue answers that its session was not re-issued and why, naming the wait, and registers
// nothing.
func TestReissueSaysWhyWhenItsHostNeverJoins(t *testing.T) {
	d := newCrewDaemon(t)
	rr := newReissueRig(d, nil)
	res, err := reissue(d.ctx, func() (sessionContext, error) { return sessionContext{}, errNoContext }, reissueWait, rr.r)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Not) != 1 || res.Not[0].Item != reissueSession || !strings.Contains(res.Not[0].Why, "within 40s") ||
		len(res.Reissued) != 0 || res.Access != nil {
		t.Fatalf("reissue with no host: %+v, want its session not re-issued, saying the 40s it waited", res)
	}
	if want := int(reissueWait / reissueEvery); rr.sleeps != want || rr.registers != 0 {
		t.Errorf("%d looks and %d registrations, want %d looks and none", rr.sleeps, rr.registers, want)
	}
}

// TestReissueRefusesAnotherAgentsContext: a context the CLI won't act as (another agent's, several
// to choose from) ends reissue at once with that refusal: it never waits on, nor calls as, a
// session that may not be its own.
func TestReissueRefusesAnotherAgentsContext(t *testing.T) {
	d := newCrewDaemon(t)
	rr := newReissueRig(d, nil)
	calls := 0
	rr.r.call = func(context.Context, sessionContext, string, []any) (any, error) { calls++; return nil, nil }
	for _, refused := range []error{errContextMismatch, errSeveralContexts} {
		if _, err := reissue(d.ctx, func() (sessionContext, error) { return sessionContext{}, refused }, reissueWait, rr.r); err != refused {
			t.Errorf("reissue with %q: %v, want that refusal", refused, err)
		}
	}
	if calls != 0 || rr.sleeps != 0 {
		t.Errorf("%d calls and %d waits, want none", calls, rr.sleeps)
	}
}

// TestReissueReportsAnEndpointOnlyOnceTheDaemonHasIt: a waker that returns without registering
// (agy's bridge detaches first; a registration refused after the command) is not reported as
// re-issued: reissue looks at crew.access for reissueConfirm, then says the endpoint wasn't.
func TestReissueReportsAnEndpointOnlyOnceTheDaemonHasIt(t *testing.T) {
	d := newCrewDaemon(t)
	path := d.agentOfKind("tifa", "agent-1", "store-1", "agy")
	rr := newReissueRig(d, nil)
	rr.r.register = func(context.Context, sessionContext, string) error { rr.registers++; return nil } // ran, registered nothing
	res, err := reissue(d.ctx, func() (sessionContext, error) { return readSessionContext(path) }, reissueWait, rr.r)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(res.Reissued, reissueEndpoint) || len(res.Not) != 1 || res.Not[0].Item != reissueEndpoint ||
		!strings.Contains(res.Not[0].Why, "no endpoint") {
		t.Fatalf("a waker that registered nothing: %+v, want the endpoint not re-issued, saying so", res)
	}
	if want := int(reissueConfirm / reissueEvery); rr.sleeps != want || rr.registers != 1 {
		t.Errorf("%d looks and %d registrations, want %d looks and 1", rr.sleeps, rr.registers, want)
	}
	if e := listed(t, d, "tifa"); e.Reachable {
		t.Errorf("crew list: %+v, want tifa still unreachable", e)
	}
}

// TestReissueConfirmsAnEndpointThatRegistersLate: a waker that registers a look or two after it
// returned (agy's bridge, detached) is re-issued as soon as crew.access says registered, not after
// the whole confirmation window.
func TestReissueConfirmsAnEndpointThatRegistersLate(t *testing.T) {
	d := newCrewDaemon(t)
	path := d.agentOfKind("tifa", "agent-1", "store-1", "agy")
	c, err := readSessionContext(path)
	if err != nil {
		t.Fatal(err)
	}
	rr := newReissueRig(d, nil)
	rr.r.register = func(context.Context, sessionContext, string) error { rr.registers++; return nil }
	sleep := rr.r.sleep
	rr.r.sleep = func(ctx context.Context, dur time.Duration) error {
		if err := sleep(ctx, dur); err != nil {
			return err
		}
		if rr.sleeps == 2 { // the bridge registers, two looks after it returned
			_, err := d.deps.crew.SetEndpoint(d.ctx, c.Credential, "agy", reissueAgyLoc)
			return err
		}
		return nil
	}
	res, err := reissue(d.ctx, func() (sessionContext, error) { return readSessionContext(path) }, reissueWait, rr.r)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Kept, []string{reissueSession}) || !slices.Equal(res.Reissued, []string{reissueEndpoint}) || len(res.Not) != 0 {
		t.Fatalf("a waker that registers late: %+v, want its endpoint re-issued", res)
	}
	if rr.sleeps != 2 {
		t.Errorf("%d looks, want 2: confirmed at the look after it registered", rr.sleeps)
	}
}

// TestReissueSaysWhatEndedItsConfirmation: a crew.access read that fails while an endpoint is being
// confirmed is reported, never taken for "no endpoint yet": a daemon gone ends reissue with that
// error; the session ending says so; an endpoint there but failing says its wakes fail, at once.
func TestReissueSaysWhatEndedItsConfirmation(t *testing.T) {
	d := newCrewDaemon(t)
	path := d.agentOfKind("tifa", "agent-1", "store-1", "agy")
	c, err := readSessionContext(path)
	if err != nil {
		t.Fatal(err)
	}
	session := func() (sessionContext, error) { return readSessionContext(path) }

	gone := errors.New("dial unix: connection refused")
	rr := newReissueRig(d, nil)
	calls := 0
	rr.r.call = func(ctx context.Context, c sessionContext, method string, params []any) (any, error) {
		if calls++; calls > 1 { // the first look finds the session; the daemon goes before the confirmation
			return nil, gone
		}
		return crewCall(ctx, c, method, params)
	}
	rr.r.register = func(context.Context, sessionContext, string) error { return nil }
	if _, err := reissue(d.ctx, session, reissueWait, rr.r); !errors.Is(err, gone) {
		t.Errorf("the daemon gone mid-confirmation: %v, want its error", err)
	}

	rr = newReissueRig(d, nil)
	rr.r.register = func(context.Context, sessionContext, string) error {
		if _, err := d.deps.crew.SetEndpoint(d.ctx, c.Credential, "agy", reissueAgyLoc); err != nil {
			return err
		}
		return sqlitecrewmail.New(d.deps.store).Write(d.ctx, func(tx crew.Tx) error {
			w, _, err := tx.Wake(c.Session)
			if err != nil {
				return err
			}
			w.Session, w.Failures = c.Session, crew.UnreachableAfter
			return tx.PutWake(w)
		})
	}
	res, err := reissue(d.ctx, session, reissueWait, rr.r)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Not) != 1 || !strings.Contains(res.Not[0].Why, "wakes fail") || rr.sleeps != 0 {
		t.Errorf("an endpoint there but failing: %+v after %d looks, want \"wakes fail\" at once", res, rr.sleeps)
	}

	rr = newReissueRig(d, nil)
	rr.r.register = func(context.Context, sessionContext, string) error {
		return d.deps.crew.Exit(d.ctx, "autodoc:aaaa", d.run, d.ep, c.Session, crew.EndedExit)
	}
	res, err = reissue(d.ctx, session, reissueWait, rr.r)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Not) != 1 || !strings.Contains(res.Not[0].Why, "session ended") || slices.Contains(res.Reissued, reissueEndpoint) {
		t.Errorf("the session ended mid-confirmation: %+v, want it said", res)
	}
}

// TestCrewReissueAnswersAsTheSessionTheCLIFinds: `autodoc crew reissue` runs reissue as the session
// the CLI finds, its answer in sentences, or with --json one JSON line: a healthy session keeps both
// its parts, and says nothing needed re-issuing; an OpenCode
// session with no endpoint is told its plugin registers it; with no context for the whole --wait
// (the CLI's own timer between looks), the session is not re-issued, naming the wait; another
// store's context is the daemon's refusal on stderr, exit 1; a --wait that isn't a duration is
// usage, exit 2.
func TestCrewReissueAnswersAsTheSessionTheCLIFinds(t *testing.T) {
	d := newCrewDaemon(t)
	t.Chdir(t.TempDir()) // a working directory with no context of its own
	strs := func(v any) string {
		var out []string
		for _, e := range asSlice(v) {
			if m, ok := e.(map[string]any); ok {
				out = append(out, fmt.Sprint(m["item"], ": ", m["why"]))
			} else {
				out = append(out, fmt.Sprint(e))
			}
		}
		return strings.Join(out, ",")
	}

	healthy := d.agentOfKind("tifa", "agent-1", "store-1", "agy")
	c, err := readSessionContext(healthy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.deps.crew.SetEndpoint(d.ctx, c.Credential, "agy", reissueAgyLoc); err != nil {
		t.Fatal(err)
	}
	if code, res, errOut := d.as(healthy, "", "crew", "reissue", "--json"); code != 0 || strs(res["kept"]) != "session,endpoint" || strs(res["reissued"]) != "" || strs(res["not"]) != "" {
		t.Fatalf("crew reissue of a healthy session: exit %d, %v, %s; want both kept", code, res, errOut)
	}

	oc := d.agentOfKind("oc", "agent-2", "store-1", crew.KindOpenCode)
	if code, res, errOut := d.as(oc, "", "crew", "reissue", "--json"); code != 0 || strs(res["kept"]) != "session" ||
		!strings.Contains(strs(res["not"]), "endpoint: AutoDoc's OpenCode plugin registers it") {
		t.Fatalf("crew reissue of an OpenCode session with no endpoint: exit %d, %v, %s; want its plugin named", code, res, errOut)
	}

	if code, res, errOut := d.as("", "", "crew", "reissue", "--wait=1s", "--json"); code != 0 || strs(res["kept"]) != "" ||
		!strings.Contains(strs(res["not"]), "session: no session context yet; its host did not re-join this slot within 1s") {
		t.Fatalf("crew reissue with no context: exit %d, %v, %s; want the session not re-issued within the wait", code, res, errOut)
	}

	// without --json it says so in sentences, the way a person at the shell reads it
	said := func(path string, args ...string) string {
		t.Helper()
		t.Setenv("AUTODOC_CREW_CONTEXT", path)
		var out, errOut bytes.Buffer
		if code := crewMain(d.ctx, append([]string{"crew", "reissue"}, args...), strings.NewReader(""), &out, &errOut); code != 0 {
			t.Fatalf("crew reissue %q: exit %d, %s", args, code, errOut.String())
		}
		return out.String()
	}
	if got, want := said(healthy), "Nothing needed re-issuing: your session and how you're woken (your endpoint) stand as they were, and nothing changed.\n"; got != want {
		t.Errorf("crew reissue of a healthy session said %q, want %q", got, want)
	}
	if got, want := said(oc), "Kept your session, as it stood.\nNot re-issued, how you're woken (your endpoint): AutoDoc's OpenCode plugin registers it, on the session's next message.\n"; got != want {
		t.Errorf("crew reissue of an OpenCode session with no endpoint said %q, want %q", got, want)
	}
	if got := said("", "--wait=1s"); !strings.HasPrefix(got, "Not re-issued, your session: no session context yet; its host did not re-join this slot within 1s") || strings.Contains(got, "{") {
		t.Errorf("crew reissue with no context said %q, want the session not re-issued, in a sentence", got)
	}

	// an answer that can't be written is a failure in either form, said on stderr, exit 1
	for _, args := range [][]string{nil, {"--json"}} {
		t.Setenv("AUTODOC_CREW_CONTEXT", healthy)
		var errOut bytes.Buffer
		if code := crewMain(d.ctx, append([]string{"crew", "reissue"}, args...), strings.NewReader(""), failingWriter{}, &errOut); code != 1 || !strings.Contains(errOut.String(), "no space left on device") {
			t.Errorf("crew reissue %q to a stdout that fails: exit %d, %q; want exit 1 and the write's error", args, code, errOut.String())
		}
	}

	foreign := d.agent("vision", "agent-3", "store-other")
	if code, _, errOut := d.as(foreign, "", "crew", "reissue", "--wait", "0s"); code != 1 || !strings.Contains(errOut, `"code":-32095`) {
		t.Fatalf("crew reissue with another store's context: exit %d, %s; want crew.context_mismatch", code, errOut)
	}

	for _, args := range [][]string{{"--wait", "soon"}, {"--wait=-1s"}, {"--wait"}, {"--later"}} {
		if code, _, errOut := d.as(healthy, "", append([]string{"crew", "reissue"}, args...)...); code != 2 || !strings.Contains(errOut, "usage: autodoc crew reissue [--wait D] [--json]") {
			t.Fatalf("crew reissue %q: exit %d, %s; want its usage", args, code, errOut)
		}
	}
}

// TestAKindWithNoWakerRegistersNone: reissue's registration names a kind AutoDoc has no waker for,
// and OpenCode's (its plugin's, on the session's next message), never taking either for done.
func TestAKindWithNoWakerRegistersNone(t *testing.T) {
	for kind, want := range map[string]string{"pi": "a pi agent has no waker yet", crew.KindOpenCode: "AutoDoc's OpenCode plugin registers it"} {
		if err := registerEndpoint(context.Background(), "", sessionContext{}, kind); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("registerEndpoint for %s: %v, want %q", kind, err, want)
		}
	}
}
