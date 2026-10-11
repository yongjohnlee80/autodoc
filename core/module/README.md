# core/module

How an AutoDoc feature offers verbs (ADR 1791719881). A feature's core package has a `module.go`
that implements `module.Module`:

```go
func (m *Manager) Name() string { return "todos" }

func (m *Manager) Verbs() []module.Verb {
	return []module.Verb{
		module.Handle(module.Contract{Verb: "todos.get", Since: 20, Audience: module.AudienceAgent,
			Result: "task", Errors: []int64{CodeNotFound}, Doc: "One task, with its rev and body."},
			m.get),
	}
}

func (m *Manager) Errors() []module.PublicError {
	return []module.PublicError{{Err: ErrNotFound, Code: CodeNotFound, Name: "todos.not_found",
		Message: "no such task", Next: "list the tasks again"}}
}
```

- **Params come from the handler's type.** `Handle[P, R]` reads P's exported fields in order:
  `param:"name"` names one, `param:"name,optional"` makes a trailing one optional, `doc:"…"`
  documents it. A field may be `string`, `bool`, `int`/`int64`, `[]byte`, `[]string`,
  `map[string]any`, `any`, a struct decoded from a map by its json tags, or a type whose pointer
  implements `Decoder`.
- **Results are json-tagged values.** The host carries them with `Wire`.
- **Errors carry their code once.** The code is the wire identity. `Name` and `Next` appear in
  `autodoc --contracts` and AGENTS.md. A module's codes must sit in its range (`codes.go`).
- **Events.** `Contract.Event` names the kind and the params holding the workspace and the
  detail. `Verb.Events` resolves them itself when they are inside a param or in the result.
- **No I/O in `Name`, `Verbs` or `Errors`.** `autodoc --contracts` builds every Module with no
  dependencies, to list them without a daemon.

The host (rpc) resolves `ScopeWorkspace` and puts the workspace, the session's protocol and its
client token in the handler's context: `WorkspaceFrom`, `ProtocolFrom` and `ClientFrom`.
