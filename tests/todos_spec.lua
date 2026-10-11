-- autodoc.todos against a real daemon built from this checkout: the todo verbs at protocol 20,
-- a write refused until the store is claimed, then the store's whole life through the client.
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

-- run calls a todos function and waits for its answer
local function run(fn, ...)
  local args = { ... }
  return t.await(15000, function(done)
    args[#args + 1] = done
    fn(unpack(args))
  end)
end

t.section("the session speaks protocol 20", function()
  t.eq(client.PROTOCOL, 20, "this plugin's protocol")
  local _, err = t.await(20000, function(done) session.call("workspace.add", { "kb", kb }, done) end)
  t.ok(err == nil, "workspace.add", err and err.message)
  t.ok(api.todos == todos, "the API carries the todos client")
end)

t.section("before the claim: reads answer, writes are refused", function()
  local r, err = run(todos.resolve, "kb")
  t.ok(err == nil and r and r.claimed == false and r.writable == true, "resolve: writable, not claimed", vim.inspect(r or err))
  local _, serr = run(todos.scaffold, "kb")
  t.eq(serr and serr.code, -32077, "a write before the claim is todos.unclaimed")
  local _, lerr = run(todos.list, "kb")
  t.eq(lerr and lerr.code, -32074, "no store yet: todos.not_a_store")
end)

t.section("a claimed store's life", function()
  local _, cerr = run(todos.claim, "kb")
  t.ok(cerr == nil, "claim", cerr and cerr.message)
  local opened, oerr = run(todos.scaffold, "kb")
  t.ok(oerr == nil and opened.scaffolded == true, "scaffold", vim.inspect(opened or oerr))
  t.ok(vim.fn.isdirectory(kb .. "/.todo-list/open") == 1, "the buckets are on disk")
  local task, aerr = run(todos.add, "kb", { title = "From Neovim", priority = "normal" })
  t.ok(aerr == nil and task and task.status == "open", "add", vim.inspect(task or aerr))
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
  local stores = run(todos.stores)
  t.ok(stores and #stores >= 1, "stores", vim.inspect(stores))
  local _, relerr = run(todos.release, "kb")
  t.ok(relerr == nil, "release", relerr and relerr.message)
end)

t.section("the client's own checks", function()
  local ok, msg = pcall(todos.get, "kb", function() end)
  t.ok(not ok and tostring(msg):find("takes 2 to 2 params", 1, true), "too few params raise at the call", msg)
  ok, msg = pcall(todos.get, "kb", "id")
  t.ok(not ok and tostring(msg):find("callback", 1, true), "a missing callback raises", msg)
  local r = run(todos.list, "kb", nil) -- a trailing nil is a param left out
  t.ok(r ~= nil, "a trailing nil is left out", vim.inspect(r))
  local _, err = t.await(10000, function(cb) todos.call("nope", {}, cb) end)
  t.eq(err and err.code, -32601, "an unknown todos verb is the daemon's method not found")
end)

t.finish()
