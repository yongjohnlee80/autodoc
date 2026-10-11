package rpc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// workspace.configure saves a workspace's settings together (ADR 0214 §3): the TUI's Edit and
// Advanced tabs as one map, every key optional, an absent key left as it is.
//
//	name               string
//	include, exclude   [string]   set together
//	schema             string     "" removes it
//	text_extensions    [string]
//	section_tokens     int        0 the default
//	embedding_policy   string     always, when opened, never
//	provider           string     "" the daemon's
//	destination        string     sqlite or postgres
//	vector_index       string     hnsw or ivfflat; "" the default
//	view_args          map
//	abstract_chunk     bool       each document's abstract as a chunk of its own; off by default
//	demote_superseded  bool       a superseded document's hits below its successor's; off by default
//	source, destination_connection   {engine, dsn, schema, remove}   an empty dsn keeps the stored one
//
// A key it does not know, or a value of the wrong shape, is InvalidParams: a client's typo is
// refused, never dropped.

var configureKeys = map[string]bool{"name": true, "include": true, "exclude": true, "schema": true,
	"text_extensions": true, "section_tokens": true, "embedding_policy": true, "provider": true,
	"destination": true, "vector_index": true, "view_args": true, "source": true, "destination_connection": true,
	"abstract_chunk": true, "demote_superseded": true}

// changesOf reads workspace.configure's settings map.
func changesOf(m map[string]any) (store.Changes, error) {
	var c store.Changes
	var unknown []string
	for k := range m {
		if !configureKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return c, invalid(fmt.Sprintf("unknown settings %v", unknown))
	}
	str := func(key string) (*string, error) {
		v, ok := m[key]
		if !ok {
			return nil, nil
		}
		s, ok := v.(string)
		if !ok {
			return nil, invalid(key + " must be a string")
		}
		return &s, nil
	}
	list := func(key string) (*[]string, error) {
		v, ok := m[key]
		if !ok {
			return nil, nil
		}
		l, err := strList(v, key)
		return &l, err
	}
	var err error
	if c.Name, err = str("name"); err != nil {
		return c, err
	}
	if c.Include, err = list("include"); err != nil {
		return c, err
	}
	if c.Exclude, err = list("exclude"); err != nil {
		return c, err
	}
	if c.SchemaPath, err = str("schema"); err != nil {
		return c, err
	}
	if c.TextExtensions, err = list("text_extensions"); err != nil {
		return c, err
	}
	if c.EmbeddingPolicy, err = str("embedding_policy"); err != nil {
		return c, err
	}
	if c.Provider, err = str("provider"); err != nil {
		return c, err
	}
	if c.Destination, err = str("destination"); err != nil {
		return c, err
	}
	if c.VectorIndex, err = str("vector_index"); err != nil {
		return c, err
	}
	for key, dst := range map[string]**bool{"abstract_chunk": &c.AbstractChunk, "demote_superseded": &c.DemoteSuperseded} {
		if v, ok := m[key]; ok {
			b, ok := v.(bool)
			if !ok {
				return c, invalid(key + " must be true or false")
			}
			*dst = &b
		}
	}
	if v, ok := m["section_tokens"]; ok {
		n, err := argInt([]any{v}, 0, "section_tokens")
		if err != nil {
			return c, err
		}
		tokens := int(n)
		c.SectionTokens = &tokens
	}
	if v, ok := m["view_args"]; ok {
		args, ok := v.(map[string]any)
		if !ok {
			return c, invalid("view_args must be a map")
		}
		c.ViewArgs = &args
	}
	for key, dst := range map[string]**store.ConnectionSpec{"source": &c.Source, "destination_connection": &c.DestinationConn} {
		v, ok := m[key]
		if !ok {
			continue
		}
		spec, err := connectionSpecOf(key, v)
		if err != nil {
			return c, err
		}
		*dst = spec
	}
	return c, nil
}

func connectionSpecOf(key string, v any) (*store.ConnectionSpec, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, invalid(key + " must be a map")
	}
	var spec store.ConnectionSpec
	for k, x := range m {
		switch k {
		case "remove":
			b, ok := x.(bool)
			if !ok {
				return nil, invalid(key + ".remove must be a boolean")
			}
			spec.Remove = b
		case "engine", "dsn", "schema":
			s, ok := x.(string)
			if !ok {
				return nil, invalid(key + "." + k + " must be a string")
			}
			switch k {
			case "engine":
				spec.Engine = s
			case "dsn":
				spec.DSN = s
			default:
				spec.Schema = s
			}
		default:
			return nil, invalid(fmt.Sprintf("unknown %s setting %q", key, k))
		}
	}
	return &spec, nil
}

// configureEvents are the events a workspace.configure that succeeded logs: one for each thing it
// changed, of the kind that thing's own verb logs, so a client following the log does what it does
// for that verb. A rename comes first, with the new name; what follows is about the workspace under
// it. The database settings are one event, workspace.databases, whose detail is empty: a
// connection is never in the log.
func configureEvents(_ context.Context, p []any, _ any) []module.Event {
	name, _ := p[0].(string)
	m, _ := p[1].(map[string]any)
	var out []module.Event
	if to, ok := m["name"].(string); ok && to != name {
		out = append(out, module.Event{Kind: "workspace.renamed", Workspace: name, Detail: to})
		name = to
	}
	for _, f := range []struct {
		kind   string
		keys   []string
		detail string
	}{
		{"workspace.patterns", []string{"include", "exclude"}, ""},
		{"workspace.schema", []string{"schema"}, ""},
		{"workspace.text_extensions", []string{"text_extensions"}, ""},
		{"workspace.section_size", []string{"section_tokens"}, ""},
		{"workspace.embedding_policy", []string{"embedding_policy"}, "embedding_policy"},
		{"workspace.provider", []string{"provider"}, "provider"},
		{"workspace.databases", []string{"destination", "vector_index", "view_args", "source", "destination_connection"}, ""},
		{"workspace.retrieval", []string{"abstract_chunk", "demote_superseded"}, ""},
	} {
		if !slices.ContainsFunc(f.keys, func(k string) bool { _, ok := m[k]; return ok }) {
			continue
		}
		e := module.Event{Kind: f.kind, Workspace: name}
		if f.detail != "" {
			e.Detail, _ = m[f.detail].(string)
		}
		out = append(out, e)
	}
	return out
}

type workspaceConfigure struct {
	Workspace string    `param:"workspace"`
	Settings  optionMap `param:"settings" doc:"any of name, include+exclude, schema, text_extensions, section_tokens, embedding_policy, provider, abstract_chunk, demote_superseded, and (where sys.capabilities says databases) destination, vector_index, view_args, source and destination_connection ({engine, dsn, schema} or {remove: true}; a blank dsn keeps the stored one)"`
}

// The kinds of event workspace.configure may log, one for each setting it changed (configureEvents).
const configureKinds = "workspace.renamed | workspace.patterns | workspace.schema | workspace.text_extensions | " +
	"workspace.section_size | workspace.embedding_policy | workspace.provider | workspace.databases | workspace.retrieval"

func (s *Server) configureVerb() module.Verb {
	v := module.Handle(module.Contract{Verb: "workspace.configure", Since: 9, Audience: module.AudienceUser,
		Errors: []int64{CodeNoSuchWorkspace, CodeConflict, CodeNotFound, CodeUnsupported, CodeProviderRefused},
		Event:  &module.EventSpec{Kind: configureKinds, Workspace: "workspace"},
		Doc:    "Saves the settings at once: all of them or none. An unknown key is invalid params. Logs the event each changed setting's own verb logs, a rename first."},
		func(ctx context.Context, p workspaceConfigure) (any, error) {
			m, ok := p.Settings.raw.(map[string]any)
			if !ok {
				return nil, invalid("settings must be a map")
			}
			c, err := changesOf(m)
			if err != nil {
				return nil, err
			}
			if s.setters.configure == nil {
				return nil, errFixed
			}
			return nil, s.setters.configure.Configure(ctx, p.Workspace, c)
		})
	v.Events = configureEvents
	return v
}

// RegistrationsMap is a build's registrations as sys.capabilities reports them: each chunker's
// version by extension, each format's deriver by format, and their fingerprint.
func RegistrationsMap(t registrations.Tables) map[string]any {
	chunkers, formats := map[string]any{}, map[string]any{}
	for ext, v := range t.Chunkers {
		chunkers[ext] = v
	}
	for f, d := range t.Formats {
		formats[f] = map[string]any{"id": d.ID, "version": d.Version}
	}
	return map[string]any{"chunkers": chunkers, "formats": formats, "fingerprint": t.Fingerprint()}
}

// ErrNoRegistrations is a Protocol 10 sys.capabilities without its registrations: a protocol
// error, never read as none, or every client built with registrations would offer a restart.
var ErrNoRegistrations = errors.New("rpc: sys.capabilities has no registrations: a protocol error")

// RegistrationsOf reads the registrations of a sys.capabilities answer.
func RegistrationsOf(caps any) (registrations.Tables, error) {
	m, _ := caps.(map[string]any)
	r, ok := m["registrations"].(map[string]any)
	if !ok {
		return registrations.Tables{}, ErrNoRegistrations
	}
	cs, ok1 := r["chunkers"].(map[string]any)
	fs, ok2 := r["formats"].(map[string]any)
	if !ok1 || !ok2 {
		return registrations.Tables{}, ErrNoRegistrations
	}
	t := registrations.Tables{Chunkers: map[string]string{}, Formats: map[string]registrations.Format{}}
	for ext, v := range cs {
		s, ok := v.(string)
		if !ok {
			return registrations.Tables{}, fmt.Errorf("%w: chunker %q", ErrNoRegistrations, ext)
		}
		t.Chunkers[ext] = s
	}
	for f, v := range fs {
		d, _ := v.(map[string]any)
		id, ok1 := d["id"].(string)
		ver, ok2 := d["version"].(string)
		if !ok1 || !ok2 {
			return registrations.Tables{}, fmt.Errorf("%w: format %q", ErrNoRegistrations, f)
		}
		t.Formats[f] = registrations.Format{ID: id, Version: ver}
	}
	return t, nil
}
