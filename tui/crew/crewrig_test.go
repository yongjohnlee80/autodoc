package crew

import (
	"context"
	"sync"
	"testing"

	corecrew "github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/tui/agents"
)

// The crew mail's cells' rig: its agents made on tui/agents' fakes (fakeagents_test.go) with the
// fake seams, and copies of the host's messaging rig, each naming its original.

// A copy of the host's messagingRig (agentmessaging_test.go).
// messagingRig is crewAgents with agent one started: a window holding a crew scope, one agent
// running in it, its CLI reporting its prompts unless a cell says otherwise.
func messagingRig(t *testing.T) (*agents.Agents, *identityHost, *crewWindow) {
	t.Helper()
	a, c, w := crewAgents(t, oneTwo)
	p, _ := a.Profile("one")
	if instance, err := a.StartInstance(p); err != nil || !instance.State().Running {
		t.Fatalf("start: %v", err)
	}
	fakeSeamsOf(a).promptsReported = func(string, bool) bool { return true }
	return a, c, w
}

// A copy of the host's settleMessaging (agentmessaging_test.go).
// settleMessaging runs what the window and the crew host left for later: posted deliveries, the
// base's Enter, the doorbell's asks again (settleRounds rounds of the crew host's timers).
func settleMessaging(c *identityHost, w *crewWindow) {
	for round := 0; ; round++ {
		c.drainPosts()
		c.fireAfters()
		if len(w.timers) == 0 || round == settleRounds {
			return
		}
		due := w.timers
		w.timers = nil
		for _, f := range due {
			f()
		}
	}
}

// A copy of the host's settleRounds (agentmessaging_test.go).
// settleRounds bounds settleMessaging's rounds of the crew host's timers: the doorbell's asks again
// need UserSendTries of them, and the scope's renewal re-arms itself every round, so the timers
// never run out on their own.
const settleRounds = UserSendTries + 2

// A copy of the host's started (agentstartup_test.go).
// started gives codename's live session an endpoint, as its SessionStart hook registers once
// Claude Code is past its startup.
func (d *crewDaemon) started(t *testing.T, codename string) {
	t.Helper()
	if err := d.mem.Write(context.Background(), func(tx corecrew.Tx) error {
		profiles, err := tx.ActiveProfiles()
		if err != nil {
			return err
		}
		for _, p := range profiles {
			if p.Codename != codename {
				continue
			}
			s, live, err := tx.LiveSession(p.ID)
			if err != nil || !live {
				return err
			}
			return tx.PutEndpoint(corecrew.Endpoint{Session: s.ID, Generation: 1, Kind: p.Kind})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// testAgentsWith is tui/agents' NewAgents for the crew mail's cells, on the fake seams s, which
// fakeSeamsOf answers for the agents made (as tui/agents' testAgentsWith and fakeSeamsOf, whose
// agents read their seams themselves).
func testAgentsWith(c agents.AgentHost, s *fakeSeams, dir, config string) *agents.Agents {
	a := agents.NewAgents(c, s, dir, config)
	agentsSeams.Lock()
	agentsSeams.m[a] = s
	agentsSeams.Unlock()
	return a
}

// fakeSeamsOf is the fake seams a cell made a's agents with (testAgentsWith): a cell sets its own
// hook there before the agents use it.
func fakeSeamsOf(a *agents.Agents) *fakeSeams {
	agentsSeams.Lock()
	defer agentsSeams.Unlock()
	s, ok := agentsSeams.m[a]
	if !ok {
		panic("fakeSeamsOf: these agents weren't made by testAgentsWith")
	}
	return s
}

// agentsSeams are the seams each rig's agents were made with, by the agents (fakeSeamsOf).
var agentsSeams = struct {
	sync.Mutex
	m map[*agents.Agents]*fakeSeams
}{m: map[*agents.Agents]*fakeSeams{}}

// tuneEnv sets variables in the environment of t's agents (procenv.Env.With: NAME=value sets NAME,
// NAME alone removes it), over what t set so far, else over the process's, as the host's tuneEnv
// sets its program's: crewAgents makes t's agents with it (cellEnv), and the process's environment
// is left as it is.
func tuneEnv(t testing.TB, set ...string) {
	t.Helper()
	cellEnvs.Lock()
	defer cellEnvs.Unlock()
	name := t.Name()
	if _, ok := cellEnvs.m[name]; !ok {
		t.Cleanup(func() {
			cellEnvs.Lock()
			delete(cellEnvs.m, name)
			cellEnvs.Unlock()
		})
	}
	cellEnvs.m[name] = cellEnvs.m[name].With(set...)
}

// cellEnv is the environment tuneEnv set for t's agents: nil (the process's) when it set none.
func cellEnv(t testing.TB) procenv.Env {
	cellEnvs.Lock()
	defer cellEnvs.Unlock()
	return cellEnvs.m[t.Name()]
}

// cellEnvs are the environments tuneEnv set, by test name; parallel tests share the map.
var cellEnvs = struct {
	sync.Mutex
	m map[string]procenv.Env
}{m: map[string]procenv.Env{}}
