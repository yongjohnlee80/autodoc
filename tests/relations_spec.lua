-- What a KB document is connected to (autodoc.relations, ADR 1791430651 §4.4 — the TUI's Relations
-- drawer in Neovim): the sections by kind and direction against a real daemon built from this
-- checkout, depth 2, the unresolved and the loops; the kb drawer's Relations section following
-- the editor (never another file's rows); <CR>, d and back (<leader>mo), back kept when :edit is
-- refused; the picker (<leader>ml); the right-click rows enabled on a KB file.
local t = require("helpers")

local bin = assert(os.getenv("AUTODOC_TEST_BIN"), "AUTODOC_TEST_BIN")
local cfg = t.tmp("config.toml")
vim.fn.writefile({
  "[server]",
  string.format("socket = %q", t.tmp("run", "autodoc.sock")),
  string.format("state_dir = %q", t.tmp("state", "autodoc")),
  string.format("data_dir = %q", t.tmp("data", "autodoc")),
  "[follow]",
  'poll_interval = "50ms"',
}, cfg)

local function write(path, lines)
  vim.fn.mkdir(vim.fn.fnamemodify(path, ":h"), "p")
  vim.fn.writefile(lines, path)
end

-- doc.md and what it is connected to: every relation kind out of it, two into it, a body link,
-- one that names nothing, a supersession loop with loop.md, and far.md two links away (the TUI's
-- relationsKB, tui/relations_test.go)
local kb = vim.fs.normalize(t.tmp("kb"))
local files = {
  ["doc.md"] = { "---", "superseded_by: [new.md]", "supersedes: [loop.md]", "sources: [src.md]", "amends: [base.md]",
    "related: [rel.md]", "---", "# Doc", "", "see [[body]] and [[missing-one]]" },
  ["new.md"] = { "# New", "", "[[far]]" },
  ["far.md"] = { "# Far" },
  ["loop.md"] = { "---", "supersedes: [doc.md]", "---", "# Loop" },
  ["old.md"] = { "---", "superseded_by: [doc.md]", "---", "# Old" },
  ["src.md"] = { "# Src" },
  ["citer.md"] = { "---", "sources: [doc.md]", "---", "# Citer" },
  ["base.md"] = { "# Base" },
  ["rel.md"] = { "# Rel" },
  ["body.md"] = { "# Body" },
}
for name, lines in pairs(files) do write(kb .. "/" .. name, lines) end
local outside = vim.fs.normalize(t.tmp("elsewhere", "notes.md"))
write(outside, { "# not in a KB" })

vim.fn.mkdir(t.tmp("project"), "p")
vim.cmd("cd " .. vim.fn.fnameescape(t.tmp("project")))

local autodoc = require("autodoc")
local session = require("autodoc.session")
local relations = require("autodoc.relations")
local drawer = require("autodoc.views.drawer")
local host = require("autodoc.views.host")
local panel = require("autodoc.views.panel")
drawer.POLL_MS = 150
autodoc.setup({ bin = bin, config = cfg, keys = true })
panel.setup()

local notes = {}
vim.notify = function(msg) notes[#notes + 1] = tostring(msg) end
local function noted(pat)
  for _, n in ipairs(notes) do if n:find(pat, 1, true) then return true end end
  return false
end

local function call(method, params)
  return t.await(10000, function(done) session.call(method, params, done) end)
end

local DOC = {
  "superseded by (2)", "new.md", "loop.md",
  "supersedes (2)", "loop.md", "old.md",
  "sources (1)", "src.md",
  "cited by (1)", "citer.md",
  "amends (1)", "base.md",
  "related (1)", "rel.md",
  "links (1)", "body.md",
  "unresolved (2)", "[[missing-one]]  (wikilink, names no document)", "loop.md  (supersession goes round a loop)",
}
local function texts(rows)
  return vim.tbl_map(function(r) return r.text end, rows or {})
end

t.section("rows: the layout, without a daemon", function()
  local rows, n = relations.rows("kb", "a.md", {
    out = { { path = "b.md", raw = "b.md", kind = "superseded_by", resolved = true },
      { path = "b.md", raw = "[[b]]", kind = "wikilink", resolved = true },
      { path = "", raw = "[[gone]]", kind = "wikilink", resolved = false } },
    ["in"] = { { path = "c.md", raw = "a.md", kind = "superseded_by", resolved = true },
      { path = "d.md", raw = "a.md", kind = "adr", resolved = true } },
    cycles = {},
    edges = { { "b.md", "e.md", "wikilink" }, { "f.md", "b.md", "related" }, { "a.md", "b.md", "wikilink" } },
  })
  t.eq(texts(rows), {
    "superseded by (1)", "b.md", "› e.md  (wikilink →)", "› f.md  (← related)",
    "supersedes (1)", "c.md",
    "links (1)", "b.md",
    "backlinks (1)", "d.md",
    "unresolved (1)", "[[gone]]  (wikilink, names no document)",
  }, "a relation in goes under the other side's name, an incoming adr under backlinks, the second ring once under its first row")
  t.eq(n, 3, "three first-ring documents")
  t.eq(rows[2].path, "b.md", "a document row opens its path")
  t.eq(rows[3].path, "e.md", "a second-ring row opens its own")
  t.eq(rows[1].path, nil, "a heading opens nothing")
end)

t.section("setup", function()
  local _, err = call("workspace.add", { "kb", kb })
  t.ok(err == nil, "workspace.add", err and err.message)
  t.ok(t.wait(20000, function()
    local nb = call("graph.neighborhood", { "kb", "doc.md", 2 })
    return type(nb) == "table" and vim.tbl_contains(nb.nodes or {}, "far.md")
  end), "indexed, its relations and links too")
end)

t.section("load: the sections, against the daemon", function()
  local rows, n, err = t.await(10000, function(done) relations.load("kb", "doc.md", false, done) end)
  t.ok(err == nil, "no error", err and err.message)
  t.eq(texts(rows), DOC, "each relation in its section, the body's links, then what names nothing and the loop")
  t.eq(n, 8, "eight neighbours")
  local deep = t.await(10000, function(done) relations.load("kb", "doc.md", true, done) end)
  local want = vim.list_extend({ DOC[1], DOC[2], "› far.md  (wikilink →)" }, vim.list_slice(DOC, 3))
  t.eq(texts(deep), want, "depth 2 adds far.md under new.md, which links to it")
  local none = t.await(10000, function(done) relations.load("kb", "nope.md", false, done) end)
  t.eq(none, {}, "a document not indexed has none")
end)

local view
local function rel_rows() return vim.tbl_filter(function(r) return r.section == "relations" end, view._rows()) end
local function rel_texts()
  local out = {}
  for _, r in ipairs(rel_rows()) do
    if r.kind == "rel_heading" or r.kind == "rel_doc" or r.kind == "rel_unresolved" then
      local i
      for j, x in ipairs(view._rows()) do if x == r then i = j end end
      out[#out + 1] = vim.trim(vim.api.nvim_buf_get_lines(view:bufnr(), i - 1, i, false)[1])
    end
  end
  return out
end
local function editor_win()
  return panel.editor_target_winid()
end
local function edit(path)
  vim.api.nvim_set_current_win(editor_win())
  vim.cmd("edit " .. vim.fn.fnameescape(path))
end

t.section("the drawer's Relations section follows the editor", function()
  local ok, v = t.await(5000, function(done) host.open(done) end)
  t.ok(ok == true and v, "the drawer is open", vim.inspect(v))
  view = host.view()
  t.ok(t.wait(10000, function()
    return vim.tbl_contains(vim.tbl_map(function(r) return r.section end, view._rows()), "relations")
  end), "it has a Relations section")
  edit(kb .. "/doc.md")
  t.ok(t.wait(10000, function() return vim.deep_equal(rel_texts(), DOC) end), "entering doc.md lists its relations",
    vim.inspect(rel_texts()))
  local head = vim.tbl_filter(function(r) return r.kind == "rel_file" end, rel_rows())[1]
  t.ok(head and head.ws == "kb" and head.path == "doc.md", "headed by the file it is of", vim.inspect(head))
  local sec
  for i, r in ipairs(view._rows()) do
    if r.kind == "section" and r.section == "relations" then sec = vim.api.nvim_buf_get_lines(view:bufnr(), i - 1, i, false)[1] end
  end
  t.ok(sec and sec:find("Relations (8)", 1, true), "its count is the neighbours", sec)

  -- another file's rows go at once: none until new.md's are read, never doc.md's
  edit(kb .. "/new.md")
  view._follow(vim.api.nvim_get_current_buf())
  local st = view._rel()
  t.ok(st.ref and st.ref.rel == "new.md" and st.rows == nil, "on entering new.md, doc.md's rows are gone before new.md's arrive",
    vim.inspect({ ref = st.ref, rows = st.rows and #st.rows }))
  t.ok(t.wait(10000, function() return vim.deep_equal(rel_texts(), { "supersedes (1)", "doc.md", "links (1)", "far.md" }) end),
    "then new.md's own", vim.inspect(rel_texts()))

  edit(outside)
  t.ok(t.wait(5000, function() return view._rel().why ~= nil end), "a file in no KB says so")
  t.ok(#vim.tbl_filter(function(r) return r.kind == "rel_doc" end, rel_rows()) == 0, "and lists nothing")
end)

t.section("the section follows the KB's change feed, not a timer", function()
  edit(kb .. "/doc.md")
  t.ok(t.wait(10000, function() return vim.deep_equal(rel_texts(), DOC) end), "doc.md's relations")
  -- a link into doc.md added outside Neovim: no write here, it is indexed on the daemon's own time
  write(kb .. "/body.md", { "# Body", "", "back to [[doc]]" })
  t.ok(t.wait(10000, function()
    local got = rel_texts()
    return vim.tbl_contains(got, "backlinks (1)") and got[#got - 3] == "body.md"
  end), "another file's new link shows as a backlink once indexed", vim.inspect(rel_texts()))
  -- this file saved: its new link shows once the write is indexed
  vim.api.nvim_buf_set_lines(0, -1, -1, false, { "", "and [[far]]" })
  vim.cmd("write")
  t.ok(t.wait(10000, function() return vim.tbl_contains(rel_texts(), "links (2)") and vim.tbl_contains(rel_texts(), "far.md") end),
    "a saved link shows once indexed", vim.inspect(rel_texts()))
  -- put the fixture back for the sections after this one
  write(kb .. "/body.md", files["body.md"])
  vim.api.nvim_buf_set_lines(0, 0, -1, false, files["doc.md"])
  vim.cmd("write")
  t.ok(t.wait(10000, function() return vim.deep_equal(rel_texts(), DOC) end), "and back to doc.md's own", vim.inspect(rel_texts()))
end)

t.section("a failed cursor read is retried, so the feed is followed after all", function()
  edit(kb .. "/doc.md")
  t.ok(t.wait(10000, function() return vim.deep_equal(rel_texts(), DOC) end), "doc.md's relations")
  -- index.status fails once: the rows still load, the cursor is missing
  local real, failed = session.request, false
  session.request = function(method, params, cb)
    if method == "index.status" and params[1] == "kb" and not failed then
      failed = true
      return vim.schedule(function() cb(nil, { message = "a status failure" }) end)
    end
    return real(method, params, cb)
  end
  view._dispatch("d", rel_rows()[1]) -- depth 2: a reload, whose status read fails
  t.ok(t.wait(10000, function() return failed and vim.tbl_contains(rel_texts(), "› far.md  (wikilink →)") end),
    "the relations load without the cursor")
  t.ok(t.wait(5000, function() return view._rel().cursor ~= nil end), "a later poll reads the cursor again")
  session.request = real
  write(kb .. "/body.md", { "# Body", "", "back to [[doc]]" })
  t.ok(t.wait(10000, function() return vim.tbl_contains(rel_texts(), "backlinks (1)") end),
    "and the feed is followed: another file's new link shows", vim.inspect(rel_texts()))
  write(kb .. "/body.md", files["body.md"])
  t.ok(t.wait(10000, function() return not vim.tbl_contains(rel_texts(), "backlinks (1)") end), "the fixture back")
  view._dispatch("d", rel_rows()[1])
  t.ok(t.wait(10000, function() return vim.deep_equal(rel_texts(), DOC) end), "depth 1 again")
end)

t.section("<CR> opens a relation, d is depth 2, and back returns", function()
  relations._reset_for_tests()
  edit(kb .. "/doc.md")
  t.ok(t.wait(10000, function() return vim.deep_equal(rel_texts(), DOC) end), "doc.md's relations")
  view._dispatch("d", rel_rows()[1])
  t.ok(t.wait(10000, function() return vim.tbl_contains(rel_texts(), "› far.md  (wikilink →)") end), "d shows the second ring")
  view._dispatch("d", rel_rows()[1])
  t.ok(t.wait(10000, function() return vim.deep_equal(rel_texts(), DOC) end), "d again hides it")

  vim.api.nvim_set_current_win(panel.window())
  local old = vim.tbl_filter(function(r) return r.kind == "rel_doc" and r.path == "old.md" end, rel_rows())[1]
  view._dispatch("<CR>", old)
  t.eq(vim.api.nvim_buf_get_name(0), kb .. "/old.md", "<CR> opens old.md in the editor")
  t.eq(vim.api.nvim_win_get_buf(panel.window()), view:bufnr(), "the panel still shows the drawer")
  relations.back()
  t.eq(vim.api.nvim_buf_get_name(0), kb .. "/doc.md", "back returns to doc.md")
  t.eq(relations._history, {}, "and the history is spent")
  relations.back()
  t.ok(noted("nothing to go back to"), "back with nowhere to go says so")
end)

t.section("back keeps its document when :edit is refused", function()
  relations._reset_for_tests()
  vim.o.hidden = false
  edit(kb .. "/doc.md")
  relations.open("kb", "src.md")
  t.eq(vim.api.nvim_buf_get_name(0), kb .. "/src.md", "open goes to src.md")
  vim.api.nvim_buf_set_lines(0, 0, 0, false, { "an unsaved edit" })
  relations.back()
  t.eq(vim.api.nvim_buf_get_name(0), kb .. "/src.md", "an unsaved buffer keeps the page (E37)")
  t.eq(relations._history, { { ws = "kb", rel = "doc.md" } }, "and back keeps doc.md")
  vim.cmd("edit!") -- the edit discarded
  relations.back()
  t.eq(vim.api.nvim_buf_get_name(0), kb .. "/doc.md", "back again goes there")
  t.eq(relations._history, {}, "its entry spent once it went")
  vim.o.hidden = true
end)

t.section("the picker: <leader>ml", function()
  relations._reset_for_tests()
  edit(kb .. "/doc.md")
  local shown
  vim.ui.select = function(items, opts, cb)
    shown = { prompt = opts.prompt, items = vim.tbl_map(opts.format_item, items) }
    cb(items[3])
  end
  relations.pick()
  t.ok(t.wait(10000, function() return shown ~= nil end), "it lists")
  t.eq(shown and shown.items[1], "superseded by  new.md", "a row is its section, then the document")
  t.eq(shown and #shown.items, 8, "each neighbour once")
  t.ok(t.wait(5000, function() return vim.api.nvim_buf_get_name(0) == kb .. "/old.md" end), "choosing one opens it")
  t.eq(relations._history, { { ws = "kb", rel = "doc.md" } }, "the document left behind is remembered")
end)

t.section("the right-click rows are enabled on a KB file", function()
  local function enabled(name)
    for _, m in ipairs(vim.fn.menu_get("PopUp", "n")) do
      for _, sub in ipairs(m.submenus or {}) do
        if sub.name == name then return sub.mappings and sub.mappings.n and sub.mappings.n.enabled == 1 end
      end
    end
  end
  edit(kb .. "/doc.md")
  vim.cmd("doautocmd MenuPopup")
  t.ok(enabled("AutoDoc: Go to related…"), "Go to related… on a KB file")
  t.ok(enabled("AutoDoc: Back"), "Back, with a history")
  edit(outside)
  vim.cmd("doautocmd MenuPopup")
  t.ok(not enabled("AutoDoc: Go to related…") and not enabled("AutoDoc: Back"), "neither on a file in no KB")
end)

t.finish()
