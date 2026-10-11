package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// Catalog is what sys.contracts and autodoc --contracts answer: the verbs a session at Protocol may
// call, sorted, with the daemon's MinProtocol, and every code those verbs may answer. Its JSON (and
// its wire form) gives each contract's scope and audience by name.
type Catalog struct {
	Protocol, MinProtocol int64
	Verbs                 []module.Contract
	Errors                []CatalogError
}

// CatalogError is one code a client may see: its stable name, what the client does next, and the
// messages it carries. The code is all of it that identifies an error on the wire.
type CatalogError struct {
	Code     int64    `json:"code"`
	Name     string   `json:"name"`
	Next     string   `json:"next,omitempty"`
	Messages []string `json:"messages,omitempty"`
}

// codeDocs name the codes rpc answers itself, golib's among them, and say what a client does next.
// Their messages are publicErrs'.
var codeDocs = []struct {
	code       int64
	name, next string
}{
	{golibrpc.CodeAccessDenied, "rpc.access_denied", "call it on the unix socket: it is for a local peer"},
	{CodeProtocolMismatch, "rpc.protocol_mismatch", "the daemon serves other protocols: autodoc --version, then restart it, or reconnect declaring one it serves"},
	{CodeHandshakeRequired, "rpc.handshake_required", "say sys.hello first"},
	{CodeNoSuchWorkspace, "rpc.no_such_workspace", "call workspace.list and use a listed name"},
	{CodeNotFound, "rpc.not_found", "refresh the listing: the path is wrong or not indexed (index.list), or the provider or ranker is gone"},
	{CodeConflict, "rpc.conflict", "doc.read, then merge or retry; for a name, choose another"},
	{CodeCursorExpired, "rpc.cursor_expired", "re-list: take index.status's cursor (sys.hello's events head for sys.events), list, and resume from it"},
	{CodeUnsupported, "rpc.unsupported", "this daemon or the workspace's filesystem cannot: do without it, or search with lexical among the stages"},
	{CodeCommitted, "rpc.committed", "the write landed: doc.read and compare, never re-send"},
	{CodeEmbedFailed, "rpc.embed_failed", "retry later, or search with lexical among the stages"},
	{CodeProviderRefused, "rpc.provider_refused", "switch provider or ranker, or fix its key or base URL"},
	{CodeSwitching, "rpc.switching", "a new model is filling: search with lexical among the stages until it is ready"},
	{CodeRankUnavailable, "rpc.rank_unavailable", "retry later, or leave rerank out of the stages for an unranked answer"},
	{golibrpc.CodeMethodNotFound, "rpc.method_not_found", "the verb is unknown, or newer than the session's protocol: sys.capabilities lists the verbs it may call"},
	{golibrpc.CodeInvalidParams, "rpc.invalid_params", "the message says which param and why; check the verb's contract"},
	{golibrpc.CodeInternal, "rpc.internal", "a failure the daemon's log details: retry, or report it"},
}

// Catalog is the catalog at protocol p of the verbs filter names: "" or "*" for all, "domain.*" for
// a domain's, or one verb by name. A filter lists the codes its verbs name and invalid params.
func (s *Server) Catalog(p int64, filter string) Catalog {
	c := Catalog{Protocol: p, MinProtocol: MinProtocol, Verbs: []module.Contract{}, Errors: []CatalogError{}}
	codes := map[int64]bool{}
	for _, name := range s.Verbs() {
		v := s.verbs[name]
		if v.Since > p || !selects(filter, name) {
			continue
		}
		c.Verbs = append(c.Verbs, v.Contract)
		codes[golibrpc.CodeInvalidParams] = true
		for _, code := range v.Errors {
			codes[code] = true
		}
	}
	all := filter == "" || filter == "*"
	byCode := map[int64]*CatalogError{}
	add := func(code int64, name, next, message string) {
		if !all && !codes[code] {
			return
		}
		e, ok := byCode[code]
		if !ok {
			e = &CatalogError{Code: code, Name: name, Next: next}
			byCode[code] = e
		}
		if message != "" && !slices.Contains(e.Messages, message) {
			e.Messages = append(e.Messages, message)
		}
	}
	for _, d := range codeDocs {
		add(d.code, d.name, d.next, "")
	}
	for _, pe := range publicErrs {
		add(pe.code, "", "", pe.message)
	}
	for _, e := range s.moduleErrs {
		add(e.Code, e.Name, e.Next, e.Message)
	}
	for _, e := range byCode {
		c.Errors = append(c.Errors, *e)
	}
	sort.Slice(c.Errors, func(i, j int) bool { return c.Errors[i].Code > c.Errors[j].Code })
	return c
}

// selects reports whether filter names verb.
func selects(filter, verb string) bool {
	switch {
	case filter == "" || filter == "*":
		return true
	case strings.HasSuffix(filter, ".*"):
		return strings.HasPrefix(verb, strings.TrimSuffix(filter, "*"))
	}
	return filter == verb
}

// contractJSON is a contract as the catalog carries it: Contract's own fields, with its scope and
// audience by name.
type contractJSON struct {
	Verb     string            `json:"verb"`
	Since    int64             `json:"since"`
	Scope    string            `json:"scope"`
	Audience string            `json:"audience"`
	Params   []module.Param    `json:"params"`
	Result   string            `json:"result,omitempty"`
	Errors   []int64           `json:"errors,omitempty"`
	Event    *module.EventSpec `json:"event,omitempty"`
	Doc      string            `json:"doc,omitempty"`
}

type catalogJSON struct {
	Protocol    int64          `json:"protocol"`
	MinProtocol int64          `json:"min_protocol"`
	Verbs       []contractJSON `json:"verbs"`
	Errors      []CatalogError `json:"errors"`
}

func (c Catalog) wire() catalogJSON {
	out := catalogJSON{Protocol: c.Protocol, MinProtocol: c.MinProtocol, Verbs: []contractJSON{}, Errors: c.Errors}
	if out.Errors == nil {
		out.Errors = []CatalogError{}
	}
	for _, v := range c.Verbs {
		params := v.Params
		if params == nil {
			params = []module.Param{}
		}
		out.Verbs = append(out.Verbs, contractJSON{Verb: v.Verb, Since: v.Since, Scope: v.Scope.String(), Audience: v.Audience.String(),
			Params: params, Result: v.Result, Errors: v.Errors, Event: v.Event, Doc: v.Doc})
	}
	return out
}

// MarshalJSON is c as --contracts prints it and sys.contracts answers it.
func (c Catalog) MarshalJSON() ([]byte, error) { return json.Marshal(c.wire()) }

// Markdown is c as AGENTS.md's tables: the verbs by domain (verb, params, result, since, audience),
// then the errors (code, name, next, messages).
func (c Catalog) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Verbs at protocol %d; the daemon serves sessions from protocol %d.\n", c.Protocol, c.MinProtocol)
	domain := ""
	for _, v := range c.Verbs {
		if d, _, _ := strings.Cut(v.Verb, "."); d != domain {
			domain = d
			fmt.Fprintf(&b, "\n### `%s`\n\n| verb | params | result | since | audience |\n| --- | --- | --- | --- | --- |\n", d)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %d | %s |\n", v.Verb, paramsCell(v.Params), resultCell(v), v.Since, audienceCell(v))
	}
	b.WriteString("\n### Errors\n\n| code | name | next | messages |\n| --- | --- | --- | --- |\n")
	for _, e := range c.Errors {
		msgs := make([]string, len(e.Messages))
		for i, m := range e.Messages {
			msgs[i] = "\"" + m + "\""
		}
		fmt.Fprintf(&b, "| %d | `%s` | %s | %s |\n", e.Code, e.Name, cell(e.Next), cell(strings.Join(msgs, "; ")))
	}
	return b.String()
}

func paramsCell(ps []module.Param) string {
	if len(ps) == 0 {
		return "—"
	}
	out := make([]string, len(ps))
	for i, p := range ps {
		opt := ""
		if p.Optional {
			opt = "?"
		}
		out[i] = fmt.Sprintf("`%s`%s %s", p.Name, opt, p.Type)
		if p.Doc != "" {
			out[i] += " (" + p.Doc + ")"
		}
	}
	return cell(strings.Join(out, ", "))
}

func resultCell(v module.Contract) string {
	var parts []string
	if v.Result != "" {
		parts = append(parts, "`"+v.Result+"`")
	}
	if v.Doc != "" {
		parts = append(parts, v.Doc)
	}
	if v.Event != nil {
		parts = append(parts, "Logs `"+v.Event.Kind+"`.")
	}
	if len(v.Errors) > 0 {
		codes := make([]string, len(v.Errors))
		for i, c := range v.Errors {
			codes[i] = fmt.Sprint(c)
		}
		parts = append(parts, "Codes: "+strings.Join(codes, ", ")+".")
	}
	if len(parts) == 0 {
		return "—"
	}
	return cell(strings.Join(parts, " "))
}

func audienceCell(v module.Contract) string {
	out := v.Audience.String()
	if v.Audience == module.AudienceUser {
		out = "for people's tools"
	}
	if v.Scope == module.ScopeLocalPeer {
		out += "; unix socket only"
	}
	return out
}

// cell is s in a table cell: one line, its pipes escaped.
func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "|", `\|`)
}

// registerContracts serves sys.contracts([filter]): the catalog at the session's protocol.
func (s *Server) registerContracts() {
	s.handle("sys.contracts", s.verb(0, 1, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		filter := ""
		if len(p) > 0 {
			var err error
			if filter, err = argStr(p, 0, "filter"); err != nil {
				return nil, err
			}
		}
		return module.Wire(s.Catalog(protocolOf(ctx), filter).wire())
	}, false))
}
