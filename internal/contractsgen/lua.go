package main

import (
	"fmt"
	"strings"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// luaHeader opens lua/autodoc/contracts.lua; TestLuaContractsAreGenerated compares the whole file.
const luaHeader = `-- The daemon's verb contracts, for autodoc.nvim: generated from the contracts this build serves by
-- go generate ./rpc (internal/contractsgen). Do not edit; autodoc --contracts prints the same.
-- autodoc.contract binds them as functions; the plugin and the binary ship from one commit.
`

// Lua is c as a Lua module: the protocols, each verb's since, scope, audience, params, result and
// doc, by name, and the error codes with their names.
func Lua(c rpc.Catalog) string {
	var b strings.Builder
	b.WriteString(luaHeader)
	fmt.Fprintf(&b, "return {\n  protocol = %d,\n  min_protocol = %d,\n  verbs = {\n", c.Protocol, c.MinProtocol)
	for _, v := range c.Verbs {
		fmt.Fprintf(&b, "    [%s] = {\n      since = %d, scope = %s, audience = %s,\n      params = {", luaString(v.Verb), v.Since,
			luaString(v.Scope.String()), luaString(v.Audience.String()))
		for i, p := range v.Params {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, "\n        { name = %s, type = %s", luaString(p.Name), luaString(p.Type))
			if p.Optional {
				b.WriteString(", optional = true")
			}
			b.WriteString(" }")
		}
		if len(v.Params) > 0 {
			b.WriteString("\n      ")
		}
		b.WriteString("},\n")
		if v.Result != "" {
			fmt.Fprintf(&b, "      result = %s,\n", luaString(v.Result))
		}
		if v.Doc != "" {
			fmt.Fprintf(&b, "      doc = %s,\n", luaString(v.Doc))
		}
		b.WriteString("    },\n")
	}
	b.WriteString("  },\n  errors = {\n")
	for _, e := range c.Errors {
		fmt.Fprintf(&b, "    { code = %d, name = %s", e.Code, luaString(e.Name))
		if e.Next != "" {
			fmt.Fprintf(&b, ", next = %s", luaString(e.Next))
		}
		b.WriteString(" },\n")
	}
	b.WriteString("  },\n}\n")
	return b.String()
}

// luaString is s as a Lua string literal LuaJIT reads: the quote, the backslash and control bytes
// escaped (decimal escapes, which every Lua has), UTF-8 left as it is.
func luaString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\%03d`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
