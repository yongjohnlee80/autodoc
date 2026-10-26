-- autodoc.todos against a real daemon built from this checkout: the todo verbs at protocol 20,
-- a workspace's store read as an empty one until its first write, which makes and claims it, then
-- the store's whole life through the client, its release and its claim again (ADR 1791532020 r13).
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
}, cfg)

local autodoc = require("autodoc")
local client = require("autodoc.client")
local session = require("autodoc.session")
local api = require("autodoc.api")
local todos = require("autodoc.todos")
autodoc.setup({ bin = bin, config = cfg })

-- run calls a todos function and waits for its answer. The count is select's, not #args, so a
-- trailing nil is passed as given.
local function run(fn, ...)
  local n = select("#", ...)
  local args = { ... }
  return t.await(15000, function(done)
    args[n + 1] = done
    fn(unpack(args, 1, n + 1))
  end)
end

-- the todos verbs arrived in protocol 20: this plugin's protocol (the generated contracts') has them
t.section("the session speaks a protocol with the todos verbs", function()
  t.ok(client.PROTOCOL >= 20, "this plugin's protocol has the todos verbs", client.PROTOCOL)
  local _, err = t.await(20000, function(done) session.call("workspace.add", { "kb", kb }, done) end)
  t.ok(err == nil, "workspace.add", err and err.message)
  t.ok(api.todos == todos, "the API carries the todos client")
end)

t.section("before the first write: the store reads as an empty one, and nothing is made", function()
  local r, err = run(todos.resolve, "kb")
  t.ok(err == nil and r and r.claimed == false and r.writable == true and not r.released,
    "resolve: writable, not claimed, not released", vim.inspect(r or err))
  local listing, lerr = run(todos.list, "kb")
  t.ok(lerr == nil and listing and #listing.tasks == 0 and #listing.malformed == 0 and listing.cursor == 0,
    "no store yet: list answers an empty store", vim.inspect(listing or lerr))
  local _, gerr = run(todos.get, "kb", "nope")
  t.eq(gerr and gerr.code, -32072, "no store yet: get is todos.not_found")
  t.ok(vim.fn.isdirectory(kb .. "/.todo-list") == 0, "the reads made nothing on disk")
  local after = run(todos.resolve, "kb")
  t.ok(after and after.claimed == false, "the reads claimed nothing", vim.inspect(after))
end)

t.section("a store's life from its first write", function()
  local task, aerr = run(todos.add, "kb", { title = "From Neovim", priority = "medium" })
  t.ok(aerr == nil and task and task.status == "open", "the first add, with no claim or scaffold first", vim.inspect(task or aerr))
  t.ok(vim.fn.isdirectory(kb .. "/.todo-list/open") == 1, "the first add made the store's buckets")
  local r = run(todos.resolve, "kb")
  t.ok(r and r.claimed == true and not r.released, "the first add claimed the store", vim.inspect(r))
  local opened, oerr = run(todos.scaffold, "kb")
  t.ok(oerr == nil and opened.scaffolded == false, "scaffold after the first add: nothing left to make", vim.inspect(opened or oerr))
  local listing = run(todos.list, "kb", { buckets = { "open" } })
  t.eq(listing and #listing.tasks, 1, "listed")
  local got = run(todos.get, "kb", task.id)
  t.eq(got and got.title, "From Neovim", "get")
  local updated, uerr = run(todos.update, "kb", task.id, got.rev, { priority = "high" })
  t.ok(uerr == nil and updated.priority == "high", "update", vim.inspect(updated or uerr))
  local _, stale = run(todos.update, "kb", task.id, got.rev, { priority = "low" })
  t.eq(stale and stale.code, -32071, "a stale rev is todos.stale")
  local assigned = run(todos.assign, "kb", task.id, updated.rev, "agent:jarvis", "yours")
  t.ok(assigned and assigned.status == "in-progress", "assigning an open task makes it in-progress", vim.inspect(assigned))
  local done = run(todos.status, "kb", task.id, assigned.rev, "completed")
  t.ok(done and done.status == "completed", "status", vim.inspect(done))
  local page = run(todos.changes, "kb", "autodoc.nvim-test", 50)
  t.ok(page and #page.changes >= 4, "the change log has the writes", vim.inspect(page))
  local _, rerr = run(todos.remove, "kb", task.id, done.rev)
  t.ok(rerr == nil, "remove", rerr and rerr.message)
  local legacy, lerr2 = run(todos.add, "kb", { title = "Given the legacy spelling", priority = "normal" })
  t.ok(lerr2 == nil and legacy and legacy.priority == "medium", "normal is medium's legacy spelling: accepted, written medium", vim.inspect(legacy or lerr2))
  local stores = run(todos.stores)
  t.ok(stores and #stores >= 1, "stores", vim.inspect(stores))
end)

t.section("a released store: reads answer, writes are todos.unclaimed until todos.claim", function()
  local _, relerr = run(todos.release, "kb")
  t.ok(relerr == nil, "release", relerr and relerr.message)
  local r = run(todos.resolve, "kb")
  t.ok(r and r.claimed == false and r.released == true, "resolve: released", vim.inspect(r))
  local _, werr = run(todos.add, "kb", { title = "After the release" })
  t.eq(werr and werr.code, -32077, "a write to a released store is todos.unclaimed")
  local listing, lerr = run(todos.list, "kb")
  t.ok(lerr == nil and listing ~= nil, "a released store still reads", vim.inspect(listing or lerr))
  local _, cerr = run(todos.claim, "kb")
  t.ok(cerr == nil, "claim", cerr and cerr.message)
  local again, aerr = run(todos.add, "kb", { title = "Claimed again" })
  t.ok(aerr == nil and again and again.status == "open", "a write after the claim", vim.inspect(again or aerr))
end)

t.section("the verbs are the contracts'", function()
  local contracts = require("autodoc.contracts")
  local contract = require("autodoc.contract")
  t.eq(client.PROTOCOL, contracts.protocol, "the plugin's protocol is its contracts'")
  local bound, want = 0, 0
  for verb, c in pairs(contracts.verbs) do
    local name = verb:match("^todos%.(.+)$")
    if name then
      want = want + 1
      if type(todos[name]) == "function" and todos.VERBS[name] then bound = bound + 1 end
      local params = {}
      for i, p in ipairs(c.params) do params[i] = p.name .. (p.optional and "?" or "") end
      t.eq(table.concat(todos.VERBS[name] or {}, ","), table.concat(params, ","), verb .. "'s params are its contract's")
    end
  end
  t.ok(want > 0 and bound == want, "every todos.* contract is a function", bound .. " of " .. want)
  local sys = contract.domain("sys")
  t.ok(sys.contracts and not sys.hello and not sys.shutdown, "the protocol's own verbs are not bound", vim.inspect(sys))
  t.eq(contract.params("no.such"), nil, "a verb the contracts lack has no params")
  local schema = require("autodoc.verbs").SPECS["kb.search"].schema
  t.eq(vim.inspect(schema), vim.inspect({ workspace = "string?", query = "string", opts = "table?" }),
    "kb.search's args are search.query's params, its workspace defaulted")
end)

t.section("the client's own checks", function()
  local ok, msg = pcall(todos.get, "kb", function() end)
  t.ok(not ok and tostring(msg):find("takes 2 to 2 params", 1, true), "too few params raise at the call", msg)
  ok, msg = pcall(todos.get, "kb", "id")
  t.ok(not ok and tostring(msg):find("callback", 1, true), "a missing callback raises", msg)
  -- a trailing nil is a param left out: list("kb", nil, cb) is list("kb", cb), not a nil opts
  local sent
  local real = todos.call
  todos.call = function(verb, params, cb) sent = params; return real(verb, params, cb) end
  local r = run(todos.list, "kb", nil)
  todos.call = real
  t.ok(r ~= nil and sent and #sent == 1 and sent[1] == "kb", "a trailing nil is left out", vim.inspect(sent))
  local _, err = t.await(10000, function(cb) todos.call("nope", {}, cb) end)
  t.eq(err and err.code, -32601, "an unknown todos verb is the daemon's method not found")
end)

t.finish()
