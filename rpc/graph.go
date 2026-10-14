package rpc

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// THE GRAPH — the links between a workspace's documents: those a file makes, those made to it, a
// neighbourhood a few links out, the links that reach nothing, and where one link reaches now.

type (
	graphPath struct {
		Workspace string    `param:"workspace"`
		Path      string    `param:"path"`
		Options   optionMap `param:"options,optional" doc:"{kinds} (protocol 14)"`
	}
	graphNeighborhood struct {
		Workspace string    `param:"workspace"`
		Path      string    `param:"path"`
		Depth     int       `param:"depth"`
		Options   optionMap `param:"options,optional" doc:"{kinds} (protocol 14)"`
	}
	graphUnresolved struct {
		Workspace string    `param:"workspace"`
		Options   optionMap `param:"options,optional" doc:"{kinds} (protocol 14)"`
	}
	graphResolve struct {
		Workspace string `param:"workspace"`
		From      string `param:"from" doc:"the file the link is written in"`
		Raw       string `param:"raw" doc:"the link as written"`
	}
)

type linkJSON struct {
	Path     string `json:"path"`
	Raw      string `json:"raw"`
	Anchor   string `json:"anchor"`
	Kind     string `json:"kind"`
	Resolved bool   `json:"resolved"`
}

type neighborhoodJSON struct {
	Nodes []string   `json:"nodes"`
	Edges []edgeJSON `json:"edges"`
}

type edgeJSON struct {
	Src  string `json:"src"`
	Dst  string `json:"dst"`
	Kind string `json:"kind"`
}

type unresolvedJSON struct {
	Src    string `json:"src"`
	Raw    string `json:"raw"`
	Reason string `json:"reason"`
}

type resolvedJSON struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func (s *Server) graphVerbs() []module.Verb {
	agent, ws := module.AudienceAgent, module.ScopeWorkspace
	links := func(verb, doc string, backlinks bool) module.Verb {
		return module.Handle(module.Contract{Verb: verb, Effect: module.EffectRead, Since: 1, Scope: ws, Audience: agent,
			Result: "[{path, raw, anchor, kind, resolved}]", Errors: []int64{CodeNoSuchWorkspace, CodeNotFound}, Doc: doc},
			func(ctx context.Context, p graphPath) ([]linkJSON, error) {
				kinds, err := graphKinds(ctx, p.Options.raw)
				if err != nil {
					return nil, err
				}
				read := workspaceOf(ctx).Index().Store().Links
				if backlinks {
					read = workspaceOf(ctx).Index().Store().Backlinks
				}
				ls, err := read(ctx, p.Path, kinds)
				if err != nil {
					return nil, err
				}
				out := make([]linkJSON, len(ls))
				for i, l := range ls {
					out[i] = linkJSON{Path: l.Path, Raw: l.Raw, Anchor: l.Anchor, Kind: l.Kind, Resolved: l.Resolved}
				}
				return out, nil
			})
	}
	return []module.Verb{
		links("graph.links", "The links the file makes.", false),
		links("graph.backlinks", "The files that link to it.", true),
		module.Handle(module.Contract{Verb: "graph.neighborhood", Effect: module.EffectRead, Since: 1, Scope: ws, Audience: agent,
			Result: "{nodes, edges: [{src, dst, kind}]}", Errors: []int64{CodeNoSuchWorkspace, CodeNotFound},
			Doc: "The files within depth links."},
			func(ctx context.Context, p graphNeighborhood) (*neighborhoodJSON, error) {
				kinds, err := graphKinds(ctx, p.Options.raw)
				if err != nil {
					return nil, err
				}
				nb, err := workspaceOf(ctx).Index().Store().Neighborhood(ctx, p.Path, p.Depth, kinds)
				if err != nil {
					return nil, err
				}
				out := &neighborhoodJSON{Nodes: nb.Nodes, Edges: make([]edgeJSON, len(nb.Edges))}
				for i, e := range nb.Edges {
					out.Edges[i] = edgeJSON{Src: e.Src, Dst: e.Dst, Kind: e.Kind}
				}
				return out, nil
			}),
		module.Handle(module.Contract{Verb: "graph.unresolved", Effect: module.EffectRead, Since: 1, Scope: ws, Audience: agent,
			Result: "[{src, raw, reason}]", Errors: []int64{CodeNoSuchWorkspace}, Doc: "Links that name no file."},
			func(ctx context.Context, p graphUnresolved) ([]unresolvedJSON, error) {
				kinds, err := graphKinds(ctx, p.Options.raw)
				if err != nil {
					return nil, err
				}
				us, err := workspaceOf(ctx).Index().Store().Unresolved(ctx, kinds)
				if err != nil {
					return nil, err
				}
				out := make([]unresolvedJSON, len(us))
				for i, u := range us {
					out[i] = unresolvedJSON{Src: u.Src, Raw: u.Raw, Reason: u.Reason}
				}
				return out, nil
			}),
		// graph.resolve: where one link, as written in the file at from, reaches now, saved or not, as
		// the index will resolve it (Protocol 18)
		module.Handle(module.Contract{Verb: "graph.resolve", Effect: module.EffectRead, Since: 18, Scope: ws, Audience: agent,
			Result: "{path, reason}", Errors: []int64{CodeNoSuchWorkspace},
			Doc: "Where one link reaches now, saved or not, as the index resolves links: path \"\" with reason missing or ambiguous, or \"\" when raw is no link the index keeps (a URL, a heading of from)."},
			func(ctx context.Context, p graphResolve) (*resolvedJSON, error) {
				path, reason, err := workspaceOf(ctx).Index().ResolveLink(ctx, p.From, p.Raw)
				if err != nil {
					return nil, err
				}
				return &resolvedJSON{Path: path, Reason: reason}, nil
			}),
	}
}

// relationsSince is the protocol frontmatter relation links arrived in. A session below it sees the
// body links alone: a new kind of link changes what a graph answer means.
const relationsSince = 14

// graphKinds is the kinds of link a graph verb answers with: the body kinds below relationsSince,
// otherwise every kind, or those its {kinds} option names. Asking for kinds below relationsSince is
// refused, not ignored, so an old client never takes an answer it did not ask for.
func graphKinds(ctx context.Context, raw any) ([]string, error) {
	below := protocolOf(ctx) < relationsSince
	if raw != nil {
		opts, ok := raw.(map[string]any)
		if !ok {
			return nil, invalid("options must be a map")
		}
		if raw, ok := opts["kinds"]; ok {
			if below {
				return nil, invalid(fmt.Sprintf("kinds needs protocol %d", relationsSince))
			}
			list, ok := raw.([]any)
			if !ok {
				return nil, invalid("kinds must be a list of strings")
			}
			kinds := make([]string, 0, len(list))
			for _, k := range list {
				s, ok := k.(string)
				if !ok {
					return nil, invalid("kinds must be a list of strings")
				}
				kinds = append(kinds, s)
			}
			return kinds, nil
		}
	}
	if below {
		return index.BodyKinds, nil
	}
	return nil, nil
}
