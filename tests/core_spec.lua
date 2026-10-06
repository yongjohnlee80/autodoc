-- The foundation against a real daemon built from this checkout: the binary found and asked for
-- the endpoint, the daemon started through --ensure, the handshake at this plugin's protocol, the
-- API, the save hook, the kb.* verbs, one connect for concurrent callers, and the epoch guard.
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

local autodoc = require("autodoc")
local client = require("autodoc.client")
local lifecycle = require("autodoc.lifecycle")
local session = require("autodoc.session")
local api = require("autodoc.api")
autodoc.setup({ bin = bin, config = cfg })

t.section("lifecycle", function()
  local path, err = lifecycle.resolve_binary(bin)
  t.eq(path, bin, "opts.bin is honoured")
  t.ok(err == nil, "no error for an executable opts.bin")
  local missing, merr = lifecycle.resolve_binary("/nowhere/autodoc")
  t.ok(missing == nil and merr:find("not executable", 1, true) ~= nil, "a missing opts.bin is refused, never replaced", merr)
  t.eq(lifecycle.parse_endpoint("unix\t/run/x.sock\n"), "/run/x.sock", "the endpoint line is parsed")
  t.eq(lifecycle.parse_endpoint("tcp\t127.0.0.1:1\n"), nil, "anything but a unix endpoint is not taken")
  t.eq(lifecycle.parse_endpoint(""), nil, "no line, no endpoint")
end)

t.section("mismatch direction", function()
  t.eq(client.mismatch({ protocol = client.PROTOCOL, min_protocol = 12 }), nil, "a daemon whose range holds this plugin is no mismatch")
  local older = client.mismatch({ protocol = client.PROTOCOL - 1, min_protocol = 12 })
  t.ok(older and older:find("DAEMON is older", 1, true), "a daemon below this plugin's protocol is the older side", older)
  local newer = client.mismatch({ protocol = client.PROTOCOL + 5, min_protocol = client.PROTOCOL + 1 })
  t.ok(newer and newer:find("PLUGIN is older", 1, true), "a daemon whose floor is above this plugin says update the plugin", newer)
end)

local c
t.section("connect through --ensure", function()
  local connects = 0
  local real = client.connect
  client.connect = function(opts, cb)
    connects = connects + 1
    return real(opts, cb)
  end
  local results = {}
  for i = 1, 3 do
    session.ensure(function(cl, err) results[i] = { cl, err } end)
  end
  t.ok(t.wait(20000, function() return results[1] and results[2] and results[3] end), "three concurrent callers all settle")
  client.connect = real
  c = results[1] and results[1][1]
  t.ok(c ~= nil, "a session, the daemon started by the binary", results[1] and results[1][2])
  t.ok(results[2][1] == c and results[3][1] == c, "concurrent callers share one session")
  t.ok(connects <= 2, "one connect for three callers (a dial to nothing, then the ensured one)", connects)
  local hello = c and c:hello() or {}
  t.eq(hello.protocol, client.PROTOCOL, "the session is at this plugin's protocol")
  t.ok(c and c:can("index.documents"), "the session may call index.documents")
end)

t.section("api", function()
  local _, err = t.await(10000, function(done) session.call("workspace.add", { "kb", kb }, done) end)
  t.ok(err == nil, "workspace.add", err and err.message)
  local list = t.await(10000, function(done) api.workspaces(done) end)
  local names = vim.tbl_map(function(w) return w.name end, list or {})
  t.ok(vim.tbl_contains(names, "kb"), "the workspace is listed", vim.inspect(names))
  api.select("kb")
  t.eq(api.selected(), "kb", "the selected KB")
  local hits
  t.ok(t.wait(15000, function()
    local r = t.await(5000, function(done) api.search("kestrel", nil, done) end)
    hits = r and r.hits or nil
    return hits and #hits > 0
  end), "a search finds the indexed file")
  if hits and hits[1] then
    t.eq(hits[1].path, "birds.md", "the hit's path")
    t.ok(type(hits[1].line_start) == "number" and hits[1].line_start >= 3, "the hit says its lines", vim.inspect(hits[1]))
  end
  local docs = t.await(10000, function(done) api.documents("kb", { sort = "path" }, done) end)
  t.ok(docs and docs.docs and docs.docs[1] and docs.docs[1].path == "birds.md", "index.documents lists the file", vim.inspect(docs))
end)

t.section("the save hook", function()
  local path = kb .. "/plover.md"
  local reindexed = nil
  local live = session.client()
  local real_call = live.call
  live.call = function(self, method, params, cb, opts)
    if method == "index.reindex" then reindexed = params end
    return real_call(self, method, params, cb, opts)
  end
  vim.cmd("edit " .. vim.fn.fnameescape(path))
  vim.api.nvim_buf_set_lines(0, 0, -1, false, { "# Plovers", "", "plover running along the shore" })
  vim.cmd("silent write")
  t.ok(t.wait(10000, function()
    local r = t.await(5000, function(done) api.search("plover", nil, done) end)
    return r and r.hits and #r.hits > 0
  end), "a file saved in Neovim becomes searchable (the hook queues it; the watcher is the backstop)")
  live.call = real_call
  t.eq(reindexed, { "kb", "plover.md" }, "the save asked the daemon to index the file at once")
  t.eq((session.workspace_of(path)), "kb", "the saved file's workspace is found by its root")
  t.eq(select(2, session.workspace_of(path)), "plover.md", "and its path relative to the root")
end)

t.section("verbs", function()
  local commands = require("auto-core.mailbox.commands")
  local spec = commands.get("kb.workspaces")
  t.ok(spec ~= nil and spec.owner == "autodoc", "kb.workspaces is registered by autodoc")
  local list = spec and spec.handler({}, {}) or {}
  t.ok(type(list) == "table" and #list >= 1, "kb.workspaces answers the workspaces", vim.inspect(list))
  local search = commands.get("kb.search")
  local res = search and search.handler({ query = "kestrel", workspace = "kb" }, {}) or {}
  t.ok(type(res) == "table" and res.hits and #res.hits > 0, "kb.search answers hits", vim.inspect(res))
  local info = commands.get("kb.info").handler({}, {})
  t.ok(info.connected and info.protocol == client.PROTOCOL, "kb.info says the session", vim.inspect(info))
  t.ok(commands.get("kb.set_primary") == nil, "kb.set_primary is not a verb")
end)

t.section("a late loss from an old session", function()
  local current = session.client()
  local at = session.epoch()
  -- an old client's loss arriving after this one connected: it must not end the current session
  local old = setmetatable({}, { __index = current })
  session._on_lost_for_tests("an old socket closed", old)
  t.ok(session.client() == current and session.epoch() == at, "a loss from a client that is not current ends nothing")
end)

t.section("capabilities unknown", function()
  local c = require("autodoc.client")
  local fake = setmetatable({ _verbs = {}, _verbs_known = false }, { __index = getmetatable(session.client()).__index })
  t.ok(fake:can("index.documents"), "a session whose capabilities could not be read lets calls through")
  fake._verbs_known = true
  t.ok(not fake:can("index.documents"), "a session with known verbs refuses one it lacks")
end)

t.section("the epoch guard", function()
  local at = session.epoch()
  local ran = false
  local late = session.guarded(function() ran = true end)
  local disconnected = false
  local events = require("auto-core.events")
  local sub = events.subscribe(session.TOPIC_DISCONNECTED, function() disconnected = true end)
  c:call("sys.shutdown", {}, function() end)
  t.ok(t.wait(10000, function() return disconnected end), "the shutdown ends the session")
  pcall(events.unsubscribe, sub)
  t.ok(session.epoch() > at, "the epoch moved on")
  late()
  t.ok(not ran, "a callback from the ended epoch is dropped")
end)

t.section("session.request across a connect", function()
  t.ok(not session.is_ready(), "no session after the shutdown")
  local before = session.epoch()
  local list, err = t.await(20000, function(done) session.request("workspace.list", {}, done) end)
  t.ok(session.epoch() > before, "the request connected a new session")
  t.ok(err == nil and type(list) == "table" and #list >= 1,
    "its reply is answered, not dropped as the epoch before the connect", vim.inspect({ list, err }))
  session.remember_workspaces({ { name = "other", root = "/elsewhere" } })
  t.eq(session.workspace("kb"), nil, "remember_workspaces replaces the kept listing")
  t.eq(session.cached_workspaces(), { "other" }, "and its names are what completion offers")
  session.remember_workspaces(list)
  t.eq(session.workspace("kb") and session.workspace("kb").root, kb, "a workspace is found by name in the kept listing")
end)

t.section("a listed KB's managed files are brought up to date, once", function()
  local scaffold = require("autodoc.kb.scaffold")
  local old = t.tmp("oldkb")
  scaffold.scaffold(old, { autodoc_version = "0.0.1", date = "2026-10-06" })
  local plain = t.tmp("plainfolder")
  vim.fn.mkdir(plain, "p")
  session.remember_workspaces({ { name = "oldkb", root = old }, { name = "plain", root = plain } })
  local ops = table.concat(vim.fn.readfile(old .. "/KB_OPERATIONS.md"), "\n")
  t.eq(scaffold.declared_version(ops), require("autodoc.kb.version").autodoc, "an older KB_OPERATIONS.md takes this build's copy")
  t.eq(vim.fn.filereadable(plain .. "/KB_OPERATIONS.md"), 0, "a folder without AGENTS.md is not a KB: nothing is written")
  vim.fn.writefile({ "---", "autodoc_version: 0.0.1", "---", "# edited after the refresh" }, old .. "/KB_OPERATIONS.md")
  session.remember_workspaces({ { name = "oldkb", root = old } })
  t.eq(vim.fn.readfile(old .. "/KB_OPERATIONS.md")[4], "# edited after the refresh", "the same root is refreshed once per session")
end)

t.finish()
