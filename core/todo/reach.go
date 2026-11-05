package todo

import (
	"context"
	"fmt"
	"strings"

	"github.com/yongjohnlee80/golib/logger"
)

// The reach states, as mail.Reach answers them (docs/decisions/todos.md#an-assignment-says-who-it-reaches).
const (
	ReachOwn       = "own"
	ReachOther     = "other"
	ReachNone      = "none"
	ReachAmbiguous = "ambiguous"
)

// bareAgent is the codename of an assignee written agent:<name>: a name the crew resolves from the
// assigner's scope. ok is false for every other form: the user, an AutoDoc window's
// (autodoc:<codename>), an instance's (agent:<name>:<instance>), an address (agent:<name>@<project>,
// which names its project itself) and "".
func bareAgent(assignee string) (name string, ok bool) {
	name, ok = strings.CutPrefix(assignee, "agent:")
	if !ok || name == "" || strings.ContainsAny(name, ":@") {
		return "", false
	}
	return name, true
}

// reachOf is where assignee is told when the assignment is made from the crew scope crew: nil when
// there is nothing to say (no crew scope given, no WithReach, an assignee that is no bare agent
// name). A failed lookup is logged and answers nil: the assignment is already written, and the
// answer still carries it.
func (m *Manager) reachOf(ctx context.Context, crew, assignee string) *Reach {
	name, ok := bareAgent(assignee)
	if crew == "" || m.o.reach == nil || !ok {
		return nil
	}
	r, err := m.o.reach(ctx, crew, name)
	if err != nil {
		logger.Warning(m.o.log, err, "todo: where an assignment to "+name+" goes: its answer carries no reach")
		return nil
	}
	switch r.State {
	case ReachOther:
		r.Note = fmt.Sprintf("%s is another project's agent: told as %s", name, r.Address)
	case ReachNone:
		r.Warning = fmt.Sprintf("nobody will be told: no project has an agent named %s", name)
	case ReachAmbiguous:
		r.Warning = fmt.Sprintf("nobody will be told: %s is %s; assign one of them by name@project",
			name, strings.Join(r.Candidates, " or "))
	}
	return &r
}
