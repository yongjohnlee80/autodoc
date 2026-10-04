package rpc

import (
	"context"
	"fmt"
	"sort"

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
//	source, destination_connection   {engine, dsn, schema, remove}   an empty dsn keeps the stored one
//
// A key it does not know, or a value of the wrong shape, is InvalidParams: a client's typo is
// refused, never dropped.

var configureKeys = map[string]bool{"name": true, "include": true, "exclude": true, "schema": true,
	"text_extensions": true, "section_tokens": true, "embedding_policy": true, "provider": true,
	"destination": true, "vector_index": true, "view_args": true, "source": true, "destination_connection": true}

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

// databasesMap is a workspace's database settings as workspace.list reports them: each
// connection by role, as where it points, never its DSN.
func databasesMap(d Databases) map[string]any {
	out := map[string]any{"uid": d.UID, "destination": d.Destination, "vector_index": d.VectorIndex}
	args := map[string]any{}
	for k, v := range d.ViewArgs {
		args[k] = v
	}
	out["view_args"] = args
	for _, c := range d.Connections {
		key := c.Role
		if key == store.RoleDestination {
			key = "destination_connection"
		}
		out[key] = map[string]any{"engine": c.Engine, "host": c.Host, "database": c.Database, "user": c.User,
			"schema": c.Schema, "has_password": c.HasPassword}
	}
	return out
}

func (s *Server) configureVerbs() {
	s.handle("workspace.configure", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		name, err := argStr(p, 0, "workspace name")
		if err != nil {
			return nil, err
		}
		m, ok := p[1].(map[string]any)
		if !ok {
			return nil, invalid("settings must be a map")
		}
		c, err := changesOf(m)
		if err != nil {
			return nil, err
		}
		manager, ok := s.workspaces.(interface {
			Configure(context.Context, string, store.Changes) error
		})
		if !ok {
			return nil, errFixed
		}
		return nil, manager.Configure(ctx, name, c)
	}, false))
	// sys.capabilities is what this daemon offers beyond the core (core/edition): a client hides
	// what is false. A daemon whose workspaces say nothing offers nothing extra.
	s.handle("sys.capabilities", s.verb(0, 0, func(context.Context, *Workspace, []any) (any, error) {
		out := map[string]any{"databases": false}
		if c, ok := s.workspaces.(interface{ Capabilities() map[string]bool }); ok {
			for k, v := range c.Capabilities() {
				out[k] = v
			}
		}
		return out, nil
	}, false))
}
