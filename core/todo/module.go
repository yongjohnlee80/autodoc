package todo

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// The todos.* verbs' public codes, in the module's range (core/module codes.go).
const (
	CodeStale       int64 = -32071 // todos.get the task again, re-apply, retry with its rev
	CodeNoTask      int64 = -32072 // list the tasks again
	CodeInvalid     int64 = -32073 // fix the field the message names
	CodeNotAStore   int64 = -32074 // todos.scaffold, or todos.set_dir
	CodeExists      int64 = -32075 // another id, or another move_store target
	CodeNotWritable int64 = -32076 // a workspace's or a registered root's store, or a set_dir override
	CodeUnclaimed   int64 = -32077 // the store is auto-core's until the cutover claims it
	// codeNoWorkspace is the daemon's shared code (rpc's CodeNoSuchWorkspace).
	codeNoWorkspace int64 = -32060
)

// protocol is the protocol the todos verbs arrived in.
const protocol = 20

// Name is the module's name: its verbs are todos.*.
func (m *Manager) Name() string { return "todos" }

// Errors are what the todos verbs may answer besides invalid params.
func (m *Manager) Errors() []module.PublicError {
	return []module.PublicError{
		{Err: ErrStale, Code: CodeStale, Name: "todos.stale", Message: "the task changed since it was read",
			Next: "todos.get it again, re-apply the change, and retry with its new rev"},
		{Err: ErrNotFound, Code: CodeNoTask, Name: "todos.not_found", Message: "no such task", Next: "list the tasks again"},
		{Err: ErrInvalid, Code: CodeInvalid, Name: "todos.invalid", Message: "a value the todo format or its rules refuse",
			Next: "fix the field and send it again"},
		{Err: ErrNotAStore, Code: CodeNotAStore, Name: "todos.not_a_store", Message: "there is no todo store there",
			Next: "todos.scaffold one, or todos.set_dir to another"},
		{Err: ErrExists, Code: CodeExists, Name: "todos.exists", Message: "that already exists",
			Next: "pick another id, or another move_store target"},
		{Err: ErrNotWritable, Code: CodeNotWritable, Name: "todos.not_writable",
			Message: "not a writable todo store: a workspace's or a registered root's .todo-list, or a set_dir override",
			Next:    "todos.register the project's root, or todos.set_dir"},
		{Err: ErrUnclaimed, Code: CodeUnclaimed, Name: "todos.unclaimed",
			Message: "the daemon does not write this store yet: auto-core does, until the cutover claims it",
			Next:    "edit it through AutoVim until the cutover"},
		{Err: ErrNoWorkspace, Code: codeNoWorkspace, Name: "todos.no_such_workspace", Message: "no such workspace",
			Next: "re-list the workspaces"},
	}
}

// The verbs' params, in their positional order.
type (
	atParams struct {
		At At `param:"at" doc:"a workspace name, or {root = \"/abs/project\"}"`
	}
	openParams struct {
		At   At       `param:"at"`
		Opts OpenOpts `param:"opts,optional" doc:"{kb_root}"`
	}
	registerParams struct {
		Root string `param:"root" doc:"an existing directory: a project that is no AutoDoc workspace"`
	}
	setDirParams struct {
		At  At     `param:"at"`
		Dir string `param:"dir" doc:"the store's directory; \"\" clears the override"`
	}
	moveParams struct {
		At At     `param:"at"`
		To string `param:"to" doc:"the store's new directory; nothing may be there yet"`
	}
	listParams struct {
		At   At       `param:"at"`
		Opts ListOpts `param:"opts,optional" doc:"{buckets, tag, assignee, priority, has_errors}; buckets [\"archived\"] lists the months, [\"archived/YYYY/MM\"] one month"`
	}
	idParams struct {
		At At     `param:"at"`
		ID string `param:"id"`
	}
	addParams struct {
		At   At   `param:"at"`
		Spec Spec `param:"spec" doc:"{title, id?, description?, status?, due?, priority?, assignee?, tags?, adr?, review?, blocked?}"`
	}
	updateParams struct {
		At    At             `param:"at"`
		ID    string         `param:"id"`
		Rev   string         `param:"rev" doc:"the rev the client read"`
		Patch map[string]any `param:"patch" doc:"hand-editable fields only: title, description, priority, due, tags, adr, review, blocked"`
	}
	statusParams struct {
		At     At     `param:"at"`
		ID     string `param:"id"`
		Rev    string `param:"rev"`
		Status string `param:"status" doc:"open, in-progress, deferred, completed or archived"`
	}
	assignParams struct {
		At       At     `param:"at"`
		ID       string `param:"id"`
		Rev      string `param:"rev"`
		Assignee string `param:"assignee" doc:"\"\" clears it"`
		Reason   string `param:"reason,optional"`
	}
	revParams struct {
		At  At     `param:"at"`
		ID  string `param:"id"`
		Rev string `param:"rev"`
	}
	pathParams struct {
		At   At     `param:"at"`
		Path string `param:"path" doc:"the malformed file, store-relative"`
	}
	changesParams struct {
		At       At     `param:"at"`
		Consumer string `param:"consumer"`
		Limit    int64  `param:"limit,optional" doc:"at most this many; 0 for the default"`
	}
	ackParams struct {
		At       At     `param:"at"`
		Consumer string `param:"consumer"`
		Seq      int64  `param:"seq"`
	}
	claimParams struct {
		At   At        `param:"at"`
		Opts ClaimOpts `param:"opts,optional" doc:"{legacy_workspace}: the directory auto-core's $WORKSPACE meant"`
	}
	setVarParams struct {
		Name  string `param:"name"`
		Value string `param:"value" doc:"\"\" removes it"`
	}
)

// none is the result of a verb that answers nothing.
type none = any

// Verbs are the todos.* verbs. Every one names its store by its first param (At); the host
// resolves nothing for them, so they are ScopeGlobal.
func (m *Manager) Verbs() []module.Verb {
	// read is a verb that changes no store; quiet, one that changes daemon state the view does not
	// show; write, one whose success logs todos.changed, so every view of the store refreshes.
	read := func(verb, result, doc string, codes ...int64) module.Contract {
		return module.Contract{Verb: verb, Since: protocol, Audience: module.AudienceAgent, Result: result,
			Errors: append([]int64{codeNoWorkspace}, codes...), Doc: doc}
	}
	quiet := func(verb, result, doc string, audience module.Audience, codes ...int64) module.Contract {
		c := read(verb, result, doc, codes...)
		c.Audience = audience
		return c
	}
	write := func(verb, result, doc string, audience module.Audience, codes ...int64) module.Contract {
		c := quiet(verb, result, doc, audience, codes...)
		c.Event = &module.EventSpec{Kind: "todos.changed"}
		return c
	}
	agent, user := module.AudienceAgent, module.AudienceUser
	writes := []int64{CodeNotWritable, CodeUnclaimed}
	task := "task: {id, version, status, title, …, rev, path, description}"
	verbs := []module.Verb{
		module.Handle(read("todos.resolve", "{store_id, store, root, override?, writable, claimed}",
			"Where an at's store is, and whether the daemon may write it."),
			func(ctx context.Context, p atParams) (Resolved, error) { return m.Resolve(ctx, p.At) }),
		module.Handle(read("todos.open", "{store_id, store, scaffolded: false}",
			"Checks that an at's store exists, and records the client's kb_root for its refs.", CodeNotAStore),
			func(ctx context.Context, p openParams) (Opened, error) { return m.Open(ctx, p.At, p.Opts) }),
		module.Handle(write("todos.scaffold", "{store_id, store, scaffolded}",
			"Creates the at's .todo-list and its buckets.", agent, append(writes, CodeExists)...),
			func(ctx context.Context, p atParams) (Opened, error) { return m.Scaffold(ctx, p.At) }),
		module.Handle(quiet("todos.register", "{new}",
			"Enrolls a project root that is no AutoDoc workspace, so its .todo-list may be written; allowed before any claim.",
			agent, CodeInvalid),
			func(ctx context.Context, p registerParams) (map[string]bool, error) {
				fresh, err := m.Register(ctx, p.Root)
				return map[string]bool{"new": fresh}, err
			}),
		module.Handle(write("todos.set_dir", "{store_id, store, root, override?, writable, claimed}",
			"Points an at's root at another store directory, or back at <root>/.todo-list.", user, CodeInvalid),
			func(ctx context.Context, p setDirParams) (Resolved, error) { return m.SetDir(ctx, p.At, p.Dir) }),
		module.Handle(read("todos.stores", "[{id, path, roots, claimed, last_use}]", "The stores the daemon knows."),
			func(ctx context.Context, _ struct{}) ([]StoreInfo, error) { return m.Stores(ctx) }),
		module.Handle(write("todos.move_store", "{store_id, store, root, override?, writable, claimed}",
			"Moves an at's store to a new directory; its claim, change log and cursors move with it.",
			user, append(writes, CodeExists)...),
			func(ctx context.Context, p moveParams) (Resolved, error) { return m.MoveStore(ctx, p.At, p.To) }),
		module.Handle(read("todos.list", "{tasks: ["+task+"], malformed: [{path, reason}], partitions?: [{month, count}], cursor}",
			"The active buckets' tasks; archives only by month, on request.", CodeNotAStore, CodeInvalid),
			func(ctx context.Context, p listParams) (Listing, error) { return m.List(ctx, p.At, p.Opts) }),
		module.Handle(read("todos.get", task, "One task, with its rev and body.", CodeNoTask, CodeNotAStore),
			func(ctx context.Context, p idParams) (Task, error) { return m.Get(ctx, p.At, p.ID) }),
		module.Handle(write("todos.add", task,
			"Creates a task. With an assignee it is an assignment: open becomes in-progress, and the assignee is told.",
			agent, append(writes, CodeInvalid, CodeExists)...),
			func(ctx context.Context, p addParams) (Task, error) { return m.Add(ctx, p.At, p.Spec) }),
		module.Handle(write("todos.update", task,
			"Changes hand-editable fields of a task read at rev; status and assignee have their own verbs.",
			agent, append(writes, CodeStale, CodeNoTask, CodeInvalid)...),
			func(ctx context.Context, p updateParams) (Task, error) {
				return m.Update(ctx, p.At, p.ID, p.Rev, Patch(p.Patch))
			}),
		module.Handle(write("todos.status", task, "Moves a task read at rev to another status.",
			agent, append(writes, CodeStale, CodeNoTask, CodeInvalid)...),
			func(ctx context.Context, p statusParams) (Task, error) {
				return m.Status(ctx, p.At, p.ID, p.Rev, p.Status)
			}),
		module.Handle(write("todos.assign", task,
			"Assigns a task read at rev (\"\" clears it); assigning an open task makes it in-progress, and the assignee is told.",
			agent, append(writes, CodeStale, CodeNoTask)...),
			func(ctx context.Context, p assignParams) (Task, error) {
				return m.Assign(ctx, p.At, p.ID, p.Rev, p.Assignee, p.Reason)
			}),
		module.Handle(write("todos.remove", "nil", "Deletes a task read at rev.", agent, append(writes, CodeStale, CodeNoTask)...),
			func(ctx context.Context, p revParams) (none, error) { return nil, m.Remove(ctx, p.At, p.ID, p.Rev) }),
		module.Handle(write("todos.remove_malformed", "nil",
			"Deletes one malformed file; a file using the removed automation fields is never deleted by the daemon.",
			user, append(writes, CodeNoTask, CodeInvalid)...),
			func(ctx context.Context, p pathParams) (none, error) {
				return nil, m.RemoveMalformed(ctx, p.At, p.Path)
			}),
		module.Handle(write("todos.refresh", "{scanned, moved, archived, errors_set, rewritten, malformed}",
			"Reconciles the active buckets: misplaced files, errors[], the 28-day archive. Never reads archives.",
			agent, writes...),
			func(ctx context.Context, p atParams) (RefreshSummary, error) { return m.Refresh(ctx, p.At) }),
		module.Handle(quiet("todos.changes", "{changes?: [{seq, id, kind, op, from?, to?, fields?, reason?, hand?, at}], resync?, snapshot?, cursor}",
			"A consumer's committed changes after its cursor, or a resync when it was away too long.", user),
			func(ctx context.Context, p changesParams) (ChangesPage, error) {
				return m.Changes(ctx, p.At, p.Consumer, int(p.Limit))
			}),
		module.Handle(quiet("todos.ack", "nil", "Advances a consumer's cursor once it has handled the changes up to seq.", user, CodeNoTask),
			func(ctx context.Context, p ackParams) (none, error) { return nil, m.Ack(ctx, p.At, p.Consumer, p.Seq) }),
		module.Handle(write("todos.claim", "nil",
			"The cutover: the daemon writes the store from now on. Only the release that removes auto-core's writer calls it.",
			user, CodeNotWritable, CodeInvalid),
			func(ctx context.Context, p claimParams) (none, error) { return nil, m.Claim(ctx, p.At, p.Opts) }),
		module.Handle(write("todos.release", "nil", "The cutover's rollback: the daemon only reads the store again.", user),
			func(ctx context.Context, p atParams) (none, error) { return nil, m.Release(ctx, p.At) }),
		module.Handle(read("todos.vars", "[{name, value?, builtin}]", "The variables a ref may start with, for an at's store."),
			func(ctx context.Context, p atParams) ([]Var, error) { return m.Vars(ctx, p.At) }),
		module.Handle(quiet("todos.set_var", "nil", "Sets or removes a user variable; built-ins are read-only.", user, CodeInvalid),
			func(ctx context.Context, p setVarParams) (none, error) { return nil, m.SetVar(ctx, p.Name, p.Value) }),
	}
	for i := range verbs {
		if verbs[i].Event != nil {
			verbs[i].Events = changedEvent
		}
	}
	return verbs
}

// changedEvent is the todos.changed a write logs. The workspace is the at's; a {root} has none, so
// the detail carries it. The detail names the task when the verb has one: its id param, or the
// task a todos.add made.
func changedEvent(_ context.Context, params []any, result any) []module.Event {
	var at At
	if len(params) > 0 {
		_ = at.DecodeParam(params[0])
	}
	id := ""
	if t, ok := result.(Task); ok {
		id = t.ID
	} else if len(params) > 1 {
		id, _ = params[1].(string)
	}
	detail := id
	if at.Workspace == "" && at.Root != "" {
		detail = at.Root
		if id != "" {
			detail += "#" + id
		}
	}
	return []module.Event{{Kind: "todos.changed", Workspace: at.Workspace, Detail: detail}}
}
