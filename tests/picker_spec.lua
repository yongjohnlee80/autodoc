-- <leader>fk against a real daemon built from this checkout: hits as picker items (file, pos,
-- text), the title saying how the answer was made, the stage toggles changing the request (a
-- retriever always stays on), the snacks source's spec, its finder, actions and preview hand-off,
-- and the fallback when snacks is absent.
local t = require("helpers")

local bin = assert(os.getenv("AUTODOC_TEST_BIN"), "AUTODOC_TEST_BIN")
local kb = t.tmp("kb")
vim.fn.mkdir(kb, "p")
local cfg = t.tmp("config.toml")
vim.fn.writefile({
  "[server]",
  string.format("socket = %q", t.tmp("run", "autodoc.sock")),
  string.format("state_dir = %q", t.tmp("state", "autodoc")),
  string.format("data_dir = %q", t.tmp("data", "autodoc")),
  "[follow]",
  'poll_interval = "50ms"',
}, cfg)
vim.fn.writefile({ "# Birds", "", "## Kestrels", "", "kestrel hovering over the field" }, kb .. "/birds.md")
vim.fn.mkdir(t.tmp("project"), "p")
vim.cmd("cd " .. vim.fn.fnameescape(t.tmp("project")))

local autodoc = require("autodoc")
local session = require("autodoc.session")
local picker = require("autodoc.picker")
picker.DEBOUNCE_MS = 10
autodoc.setup({ bin = bin, config = cfg })

local notes = {}
vim.notify = function(msg) notes[#notes + 1] = tostring(msg) end

-- every search.query's params, from the live client
local queries = {}
local function watch_queries()
  local live = session.client()
  if live._watched then return end
  live._watched = true
  local real = live.call
  live.call = function(self, method, params, cb, opts)
    if method == "search.query" then queries[#queries + 1] = vim.deepcopy(params) end
    return real(self, method, params, cb, opts)
  end
end

local function query(q)
  return t.await(10000, function(done) picker.query(q, function(items, result, err) done(items, result, err) end) end)
end

t.section("setup", function()
  local _, err = t.await(20000, function(done) session.call("workspace.add", { "kb", kb }, done) end)
  t.ok(err == nil, "workspace.add", err and err.message)
  session.select("kb")
  watch_queries()
  t.ok(t.wait(15000, function()
    local items = query("kestrel")
    return items and #items > 0
  end), "the KB is indexed")
end)

t.section("a search's hits are picker items", function()
  local items, result = query("kestrel")
  local it = items and items[1]
  t.ok(it ~= nil, "a hit")
  if not it then return end
  t.eq(it.file, vim.fs.normalize(kb) .. "/birds.md", "file is the root and the hit's path")
  t.ok(type(it.pos) == "table" and it.pos[1] == result.hits[1].line_start and it.pos[2] == 0, "pos is the hit's first line", vim.inspect(it.pos))
  t.ok(it.pos[1] >= 3, "the line of the section, not the file's top", it.pos[1])
  t.ok(it.text:find(" · ", 1, true) ~= nil and it.text:find("kestrel", 1, true) ~= nil, "text is breadcrumb · snippet", it.text)
end)

t.section("the title says how the answer was made", function()
  local _, result = query("kestrel")
  local title = picker.title(result, "kb")
  t.ok(title:find("lexical", 1, true) ~= nil, "the stages performed", title)
  local skipped = result.stages and result.stages.skipped or {}
  if skipped.semantic then
    t.ok(title:find("semantic skipped: " .. skipped.semantic, 1, true) ~= nil, "and why semantic did not run", title)
    t.ok(title:find("lexical only", 1, true) ~= nil, "lexical only, when it is the one stage", title)
  end
  t.eq(picker.title(nil, "kb"), "KB kb · lexical + semantic + rerank", "before an answer: the stages asked for")
end)

t.section("the stage toggles change the request", function()
  t.eq(picker.request_opts().stages, nil, "every stage on: no stages sent (the daemon's auto)")
  queries = {}
  query("kestrel")
  t.ok(queries[1] and (queries[1][3] == nil or queries[1][3].stages == nil), "the request carries no stages", vim.inspect(queries[1]))
  t.ok(picker.toggle("semantic"), "semantic off")
  t.eq(picker.request_opts().stages, { "lexical", "rerank" }, "lexical and rerank asked")
  local changed, why = picker.toggle("lexical")
  t.ok(not changed and why:find("retriever must stay on", 1, true) ~= nil, "the last retriever cannot be turned off", why)
  t.eq(picker.request_opts().stages, { "lexical", "rerank" }, "a refused toggle changes nothing")
  t.ok(picker.toggle("rerank"), "rerank off")
  queries = {}
  local items, result = query("kestrel")
  t.eq(queries[1] and queries[1][3] and queries[1][3].stages, { "lexical" }, "the request asks lexical alone")
  t.eq(result and result.stages and result.stages.requested, { "lexical" }, "and the daemon says so")
  t.ok(items and #items > 0, "with hits")
  t.eq(picker.title(result, "kb"), "KB kb · lexical only", "the title follows")
  picker.toggle("semantic"); picker.toggle("rerank")
  t.eq(picker.request_opts().stages, nil, "all back on")
end)

-- a coroutine standing in for snacks' Async: sleep, suspend and resume
local function run_finder(fn, search)
  local got, finished = {}, false
  local co
  local async = {
    sleep = function(_, ms)
      local c = coroutine.running()
      vim.defer_fn(function() coroutine.resume(c) end, ms)
      coroutine.yield()
    end,
    suspend = function() coroutine.yield() end,
    resume = function()
      vim.schedule(function() if coroutine.status(co) == "suspended" then coroutine.resume(co) end end)
    end,
  }
  local fake_picker = { title = "", update_titles = function() end }
  local ret = fn({}, { filter = { search = search }, async = async, picker = fake_picker })
  if type(ret) ~= "function" then return ret, fake_picker end
  co = coroutine.create(function()
    ret(function(item) got[#got + 1] = item end)
    finished = true
  end)
  coroutine.resume(co)
  t.wait(10000, function() return finished end)
  return got, fake_picker
end

t.section("the snacks source", function()
  local spec
  package.loaded["snacks"] = { picker = { pick = function(s) spec = s end } }
  picker.open()
  t.ok(spec ~= nil and spec.source == "autodoc", "snacks.picker.pick is given the autodoc source")
  if not spec then return end
  t.ok(spec.live == true and spec.confirm == "jump" and spec.preview == "file", "live, previewed as a file, jumped through snacks' jump")
  local keys = spec.win.input.keys
  t.ok(keys["<M-l>"] and keys["<M-s>"] and keys["<M-r>"], "<M-l> <M-s> <M-r> toggle the stages")
  t.eq(keys["<M-s>"][1], "autodoc_semantic", "<M-s> runs the semantic toggle")
  local items, fake = run_finder(spec.finder, "kestrel")
  t.ok(#items > 0 and items[1].file and items[1].pos and items[1].text, "the finder yields items with file, pos and text", vim.inspect(items[1]))
  t.ok(fake.title:find("lexical", 1, true) ~= nil, "and retitles the picker by the answer", fake.title)
  t.eq(#run_finder(spec.finder, "   "), 0, "an empty query asks nothing")
  local found = 0
  local p = { title = "", update_titles = function() end, find = function(_, o) found = found + ((o and o.refresh) and 1 or 0) end }
  spec.actions.autodoc_semantic(p)
  t.ok(picker.enabled.semantic == false and found == 1, "the semantic action toggles it and searches again")
  spec.actions.autodoc_lexical(p)
  t.ok(picker.enabled.lexical == true and found == 1, "the lexical action is refused while it is the last retriever")
  spec.actions.autodoc_semantic(p)
  local sent, offered
  local real_select = vim.ui.select
  vim.ui.select = function(slots, _, cb) offered = slots; cb(slots[2]) end
  package.loaded["autodoc.preview"] = { render_path = function(slot, path) sent = { slot, path } end }
  spec.actions.autodoc_preview(p, items[1])
  local slots = require("autodoc.preview.layout").SLOTS
  t.eq(offered, slots, "<M-p> offers the preview's slots")
  t.eq(sent, { slots[2], items[1].file }, "and renders the hit's file into the slot chosen: render_path(slot, path)")
  sent = nil
  vim.ui.select = function(_, _, cb) cb(nil) end
  spec.actions.autodoc_preview(p, items[1])
  t.eq(sent, nil, "a dismissed prompt renders nothing")
  vim.ui.select = function(s, _, cb) cb(s[1]) end
  package.loaded["autodoc.preview"] = { render_path = function() error("boom") end }
  spec.actions.autodoc_preview(p, items[1])
  t.ok(notes[#notes]:find("preview: .*boom") ~= nil, "a render that fails says so", notes[#notes])
  vim.ui.select = real_select
  -- the preview ships in this build: a build without it is a require that fails
  package.loaded["autodoc.preview"] = nil
  package.preload["autodoc.preview"] = function() error("not in this build") end
  spec.actions.autodoc_preview(p, items[1])
  t.ok(notes[#notes]:find("preview is not in this build", 1, true) ~= nil, "without autodoc.preview it says so")
  package.preload["autodoc.preview"] = nil
  package.loaded["snacks"] = nil
end)

t.section("without snacks, the fallback", function()
  t.eq(picker.snacks(), nil, "snacks is absent")
  local listed
  vim.ui.input = function(_, cb) cb("kestrel") end
  vim.ui.select = function(items, opts, cb)
    listed = { items = items, prompt = opts.prompt, first = opts.format_item(items[1]) }
    cb(items[1])
  end
  picker.open()
  t.ok(t.wait(10000, function() return listed ~= nil end), "the hits are offered in vim.ui.select")
  if not listed then return end
  t.ok(listed.first:find("birds.md:", 1, true) ~= nil, "each as path:line and text", listed.first)
  t.ok(listed.prompt:find("lexical", 1, true) ~= nil, "titled by how the answer was made", listed.prompt)
  t.eq(vim.api.nvim_buf_get_name(0), vim.fs.normalize(kb) .. "/birds.md", "choosing one opens its file")
  t.eq(vim.api.nvim_win_get_cursor(0)[1], listed.items[1].pos[1], "at the hit's line")
end)

t.section("no KB selected", function()
  session.select(nil)
  local items, _, err = query("kestrel")
  t.ok(items == nil and err and err.message:find("no KB selected", 1, true), "the picker says to select one", err and err.message)
  session.select("kb")
end)

t.finish()
