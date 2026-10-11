-- The migration's offline copies held to the daemon they copy, on a real daemon built from this
-- checkout: autodoc.kb.migrate.schema_check against doc.validate, and autodoc.kb.migrate.refs
-- against graph.resolve. The copies exist because the migration works on a tree that exists only in
-- its plan (schema_check.lua, refs.lua); this spec is what keeps them the daemon's rules.
local t = require("helpers")

local bin = assert(os.getenv("AUTODOC_TEST_BIN"), "AUTODOC_TEST_BIN")
local root = t.tmp("kb")
local cfg = t.tmp("config.toml")
vim.fn.writefile({
  "[server]",
  string.format("socket = %q", t.tmp("run", "autodoc.sock")),
  string.format("state_dir = %q", t.tmp("state", "autodoc")),
  string.format("data_dir = %q", t.tmp("data", "autodoc")),
}, cfg)

local function write(rel, text)
  local p = root .. "/" .. rel
  vim.fn.mkdir(vim.fn.fnamemodify(p, ":h"), "p")
  vim.fn.writefile(vim.split(text, "\n", { plain = true }), p)
end

local schema = require("autodoc.kb.schema")
local schema_check = require("autodoc.kb.migrate.schema_check")
local refs = require("autodoc.kb.migrate.refs")
local session = require("autodoc.session")

-- the tree graph.resolve and refs.resolve both answer for
local files = { "notes/a.md", "notes/b.md", "other/b.md", "deep/x/y.md", "notes/sub/c.md" }
for _, f in ipairs(files) do write(f, "# " .. f .. "\n") end
-- the KB carries the schema AutoDoc ships, so doc.validate checks what schema_check checks
write("_schema/frontmatter.yaml", table.concat(vim.fn.readfile(schema.schema_path), "\n"))

require("autodoc").setup({ bin = bin, config = cfg })

local function call(verb, params)
  return t.await(20000, function(done) session.call(verb, params, done) end)
end

t.section("the workspace serves the shipped schema", function()
  local _, err = call("workspace.add", { "kb", root })
  t.ok(err == nil, "workspace.add", err and err.message)
  _, err = call("workspace.configure", { "kb", { schema = "_schema/frontmatter.yaml" } })
  t.ok(err == nil, "workspace.configure schema", err and err.message)
  local ready = vim.wait(20000, function()
    local res = call("doc.validate", { "kb", "probe.md", "---\ntype: note\n---\n" })
    return res and res.diagnostics and #res.diagnostics > 0
  end, 100)
  t.ok(ready, "doc.validate answers with the schema")
  ready = vim.wait(20000, function()
    local res = call("graph.resolve", { "kb", "notes/a.md", "[[y]]" })
    return res and res.path == "deep/x/y.md"
  end, 100)
  t.ok(ready, "the tree is indexed")
end)

-- The rules both sides have. schema_check also reports no_frontmatter, which only the migration
-- counts; the daemon also reports YAML and mapping errors, which the migration's planned texts
-- cannot have (it writes them).
local SHARED = { required = true, type = true, enum = true, unknown_type = true }

local function keys(diags)
  local out = {}
  for _, d in ipairs(diags or {}) do
    if SHARED[d.rule] then out[#out + 1] = (d.field or "") .. ":" .. d.rule end
  end
  table.sort(out)
  return table.concat(out, " ")
end

local fm = function(lines) return "---\n" .. table.concat(lines, "\n") .. "\n---\n\n# Doc\n" end
local base = { "status: draft", "created: 2026-10-12", "tags: [a]", 'abstract: "x"' }
local function with(typ, extra)
  local lines = { "type: " .. typ }
  local over = {}
  for _, l in ipairs(extra or {}) do over[l:match("^(%w+):")] = l end
  for _, l in ipairs(base) do lines[#lines + 1] = over[l:match("^(%w+):")] or l end
  for _, l in ipairs(extra or {}) do
    local k = l:match("^(%w+):")
    local listed = false
    for _, b in ipairs(base) do listed = listed or b:match("^(%w+):") == k end
    if not listed then lines[#lines + 1] = l end
  end
  return fm(lines)
end

-- Each rule rejects at least one document, so agreement is not agreement on an empty set.
local CORPUS = {
  { "a valid synthesis", with("synthesis"), "" },
  { "required fields missing", fm({ "type: note", "status: x" }), ":required" },
  { "an enum value outside it", with("synthesis", { "status: bogus" }), "status:enum" },
  { "an integer that is not", with("adr", { "status: accepted", 'number: "1"', "revision: soon" }), "revision:type" },
  { "a date that is not", with("note", { "created: yesterday" }), "created:type" },
  { "a list where a string goes", with("note", { "title: [a, b]" }), "title:type" },
  -- a type outside the discriminator's enum is the enum's to refuse: every type the enum names has
  -- a section in the shipped schema, so unknown_type needs a schema of its own (the next section)
  { "a type outside the enum", with("widget"), "type:enum" },
  { "a type's own required field", with("review", { "status: completed" }), "required" },
}

t.section("schema_check finds what doc.validate finds", function()
  local s = schema.shipped()
  for _, c in ipairs(CORPUS) do
    local name, text, rejects = c[1], c[2], c[3]
    local res, err = call("doc.validate", { "kb", "doc.md", text })
    local want, got = keys(res and res.diagnostics), keys(schema_check.validate(s, text))
    t.ok(err == nil and got == want, name .. ": the same diagnostics",
      string.format("daemon {%s}, schema_check {%s}%s", want, got, err and (" err " .. err.message) or ""))
    t.ok(rejects == "" and want == "" or rejects ~= "" and want:find(rejects, 1, true) ~= nil,
      name .. (rejects == "" and ": accepted" or ": rejected for " .. rejects), want)
  end
end)

t.section("unknown_type: a type the enum allows and the schema has no section for", function()
  local own = table.concat({
    "version: 2",
    "strict: false",
    "discriminator: type",
    "common:",
    "  type: {type: string, required: true, enum: [note, ghost]}",
    "types:",
    "  note:",
    "    status: {type: string}",
  }, "\n") .. "\n"
  local root2 = t.tmp("kb2")
  vim.fn.mkdir(root2, "p")
  vim.fn.writefile(vim.split(own, "\n", { plain = true }), root2 .. "/schema.yaml")
  local _, err = call("workspace.add", { "kb2", root2 })
  t.ok(err == nil, "workspace.add kb2", err and err.message)
  _, err = call("workspace.configure", { "kb2", { schema = "schema.yaml" } })
  t.ok(err == nil, "workspace.configure kb2's schema", err and err.message)
  local text = "---\ntype: ghost\n---\n"
  local want = ""
  t.wait(20000, function()
    local res = call("doc.validate", { "kb2", "doc.md", text })
    want = keys(res and res.diagnostics)
    return want ~= ""
  end)
  local got = keys(schema_check.validate(schema.parse(own), text))
  t.ok(want == "type:unknown_type" and got == want, "the same unknown_type", string.format("daemon {%s}, schema_check {%s}", want, got))
end)

t.section("refs.resolve reaches where graph.resolve does", function()
  local view = refs.view(files)
  local src = "notes/a.md"
  -- { the link as written, refs' kind, its target as refs scans it, where both reach (nil: none) }
  local SAME = {
    { "[[notes/b]]", "wiki", "notes/b", "notes/b.md" },
    { "[[y]]", "wiki", "y", "deep/x/y.md" },
    { "[[x/y]]", "wiki", "x/y", "deep/x/y.md" },
    { "[[sub/c]]", "wiki", "sub/c", "notes/sub/c.md" },
    { "[[missing]]", "wiki", "missing", nil },
    { "[t](b.md)", "rel", "b.md", "notes/b.md" },
    { "[t](../other/b.md)", "rel", "../other/b.md", "other/b.md" },
    { "[t](sub/c)", "rel", "sub/c", "notes/sub/c.md" },
    { "[t](../gone.md)", "rel", "../gone.md", nil },
  }
  for _, c in ipairs(SAME) do
    local raw, kind, target, want = c[1], c[2], c[3], c[4]
    local res, err = call("graph.resolve", { "kb", src, raw })
    local daemon = res and res.path ~= "" and res.path or nil
    local lua = refs.resolve({ kind = kind, old = target }, src, view)
    t.ok(err == nil and daemon == want and lua == want, raw .. " reaches " .. tostring(want),
      string.format("daemon %s (%s), refs %s", tostring(daemon), res and res.reason or "?", tostring(lua)))
  end
  -- Where they differ by design, pinned so a change to either side is seen: of two files named b
  -- at one depth, the daemon reaches neither (ambiguous); the migration takes the shorter path and
  -- reports the link as suffix-ambiguous for a person to check.
  -- and the migration reads a wikilink relative to its source first (as Obsidian does), the daemon
  -- never.
  for _, c in ipairs({ { "deep/x/y.md", "notes/b.md", "suffix-ambiguous" }, { "notes/a.md", "notes/b.md", "relative" } }) do
    local from, reach, why = c[1], c[2], c[3]
    local res = call("graph.resolve", { "kb", from, "[[b]]" })
    local lua, how = refs.resolve({ kind = "wiki", old = "b" }, from, view)
    t.ok(res and res.path == "" and res.reason == "ambiguous" and lua == reach and how == why,
      "[[b]] from " .. from .. ": ambiguous to the daemon, " .. why .. " to the migration", vim.inspect({ res, lua, how }))
  end
end)

t.finish()
