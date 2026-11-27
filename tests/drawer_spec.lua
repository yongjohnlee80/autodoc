-- The kb drawer against a real daemon built from this checkout, in autodoc's own panel (no
-- auto-finder): the sections and their counts, the primary and selected marks, a KB's documents as
-- a folder tree, opening one, selecting, the primary with and without confirmation, adding,
-- renaming (an open KB staying open) and removing a location, the model sections' dialogs, the
-- live tree through index.changes, nothing pulled while hidden, and a stale reply dropped.
--
-- Runs against either auto-core: with auto-core.kb (feat/kb-primary) P sets the primary; without
-- it (main) the drawer shows no ★ and P says auto-core.kb is unavailable.
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
local kb1, kb2 = t.tmp("kb1"), t.tmp("kb2")
write(kb1 .. "/README.md", { "# One", "", "the first knowledge base" })
write(kb1 .. "/adrs/0001-first.md", { "# First", "", "a decision about kestrels" })
write(kb1 .. "/adrs/0002-second.md", { "# Second", "", "a decision about plovers" })
write(kb2 .. "/notes.md", { "# Notes", "", "the second knowledge base" })

-- the project is the sandbox, so auto-core.kb keys its primary there
vim.fn.mkdir(t.tmp("project"), "p")
vim.cmd("cd " .. vim.fn.fnameescape(t.tmp("project")))

local autodoc = require("autodoc")
local session = require("autodoc.session")
local drawer = require("autodoc.views.drawer")
local host = require("autodoc.views.host")
local panel = require("autodoc.views.panel")
drawer.POLL_MS = 150
autodoc.setup({ bin = bin, config = cfg })
panel.setup() -- idempotent: the fallback host, registered as setup registers it

local has_kb = pcall(require, "auto-core.kb")
print("auto-core.kb: " .. (has_kb and "available" or "absent"))

-- ─── stubs: the dialogs answer from queues, notifications are kept ───

local notes = {}
vim.notify = function(msg) notes[#notes + 1] = tostring(msg) end
local function noted(pat)
  for _, n in ipairs(notes) do if n:find(pat, 1, true) then return true end end
  return false
end

local inputs, input_seen = {}, {}
vim.ui.input = function(opts, cb)
  input_seen[#input_seen + 1] = opts
  local a = table.remove(inputs, 1)
  if type(a) == "function" then a = a(opts) end
  cb(a)
end
local selects = {}
vim.ui.select = function(items, opts, cb)
  local f = table.remove(selects, 1)
  cb(f and f(items, opts) or nil)
end
local secrets = {}
drawer.prompt_secret = function(_, cb) cb(table.remove(secrets, 1)) end

local modal = require("auto-core.ui.modal")
local answers, modal_seen = {}, {}
modal.open = function(opts)
  modal_seen[#modal_seen + 1] = opts
  local f = table.remove(answers, 1)
  local v = nil
  if f then v = f(opts) end
  if v == nil then
    if opts.on_cancel then opts.on_cancel() end
  else
    opts.on_choice(v)
  end
end
local YES = function() return true end
local NO = function() return false end

-- ─── helpers over the view ───

local view
local function rows() return view and view._rows() or {} end
local function find(pred)
  for i, r in ipairs(rows()) do if pred(r) then return r, i end end
end
local function line(i) return vim.api.nvim_buf_get_lines(view:bufnr(), i - 1, i, false)[1] end
local function ws_row(name) return find(function(r) return r.kind == "workspace" and r.ws == name end) end
local function doc_row(ws, path) return find(function(r) return r.kind == "doc" and r.ws == ws and r.path == path end) end
local function section_row(key) return find(function(r) return r.kind == "section" and r.section == key end) end
local function call(method, params)
  return t.await(10000, function(done) session.call(method, params, done) end)
end

t.section("setup", function()
  local _, e1 = call("workspace.add", { "kb1", kb1 })
  local _, e2 = call("workspace.add", { "kb2", kb2 })
  t.ok(e1 == nil and e2 == nil, "two workspaces added", vim.inspect({ e1, e2 }))
  -- indexed before the drawer opens, so the tree cells exercise the listing, not the change feed
  t.ok(t.wait(15000, function()
    local a, b = call("index.status", { "kb1" }), call("index.status", { "kb2" })
    return a and a.docs == 3 and b and b.docs == 1
  end), "both are indexed")
end)

t.section("the drawer opens in autodoc's own panel", function()
  local ok, v = t.await(5000, function(done) host.open(done) end)
  t.ok(ok == true and v and v.host == "autodoc", "host.open mounts the fallback panel without auto-finder", vim.inspect(v))
  t.eq(host.owner(), "autodoc", "the owner is autodoc's self-host")
  view = host.view()
  t.ok(view ~= nil and panel.window() ~= nil, "a view, in the panel's window")
  t.ok(t.wait(10000, function() return ws_row("kb1") and ws_row("kb2") end), "both KBs are listed")
  t.ok(line(1):find("^AutoDoc .* · connected") ~= nil, "the header says the daemon's version and connection", line(1))
  local b = view:bufnr()
  for _, k in ipairs({ "<CR>", "s", "P", "A", "r", "e", "d", "a", "w", "R", "?" }) do
    t.ok(vim.fn.maparg(k, "n", false, true).buffer == 1, "the buffer maps " .. k)
  end
  local q = vim.fn.maparg("q", "n", false, true)
  t.ok(not tostring(q.desc or ""):find("autodoc drawer", 1, true), "q stays the host's", q.desc)
  t.ok(b and vim.bo[b].filetype == "autodoc", "the profile's filetype")
end)

t.section("sections render with counts", function()
  local provs = call("embedding.providers", {})
  local rks = call("ranker.list", {})
  t.ok(t.wait(5000, function()
    local _, i = section_row("ranker")
    return i and not line(i):find("…", 1, true)
  end), "every section has loaded")
  local _, ik = section_row("kb")
  local _, ie = section_row("embedding")
  local _, ir = section_row("ranker")
  t.eq(line(ik), "▼ Knowledge Base (2)", "the Knowledge Base header and count")
  t.eq(line(ie), string.format("▼ Embedding Models (%d)", #(provs.providers or {})), "the Embedding Models header and count")
  t.eq(line(ir), string.format("▼ Reranker (%d)", #(rks.rankers or {})), "the Reranker header and count")
  t.ok(ik < ie and ie < ir, "the fixed section order")
end)

t.section("a section folds, and the fold is kept", function()
  local r = section_row("ranker")
  view._dispatch("<CR>", r)
  local _, i = section_row("ranker")
  t.ok(line(i):find("^▶ Reranker") ~= nil, "<CR> on a header folds it", line(i))
  t.ok(drawer.collapsed("ranker"), "the fold is in auto-core.state autodoc.ui")
  local stored = require("auto-core.state").namespace("autodoc.ui", { persist = "json" }):get("collapsed")
  t.ok(type(stored) == "table" and stored.ranker == true, "stored under collapsed.ranker", vim.inspect(stored))
  view._dispatch("<CR>", (section_row("ranker")))
  t.ok(not drawer.collapsed("ranker"), "and unfolds")
end)

t.section("s selects the KB to search", function()
  t.ok(line(select(2, ws_row("kb1"))):find("^  ○ kb1") ~= nil, "nothing marked before a selection", line(select(2, ws_row("kb1"))))
  view._dispatch("s", (ws_row("kb2")))
  t.eq(session.selected(), "kb2", "s selected kb2")
  local _, i = ws_row("kb2")
  t.ok(line(i):find("^  ● kb2") ~= nil and line(i):find("selected", 1, true) ~= nil, "● marks the selected KB", line(i))
end)

t.section("P: the primary KB", function()
  local kb1row = ws_row("kb1")
  if not has_kb then
    local before = #modal_seen
    view._dispatch("P", kb1row)
    t.ok(#modal_seen == before, "without auto-core.kb, P asks nothing")
    t.ok(noted("auto-core.kb is unavailable"), "and says auto-core.kb is unavailable")
    t.ok(not line(select(2, ws_row("kb1"))):find("★", 1, true), "no ★ without auto-core.kb")
    return
  end
  local kb = require("auto-core.kb")
  t.eq(kb.primary(), nil, "no primary yet")
  answers = { NO }
  view._dispatch("P", kb1row)
  local seen = modal_seen[#modal_seen]
  t.ok(seen and seen.reversibility == "reversible", "P asks in a reversible modal")
  local body = table.concat(type(seen.body) == "table" and seen.body or { seen.body or "" }, "\n")
  t.ok(body:find(kb1, 1, true) and body:find("kb1", 1, true), "the modal names the KB and its root", body)
  t.ok(body:find("Agents spawned from now on", 1, true) ~= nil, "it says newly spawned agents use it")
  t.ok(body:find("todo store is untouched", 1, true) ~= nil, "it says the todo store is untouched")
  t.eq(kb.primary(), nil, "declined: no primary")
  local ok = require("autodoc.api").set_primary({ name = "kb1", root = kb1 }, false)
  t.ok(ok == false, "set_primary without confirmation is refused")
  t.eq(kb.primary(), nil, "still no primary")
  answers = { YES }
  view._dispatch("P", kb1row)
  local p = kb.primary()
  t.ok(p and p.workspace == "kb1" and p.root == vim.fs.normalize(kb1), "confirmed: kb1 is the primary", vim.inspect(p))
  t.ok(t.wait(2000, function() return line(select(2, ws_row("kb1"))):find("^  ★ kb1") ~= nil end),
    "★ marks the primary", line(select(2, ws_row("kb1"))))
end)

t.section("a KB's documents, as a folder tree", function()
  view._dispatch("<CR>", (ws_row("kb1")))
  t.ok(t.wait(10000, function() return doc_row("kb1", "README.md") ~= nil end), "<CR> on a KB lists its documents")
  local dir, di = find(function(r) return r.kind == "dir" and r.ws == "kb1" and r.rel == "adrs" end)
  t.ok(dir ~= nil and line(di):find("▶ adrs/", 1, true) ~= nil, "a folder row, folded", dir and line(di))
  t.ok(doc_row("kb1", "adrs/0001-first.md") == nil, "a folded folder hides its documents")
  t.ok(di < select(2, doc_row("kb1", "README.md")), "folders come before files")
  view._dispatch("<CR>", dir)
  t.ok(doc_row("kb1", "adrs/0001-first.md") and doc_row("kb1", "adrs/0002-second.md"), "<CR> on the folder shows its documents")
  local _, fi = doc_row("kb1", "adrs/0001-first.md")
  t.ok(line(fi):find("0001-first.md", 1, true) and not line(fi):find("adrs/0001", 1, true), "a document shows its own name", line(fi))
end)

t.section("<CR> opens a document as an ordinary buffer", function()
  local pwin = panel.window()
  vim.api.nvim_set_current_win(pwin)
  view._dispatch("<CR>", (doc_row("kb1", "README.md")))
  local win = vim.api.nvim_get_current_win()
  t.eq(vim.api.nvim_buf_get_name(0), vim.fs.normalize(kb1) .. "/README.md", "the document is the current buffer")
  t.ok(win ~= pwin, "in an editor window, not the panel's")
  t.ok(vim.bo.buftype == "" and not vim.wo[win].winfixbuf, "an ordinary file buffer in an unpinned window")
  t.eq(vim.api.nvim_win_get_buf(pwin), view:bufnr(), "the panel still shows the drawer")
end)

t.section("the visible tree follows index.changes", function()
  write(kb1 .. "/adrs/0003-third.md", { "# Third", "", "a new decision" })
  t.ok(t.wait(5000, function() return doc_row("kb1", "adrs/0003-third.md") ~= nil end), "a file added on disk appears within 5 s")
  vim.fn.rename(kb1 .. "/adrs/0002-second.md", kb1 .. "/adrs/0002-renamed.md")
  t.ok(t.wait(5000, function()
    return doc_row("kb1", "adrs/0002-renamed.md") ~= nil and doc_row("kb1", "adrs/0002-second.md") == nil
  end), "a rename moves the node")
  vim.fn.delete(kb1 .. "/adrs/0003-third.md")
  t.ok(t.wait(5000, function() return doc_row("kb1", "adrs/0003-third.md") == nil end), "a removed file goes")
end)

t.section("an expired change cursor re-lists", function()
  local tree = view._st.trees["kb1"]
  local listed = 0
  local live = session.client()
  local real = live.call
  live.call = function(self, method, params, cb, opts)
    if method == "index.changes" and params[1] == "kb1" then
      return cb(nil, { code = -32063, message = "the change cursor is older than the retained log" })
    end
    if method == "index.documents" and params[1] == "kb1" then listed = listed + 1 end
    return real(self, method, params, cb, opts)
  end
  t.ok(t.wait(3000, function() return listed > 0 end), "CursorExpired re-lists through index.documents")
  live.call = real
  t.ok(t.wait(5000, function() return view._st.trees["kb1"] ~= tree and doc_row("kb1", "README.md") ~= nil end),
    "and the tree is rebuilt")
end)

t.section("nothing is pulled while the drawer is hidden", function()
  local pulls = 0
  local live = session.client()
  local real = live.call
  live.call = function(self, method, params, cb, opts)
    if method == "sys.events" or method == "index.changes" or method == "index.status" then pulls = pulls + 1 end
    return real(self, method, params, cb, opts)
  end
  t.ok(t.wait(2000, function() return pulls > 0 end), "a visible drawer pulls")
  local pwin = panel.window()
  local other = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_set_option_value("winfixbuf", false, { scope = "local", win = pwin })
  vim.api.nvim_win_set_buf(pwin, other)
  vim.wait(drawer.POLL_MS * 2)
  local at = pulls
  vim.wait(drawer.POLL_MS * 6)
  t.eq(pulls, at, "a hidden drawer pulls nothing over six intervals")
  t.ok(not view._polling(), "and its timer is stopped")
  vim.api.nvim_win_set_buf(pwin, view:bufnr())
  host.focus()
  t.ok(t.wait(3000, function() return pulls > at end), "shown again, it pulls again")
  live.call = real
end)

t.section("a stale reply is dropped", function()
  local live = session.client()
  local real = live.call
  local n = 0
  live.call = function(self, method, params, cb, opts)
    if method ~= "embedding.providers" then return real(self, method, params, cb, opts) end
    n = n + 1
    local name = n == 1 and "STALE" or "FRESH"
    local reply = { providers = { { name = name, kind = "openai", base_url = "x", model = "m", context = 0, has_key = false } }, active = "", error = "" }
    vim.defer_fn(function() cb(reply, nil) end, n == 1 and 400 or 10)
    return n
  end
  view._load_providers()
  view._load_providers()
  vim.wait(800)
  live.call = real
  t.ok(find(function(r) return r.kind == "provider" and r.provider.name == "FRESH" end) ~= nil, "the newer reply paints")
  t.ok(find(function(r) return r.kind == "provider" and r.provider.name == "STALE" end) == nil, "the older, later reply is dropped")
  view._load_providers()
  t.ok(t.wait(3000, function() return find(function(r) return r.kind == "provider" and r.provider.name == "FRESH" end) == nil end),
    "a real listing replaces the fake")
end)

-- Opening a KB reads its index.status for the tree, and the poll reads it for the count: a tree's
-- read answered after a newer poll's must not put its older count back. The tree's read is held
-- until a poll's has painted; the counts are marked so each answer is told apart.
t.section("an opening tree's older count never paints over a newer poll's", function()
  view._dispatch("<CR>", (ws_row("kb1"))) -- fold
  t.ok(view._st.expanded["kb1"] == nil, "kb1 is folded")
  local live = session.client()
  local real = live.call
  local taken, held, at_release = false, nil, nil
  live.call = function(self, method, params, cb, opts)
    if method ~= "index.status" or params[1] ~= "kb1" then return real(self, method, params, cb, opts) end
    local first = not taken -- the first kb1 read after this is installed: the tree's, sent below
    taken = true
    return real(self, method, params, function(res, err)
      if type(res) == "table" then res = vim.tbl_extend("force", res, { docs = first and 111 or 222 }) end
      if not first then return cb(res, err) end
      held = function()
        cb(res, err)
        vim.schedule(function() at_release = view._st.status["kb1"] and view._st.status["kb1"].docs end) -- after cb's apply
      end
    end, opts)
  end
  view._dispatch("<CR>", (ws_row("kb1"))) -- open, in the same tick: load_tree reads index.status first
  t.ok(t.wait(5000, function() return held ~= nil end), "the tree's read is answered, and held")
  t.ok(t.wait(5000, function() return view._st.status["kb1"] and view._st.status["kb1"].docs == 222 end),
    "a poll's newer read paints its count")
  held()
  t.ok(t.wait(5000, function() return at_release ~= nil end), "the tree's read lands after it")
  t.eq(at_release, 222, "the newer count is kept")
  live.call = real
  t.ok(t.wait(5000, function() local tr = view._st.trees["kb1"]; return tr and not tr.loading and doc_row("kb1", "README.md") ~= nil end),
    "and the tree is listed all the same")
end)

t.section("A adds a location, creating its folder", function()
  local function titled(word) return function(o) return o.title:find(word, 1, true) and true or nil end end
  local function offered(word, from)
    for i = from + 1, #modal_seen do if modal_seen[i].title:find(word, 1, true) then return true end end
    return false
  end
  local dir = t.tmp("kb3")
  inputs = { dir, "kb3" }
  local before = #modal_seen
  answers = { titled("Create"), NO, NO }
  view._dispatch("A", (ws_row("kb1")))
  t.ok(vim.fn.isdirectory(dir) == 1, "the new folder was created")
  t.ok(input_seen[#input_seen - 1].completion == "dir", "the folder prompt completes directories")
  t.ok(t.wait(5000, function() return ws_row("kb3") ~= nil end), "the workspace is listed")
  t.ok(t.wait(5000, function() return offered("Scaffold", before) end), "a folder without AGENTS.md is offered the scaffold")
  t.eq(vim.fn.filereadable(dir .. "/AGENTS.md"), 0, "declined: nothing is written")
  if has_kb then
    t.ok(t.wait(5000, function() return modal_seen[#modal_seen].title:find("primary", 1, true) ~= nil end),
      "then it asks whether to make it primary")
    t.eq(require("auto-core.kb").primary().workspace, "kb1", "declined: the primary is unchanged")
    -- dismissed (not declined), the scaffold prompt ends what was asked
    local dir7 = t.tmp("kb7")
    vim.fn.mkdir(dir7, "p")
    inputs = { dir7, "kb7" }
    local at = #modal_seen
    answers = { function() return nil end, NO }
    view._dispatch("A", (ws_row("kb1")))
    t.ok(t.wait(5000, function() return offered("Scaffold", at) end), "kb7 is offered the scaffold")
    vim.wait(300)
    t.ok(not offered("primary", at), "dismissing it asks nothing more")
    answers = {}
  end
  -- accepted, the build's own scaffold writes the KB
  local dir5 = t.tmp("kb5")
  vim.fn.mkdir(dir5, "p")
  inputs = { dir5, "kb5" }
  answers = { titled("Scaffold"), NO }
  view._dispatch("A", (ws_row("kb1")))
  t.ok(t.wait(5000, function() return vim.fn.filereadable(dir5 .. "/AGENTS.md") == 1 end),
    "accepted, the scaffold writes AGENTS.md", vim.inspect(notes[#notes]))
  t.ok(t.wait(2000, function() return noted("scaffolded a KB in " .. dir5) end), "and says so")
  -- a build without the scaffold says the folder is not a KB yet
  package.loaded["autodoc.kb.scaffold"] = nil
  package.preload["autodoc.kb.scaffold"] = function() error("not in this build") end
  local dir6 = t.tmp("kb6")
  vim.fn.mkdir(dir6, "p")
  inputs = { dir6, "kb6" }
  answers = { NO }
  view._dispatch("A", (ws_row("kb1")))
  t.ok(t.wait(5000, function() return noted("kb6 is not a KB yet") end), "without the scaffold, it says the folder is not a KB yet")
  package.preload["autodoc.kb.scaffold"] = nil
  -- with a scaffold module, the scaffold is offered and run
  local scaffolded = nil
  package.loaded["autodoc.kb.scaffold"] = { scaffold = function(root, opts)
    scaffolded = { root, opts and opts.name }
    vim.fn.writefile({ "# AGENTS" }, root .. "/AGENTS.md")
    return true
  end }
  local dir4 = t.tmp("kb4")
  vim.fn.mkdir(dir4, "p")
  inputs = { dir4, "kb4" }
  answers = { function(o) return o.title:find("Scaffold", 1, true) and true or nil end, NO }
  view._dispatch("A", (ws_row("kb1")))
  t.ok(t.wait(5000, function() return scaffolded ~= nil end), "the scaffold is offered and run", vim.inspect(modal_seen[#modal_seen]))
  t.eq(scaffolded, { dir4, "kb4" }, "on the folder, with the workspace's name")
  package.loaded["autodoc.kb.scaffold"] = nil
end)

-- The rename's reply and the daemon's workspace.renamed event both move an expanded KB's state to
-- its new name. Each order is forced: the events poll held until the reply has been applied, then
-- the reply held until the event has. An open KB stays open, with its tree, either way.
t.section("r renames, and an open KB stays open whichever of its reply and its event comes first", function()
  t.ok(t.wait(5000, function() return ws_row("kb4") ~= nil end), "kb4 is listed")
  -- a folded KB stays folded: reply and event, in whatever order they come
  local cursor0 = view._st.events_cursor
  inputs = { "kb4-folded" }
  view._dispatch("r", (ws_row("kb4")))
  t.ok(t.wait(5000, function() return noted("renamed kb4 to kb4-folded") and view._st.events_cursor ~= cursor0 end),
    "a folded KB is renamed: reply and event both applied", notes[#notes])
  vim.wait(drawer.POLL_MS * 4)
  t.ok(view._st.expanded["kb4-folded"] == nil and view._st.trees["kb4-folded"] == nil, "and it stays folded",
    vim.inspect(view._st.expanded))
  t.ok(t.wait(5000, function() return ws_row("kb4-folded") ~= nil end), "kb4-folded is listed")
  view._dispatch("<CR>", (ws_row("kb4-folded")))
  t.ok(t.wait(5000, function() local tr = view._st.trees["kb4-folded"]; return tr and not tr.loading end),
    "kb4-folded is opened, its tree listed")

  local live = session.client()
  local real = live.call
  local hold_events, held_events = false, {}
  local hold_reply, held_reply = false, nil
  live.call = function(self, method, params, cb, opts)
    if method == "sys.events" and hold_events then
      held_events[#held_events + 1] = function() return real(self, method, params, cb, opts) end
      return 0
    end
    if method == "workspace.rename" and hold_reply then
      return real(self, method, params, function(res, err) held_reply = function() cb(res, err) end end, opts)
    end
    return real(self, method, params, cb, opts)
  end
  -- the events cursor moves only when a pulled page is applied: the rename's event is the next one
  local function event_applied(at) return function() return view._st.events_cursor ~= at end end
  local function settled(name)
    vim.wait(drawer.POLL_MS * 4) -- the event's relist and any move after it have run
    local tr = view._st.trees[name]
    return view._st.expanded[name] == true and tr ~= nil
  end

  -- the reply first: no events are pulled until it has been applied
  hold_events = true
  local at = view._st.events_cursor
  inputs = { "kb4-moved" }
  view._dispatch("r", (ws_row("kb4-folded")))
  t.ok(t.wait(5000, function() return noted("renamed kb4-folded to kb4-moved") end), "the reply is applied first", notes[#notes])
  hold_events = false
  -- a poll held meanwhile is sent now, the newest only (older replies are dropped as stale anyway);
  -- none held, the next tick pulls the event
  if #held_events > 0 then held_events[#held_events]() end
  held_events = {}
  t.ok(t.wait(5000, event_applied(at)), "then the event")
  t.ok(settled("kb4-moved"), "kb4-moved is still open, its tree kept", vim.inspect(view._st.expanded))
  t.ok(view._st.expanded["kb4-folded"] == nil, "and nothing is left under kb4-folded")

  -- the event first: the reply is held until the event has been applied. Open to begin with (a
  -- drawer that folded it above is opened again, so this order is shown on its own)
  if not view._st.expanded["kb4-moved"] then view._dispatch("<CR>", (ws_row("kb4-moved"))) end
  t.ok(t.wait(5000, function() local tr = view._st.trees["kb4-moved"]; return tr and not tr.loading end),
    "kb4-moved is open, its tree listed")
  hold_reply = true
  inputs = { "kb4-renamed" }
  view._dispatch("r", (ws_row("kb4-moved")))
  t.ok(t.wait(5000, function() return held_reply ~= nil end), "the reply is held")
  t.ok(t.wait(5000, function() return view._st.expanded["kb4-renamed"] == true end),
    "the event is applied first: it moved kb4-moved's state")
  hold_reply = false
  held_reply()
  t.ok(t.wait(5000, function() return noted("renamed kb4-moved to kb4-renamed") end), "then the reply", notes[#notes])
  t.ok(settled("kb4-renamed"), "kb4-renamed is still open, its tree kept", vim.inspect(view._st.expanded))
  live.call = real

  t.ok(t.wait(5000, function()
    return ws_row("kb4-renamed") ~= nil and ws_row("kb4") == nil and ws_row("kb4-folded") == nil and ws_row("kb4-moved") == nil
  end),
    "the renamed workspace is listed", notes[#notes])
  view._dispatch("<CR>", (ws_row("kb4-renamed"))) -- folded again, as the cells after it found it
end)

t.section("e edits a KB's settings", function()
  selects = { function(items) return items[3] end, function(items) return items[2] end }
  view._dispatch("e", (ws_row("kb4-renamed")))
  t.ok(t.wait(5000, function()
    local r = ws_row("kb4-renamed")
    return r and r.workspace.embedding_policy == "when opened"
  end), "the embedding policy is saved through workspace.configure", notes[#notes])
end)

t.section("d removes the index, never the files", function()
  answers = { NO }
  view._dispatch("d", (ws_row("kb2")))
  local seen = modal_seen[#modal_seen]
  t.ok(seen.reversibility == "irreversible", "d asks in an irreversible modal")
  local body = table.concat(seen.body, "\n")
  t.ok(body:find("NOT touched", 1, true) ~= nil, "which says the files are not touched", body)
  vim.wait(300)
  t.ok(ws_row("kb2") ~= nil, "declined: kb2 is kept")
  answers = { YES }
  view._dispatch("d", (ws_row("kb2")))
  t.ok(t.wait(5000, function() return ws_row("kb2") == nil end), "confirmed: kb2's row goes")
  local list = call("workspace.list", {})
  t.ok(not vim.tbl_contains(vim.tbl_map(function(w) return w.name end, list), "kb2"), "and the daemon no longer serves it")
  t.ok(vim.fn.filereadable(kb2 .. "/notes.md") == 1, "its files are still on disk")
  t.eq(session.selected() == "kb2", false, "a removed selection is cleared")
end)

t.section("the Reranker section's dialogs", function()
  selects = { function(items) return items[1] end } -- TEI
  inputs = { "tei-test", "http://127.0.0.1:9" }
  secrets = { "" }
  view._dispatch("a", (section_row("ranker")))
  t.ok(t.wait(5000, function() return find(function(r) return r.kind == "ranker" and r.ranker.name == "tei-test" end) ~= nil end),
    "a adds a reranker through ranker.add")
  local rk = call("ranker.list", {})
  local added
  for _, r in ipairs(rk.rankers) do if r.name == "tei-test" then added = r end end
  t.ok(added and added.kind == "tei" and added.base_url == "http://127.0.0.1:9", "with the form's fields", vim.inspect(added))
  inputs = { "5", "25" }
  view._dispatch("w", (section_row("ranker")))
  t.ok(noted("the window is 10 to 100"), "an out-of-range window is refused and asked again")
  t.ok(t.wait(5000, function() return (call("ranker.list", {}) or {}).window == 25 end), "w sets the window through ranker.window")
  answers = { YES }
  view._dispatch("d", (find(function(r) return r.kind == "ranker" and r.ranker.name == "tei-test" end)))
  t.ok(modal_seen[#modal_seen].reversibility == "irreversible", "removing a reranker asks irreversibly")
  t.ok(t.wait(5000, function() return find(function(r) return r.kind == "ranker" and r.ranker.name == "tei-test" end) == nil end),
    "d removes it")
end)

-- fake_tei serves a TEI re-ranker's /info and /rerank (a score for each text), enough for the daemon
-- to set it up, probe it and use it. It runs in this nvim's loop, which every t.wait turns.
local function fake_tei()
  local srv = assert(vim.uv.new_tcp())
  assert(srv:bind("127.0.0.1", 0))
  srv:listen(16, function()
    local c = vim.uv.new_tcp()
    srv:accept(c)
    local got = ""
    c:read_start(function(_, data)
      if not data then return c:close() end
      got = got .. data
      local head_end = got:find("\r\n\r\n", 1, true)
      if not head_end then return end
      local len = tonumber(got:sub(1, head_end):lower():match("content%-length:%s*(%d+)")) or 0
      if #got < head_end + 3 + len then return end
      c:read_stop()
      local body
      if got:find("^POST /rerank") then
        local req = vim.json.decode(got:sub(head_end + 4, head_end + 3 + len))
        local out = {}
        for i = 1, #req.texts do out[i] = { index = i - 1, score = 1 / i } end
        body = vim.json.encode(out)
      else
        body = vim.json.encode({ model_id = "fake/reranker", model_type = { reranker = vim.empty_dict() },
          max_client_batch_size = 32 })
      end
      c:write("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\nContent-Length: "
        .. #body .. "\r\n\r\n" .. body, function() c:close() end)
    end)
  end)
  return srv, "http://127.0.0.1:" .. srv:getsockname().port
end

t.section("the Reranker's none, and removing the only ranker while it is in use", function()
  local srv, base = fake_tei()
  local ranker_row = function() return find(function(r) return r.kind == "ranker" and r.ranker.name == "tei-live" end) end
  local none_row = function() return find(function(r) return r.kind == "no_ranker" end) end
  t.ok(none_row() == nil, "no none while there is no ranker to choose against")
  selects = { function(items) return items[1] end } -- TEI
  inputs = { "tei-live", base }
  secrets = { "" }
  view._dispatch("a", (section_row("ranker")))
  t.ok(t.wait(5000, function() return ranker_row() ~= nil end), "a ranker is added")
  for i = 1, #rows() do
    t.ok(not line(i):find("this build supplies its ranker", 1, true), "no build ranker is claimed for a build with none", line(i))
  end
  local _, ni = none_row()
  t.ok(ni and line(ni):find("^  ● none") ~= nil, "none is marked while no ranker is used", ni and line(ni))
  view._dispatch("<CR>", (ranker_row()))
  t.ok(t.wait(5000, function() return (call("ranker.list", {}) or {}).active == "tei-live" end), "<CR> uses the ranker", vim.inspect(call("ranker.list", {}), { newline = " ", indent = "" }))
  t.ok(t.wait(5000, function() local _, i = none_row(); return i and line(i):find("^  ○ none") ~= nil end),
    "and none is no longer marked")
  view._dispatch("<CR>", (none_row()))
  t.ok(t.wait(5000, function() return (call("ranker.list", {}) or {}).active == "" end), "<CR> on none stops re-ranking")
  t.ok(t.wait(5000, function() local _, i = none_row(); return i and line(i):find("^  ● none") ~= nil end), "none is marked again")
  t.ok(noted("search is not re-ranked"), "and says so")
  view._dispatch("<CR>", (ranker_row()))
  t.ok(t.wait(5000, function() return (call("ranker.list", {}) or {}).active == "tei-live" end), "in use again")
  answers = { YES }
  view._dispatch("d", (ranker_row()))
  t.ok(t.wait(5000, function()
    local l = call("ranker.list", {}) or {}
    return #(l.rankers or { 1 }) == 0 and l.active == ""
  end), "d removes the only ranker while it is in use, and none is used")
  t.ok(t.wait(5000, function() return ranker_row() == nil and none_row() == nil end), "the section lists neither")
  srv:close()
end)

t.section("the Embedding Models section's dialogs", function()
  selects = { function(items) return items[3] end } -- OpenAI-compatible
  inputs = { "local-oa", "http://127.0.0.1:9", "text-embedding-3-small" }
  secrets = { "" }
  view._dispatch("a", (section_row("embedding")))
  local prow
  t.ok(t.wait(5000, function()
    prow = find(function(r) return r.kind == "provider" and r.provider.name == "local-oa" end)
    return prow ~= nil
  end), "a adds a provider through embedding.add", notes[#notes])
  if prow then
    t.ok(prow.provider.kind == "openai" and prow.provider.model == "text-embedding-3-small", "with the form's fields", vim.inspect(prow.provider))
    local before = #notes
    view._dispatch("<CR>", prow)
    t.ok(t.wait(10000, function()
      for k = before + 1, #notes do if notes[k]:find("use local-oa", 1, true) then return true end end
      return false
    end), "<CR> asks embedding.use, and says why it failed", notes[#notes])
  end
end)

t.section("dispose releases everything", function()
  local v = view
  host.toggle()
  t.eq(host.owner(), nil, "toggle closes the drawer")
  t.eq(v._sub_count(), 0, "no subscription survives")
  t.ok(not v._polling(), "no timer survives")
  t.eq(v:bufnr(), nil, "the buffer is gone")
end)

t.section("the header without a daemon", function()
  local real = session.ensure
  session.ensure = function(cb)
    cb(nil, require("autodoc.lifecycle").describe_manual("`autodoc --print-endpoint` failed: nothing here"))
  end
  local v = drawer.new({ buf_name = "autodoc://drawer-down" })
  local b = v:get_buffer(0)
  local lines = vim.api.nvim_buf_get_lines(b, 0, -1, false)
  session.ensure = real
  t.eq(lines[1], "AutoDoc · not connected", "the header says it is not connected")
  t.ok(table.concat(lines, "\n"):find("By hand:", 1, true) ~= nil, "and gives the manual command", table.concat(lines, "\n"))
  v:dispose()
end)

t.finish()
