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
	CodeUnclaimed   int64 = -32077 // the store was released (todos.release): only todos.claim lets the daemon write it again
	// codeNoWorkspace is the daemon's shared code (rpc's CodeNoSuchWorkspace).
	codeNoWorkspace int64 = -32060
)

// protocol is the protocol the todos verbs arrived in.
const protocol = 20

// protocolMigrateRefs is the protocol todos.migrate_refs arrived in (ADR 1792015069).
const protocolMigrateRefs = 22

// protocolWorkspaceVars is the protocol variables moved to the workspace in (ADR 1792132105):
// workspace.vars, workspace.set_var, workspace.of and todos.set_kb; a variable's source "kb" and its
// conflict. A session below it reads "store" for "kb".
const protocolWorkspaceVars = 24

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
			Message: "this todo store was released (todos.release): AutoDoc only reads it",
			Next:    "todos.claim it to write it again"},
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
		At    At     `param:"at"`
		Spec  Spec   `param:"spec" doc:"{title, id?, description?, status?, due?, priority?, assignee?, tags?, adr?, review?, blocked?}"`
		Scope string `param:"scope,optional" doc:"with an assignee, the assigner's scope, as todos.assign's (protocol 30)"`
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
		Scope    string `param:"scope,optional" doc:"the assigner's scope, at most 256 bytes: kept on the change and the snapshot, never read (protocol 30)"`
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
		Scoped   bool   `param:"scoped,optional" doc:"the consumer reads each assignment's scope and delivers only its own; without it, a scoped assignment comes without its assignee (protocol 33)"`
	}
	ackParams struct {
		At       At     `param:"at"`
		Consumer string `param:"consumer"`
		Seq      int64  `param:"seq"`
	}
	claimParams struct {
		At   At        `param:"at"`
		Opts ClaimOpts `param:"opts,optional" doc:"{legacy_workspace}: the directory $WORKSPACE named when the store's refs were written, before AutoDoc owned it"`
	}
	migrateParams struct {
		At   At          `param:"at"`
		Opts MigrateOpts `param:"opts" doc:"{plan: true} lists the store's $WORKSPACE refs and where each exists; {plan: true, worktrees: true} lists its refs into removed git worktrees and their main-worktree copies; {apply: [...]} rewrites exactly the listed ones"`
	}
	setVarParams struct {
		Name  string     `param:"name"`
		Value string     `param:"value" doc:"\"\" removes it"`
		Opts  SetVarOpts `param:"opts,optional" doc:"{at, scope}: scope global (the default without at), store (the at's store's workspace's KB file, a path relative to the KB root; the default with at) or machine (this machine's value for that workspace)"`
	}
	wsVarsParams struct {
		Workspace string `param:"workspace"`
	}
	wsSetVarParams struct {
		Workspace string `param:"workspace"`
		Name      string `param:"name"`
		Value     string `param:"value" doc:"\"\" removes it"`
		Opts      struct {
			Scope       string `json:"scope,omitempty"`
			MachineOnly bool   `json:"machine_only,omitempty"`
		} `param:"opts,optional" doc:"{scope, machine_only}: scope machine (this machine's absolute value; the default) or kb (the KB's .autodoc-vars.toml, stored relative to the KB root); machine_only with scope machine also declares the name machine-only in the KB's file, so other machines report it as a value to set and a migration conflict on it is settled"`
	}
	ofParams struct {
		Path string `param:"path" doc:"an absolute file path"`
	}
	setKBParams struct {
		At        At     `param:"at"`
		Workspace string `param:"workspace" doc:"the workspace the store belongs to from now on"`
	}
)

// Owned is the workspace a file belongs to (workspace.of): "" for none.
type Owned struct {
	Workspace string `json:"workspace,omitempty"`
	Root      string `json:"root,omitempty"`
}

// none is the result of a verb that answers nothing.
type none = any

// Verbs are the todos.* verbs. Every one names its store by its first param (At); the host
// resolves nothing for them, so they are ScopeGlobal.
func (m *Manager) Verbs() []module.Verb {
	// read is a verb that changes no store; quiet, one that changes daemon state the view does not
	// show, its effect named (ADR 1792004705 §4.1); write, one whose success logs todos.changed, so
	// every view of the store refreshes. settings marks a write that moves which store the daemon
	// writes, not a task.
	read := func(verb, result, doc string, codes ...int64) module.Contract {
		return module.Contract{Verb: verb, Effect: module.EffectRead, Since: protocol, Audience: module.AudienceAgent, Result: result,
			Errors: append([]int64{codeNoWorkspace}, codes...), Doc: doc}
	}
	quiet := func(verb string, effect module.Effect, result, doc string, audience module.Audience, codes ...int64) module.Contract {
		c := read(verb, result, doc, codes...)
		c.Audience, c.Effect = audience, effect
		return c
	}
	settings := func(c module.Contract) module.Contract { c.Effect = module.EffectSettings; return c }
	write := func(verb, result, doc string, audience module.Audience, codes ...int64) module.Contract {
		c := quiet(verb, module.EffectWrite, result, doc, audience, codes...)
		c.Event = &module.EventSpec{Kind: "todos.changed"}
		return c
	}
	agent, user := module.AudienceAgent, module.AudienceUser
	writes := []int64{CodeNotWritable, CodeUnclaimed}
	task := "task: {id, version, status, title, …, rev, path, description}"
	verbs := []module.Verb{
		module.Handle(read("todos.resolve", "{store_id, store, root, override?, writable, claimed, released?}",
			"Where an at's store is, and whether the daemon may write it."),
			func(ctx context.Context, p atParams) (Resolved, error) { return m.Resolve(ctx, p.At) }),
		module.Handle(read("todos.open", "{store_id, store, scaffolded: false}",
			"Checks that an at's store exists. A kb_root is accepted and ignored: $KB_ROOT is the store's workspace's.", CodeNotAStore),
			func(ctx context.Context, p openParams) (Opened, error) { return m.Open(ctx, p.At, p.Opts) }),
		module.Handle(write("todos.scaffold", "{store_id, store, scaffolded}",
			"Creates the at's .todo-list and its buckets.", agent, append(writes, CodeExists)...),
			func(ctx context.Context, p atParams) (Opened, error) { return m.Scaffold(ctx, p.At) }),
		module.Rooted(module.Handle(quiet("todos.register", module.EffectSettings, "{new}",
			"Enrolls a project root that is no AutoDoc workspace, so its .todo-list may be written; allowed before any claim.",
			agent, CodeInvalid),
			func(ctx context.Context, p registerParams) (map[string]bool, error) {
				fresh, err := m.Register(ctx, p.Root)
				return map[string]bool{"new": fresh}, err
			})),
		module.Rooted(module.Handle(settings(write("todos.set_dir", "{store_id, store, root, override?, writable, claimed, released?}",
			"Points an at's root at another store directory, or back at <root>/.todo-list.", user, CodeInvalid)),
			func(ctx context.Context, p setDirParams) (Resolved, error) { return m.SetDir(ctx, p.At, p.Dir) })),
		module.Handle(read("todos.stores", "[{id, path, roots, claimed, released?, last_use}]", "The stores the daemon knows."),
			func(ctx context.Context, _ struct{}) ([]StoreInfo, error) { return m.Stores(ctx) }),
		module.Rooted(module.Handle(settings(write("todos.move_store", "{store_id, store, root, override?, writable, claimed, released?}",
			"Moves an at's store to a new directory; its claim, change log and cursors move with it.",
			user, append(writes, CodeExists)...)),
			func(ctx context.Context, p moveParams) (Resolved, error) { return m.MoveStore(ctx, p.At, p.To) })),
		module.Handle(read("todos.list", "{tasks: ["+task+"], malformed: [{path, reason}], partitions?: [{month, count}], cursor}",
			"The active buckets' tasks; archives only by month, on request.", CodeNotAStore, CodeInvalid),
			func(ctx context.Context, p listParams) (Listing, error) { return m.List(ctx, p.At, p.Opts) }),
		module.Handle(read("todos.get", task, "One task, with its rev and body.", CodeNoTask, CodeNotAStore),
			func(ctx context.Context, p idParams) (Task, error) { return m.Get(ctx, p.At, p.ID) }),
		module.Handle(write("todos.add", task,
			"Creates a task. With an assignee it is an assignment: open becomes in-progress, and the assignee is told.",
			agent, append(writes, CodeInvalid, CodeExists)...),
			func(ctx context.Context, p addParams) (Task, error) { return m.Add(ctx, p.At, p.Spec, p.Scope) }),
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
				return m.Assign(ctx, p.At, p.ID, p.Rev, p.Assignee, p.Reason, p.Scope)
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
		module.Handle(quiet("todos.changes", module.EffectRead, "{changes?: [{seq, id, kind, op, from?, to?, fields?, reason?, scope?, hand?, at}], resync?, snapshot?, cursor}",
			"A consumer's committed changes after its cursor, or a resync when it was away too long.", user),
			func(ctx context.Context, p changesParams) (ChangesPage, error) {
				page, err := m.Changes(ctx, p.At, p.Consumer, int(p.Limit))
				if err != nil || p.Scoped {
					return page, err
				}
				return page.ForUnscopedReader(), nil
			}),
		module.Handle(quiet("todos.ack", module.EffectWrite, "nil", "Advances a consumer's cursor once it has handled the changes up to seq.", user, CodeNoTask),
			func(ctx context.Context, p ackParams) (none, error) { return nil, m.Ack(ctx, p.At, p.Consumer, p.Seq) }),
		module.Handle(settings(write("todos.claim", "nil",
			"Lets the daemon write a store todos.release gave back. A store never released needs none: its first write claims it.",
			user, CodeNotWritable, CodeInvalid)),
			func(ctx context.Context, p claimParams) (none, error) { return nil, m.Claim(ctx, p.At, p.Opts) }),
		module.Handle(settings(write("todos.release", "nil", "Gives the store back: the daemon only reads it, and a write no longer claims it, until todos.claim.", user)),
			func(ctx context.Context, p atParams) (none, error) { return nil, m.Release(ctx, p.At) }),
		module.Handle(write("todos.migrate_refs", "{candidates?: [{kind?, id, path, field, index, ref, to, under_workspace, under_project, suggested}], rewritten, skipped?}",
			"Migrates refs a plan lists, Lua-era $WORKSPACE refs (to $PROJECT_ROOT) or refs into removed git worktrees (kind worktree, to the main worktree's copy): an apply rewrites exactly the reviewed list, journaled.",
			user, append(writes, CodeInvalid)...),
			func(ctx context.Context, p migrateParams) (MigrateResult, error) {
				return m.MigrateRefs(ctx, p.At, p.Opts)
			}),
		module.Handle(read("todos.vars", "[{name, value?, builtin, derived?, source?, machine?, conflict?}]",
			"The variables an at's store's refs may start with: its workspace's, the same from every route to the store; each effective value and its source."),
			func(ctx context.Context, p atParams) ([]Var, error) {
				vars, err := m.Vars(ctx, p.At)
				return compatVars(ctx, vars), err
			}),
		module.Handle(quiet("todos.set_var", module.EffectSettings, "nil",
			"Sets or removes a variable: globally, in an at's store's workspace's KB file, or for this machine; a KB or machine value may override a built-in.",
			user, CodeInvalid, CodeNotWritable, CodeUnclaimed),
			func(ctx context.Context, p setVarParams) (none, error) {
				return nil, m.SetVar(ctx, p.Name, p.Value, p.Opts)
			}),
		module.Handle(read("workspace.vars", "[{name, value?, builtin, derived?, source?, machine?, conflict?}]",
			"A workspace's variables (ADR 1792132105): the ones every document of it, its todo stores' tasks included, resolves $NAME refs with; each effective value and its source. \"\" is no workspace's: $HOME and the global variables, which a file no workspace owns resolves with."),
			func(ctx context.Context, p wsVarsParams) ([]Var, error) { return m.WorkspaceVars(ctx, p.Workspace) }),
		module.Handle(quiet("workspace.set_var", module.EffectSettings, "nil",
			"Sets or removes a workspace's variable: this machine's value (scope machine, absolute) or the KB's (scope kb, relative to the KB root, carried by git); machine_only declares the name machine-only in the KB's file.",
			user, CodeInvalid),
			func(ctx context.Context, p wsSetVarParams) (none, error) {
				return nil, m.SetVar(ctx, p.Name, p.Value, SetVarOpts{Workspace: p.Workspace, Scope: p.Opts.Scope, MachineOnly: p.Opts.MachineOnly})
			}),
		module.Handle(read("workspace.of", "{workspace?, root?}",
			"The workspace a file belongs to, the owner its refs resolve with: a todo store's file, the store's workspace; any other file, the deepest workspace containing it; none otherwise."),
			func(ctx context.Context, p ofParams) (Owned, error) {
				ws, root, err := m.Owner(ctx, p.Path)
				return Owned{Workspace: ws, Root: root}, err
			}),
		module.Handle(settings(write("todos.set_kb", "nil",
			"Records the workspace an at's store belongs to: links a store outside every workspace, or moves one; its refs resolve with that workspace's variables.",
			user, CodeInvalid)),
			func(ctx context.Context, p setKBParams) (none, error) { return nil, m.SetKB(ctx, p.At, p.Workspace) }),
	}
	for i := range verbs {
		if verbs[i].Event != nil {
			verbs[i].Events = changedEvent
		}
		switch verbs[i].Verb {
		case "todos.migrate_refs":
			verbs[i].Since = protocolMigrateRefs
		case "workspace.vars", "workspace.set_var", "workspace.of", "todos.set_kb":
			verbs[i].Since = protocolWorkspaceVars
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

// compatVars is vars as a session below protocolWorkspaceVars reads them: a KB-file value's source is
// "store", as the store's own file's was.
func compatVars(ctx context.Context, vars []Var) []Var {
	if p := module.ProtocolFrom(ctx); p == 0 || p >= protocolWorkspaceVars {
		return vars
	}
	for i := range vars {
		if vars[i].Source == "kb" {
			vars[i].Source = "store"
		}
	}
	return vars
}
