-- The offer to link the project's primary KB to an AutoDoc workspace (autodoc.kb.link), against a
-- real daemon built from this checkout. auto-core.kb is a stub that records what set_primary was
-- given; vim.ui.select / vim.ui.input are scripted. Each cell asserts the effect: the record
-- auto-core was handed, the daemon's workspaces, the preference file, and what was asked.
local t = require("helpers")

local bin = assert(os.getenv("AUTODOC_TEST_BIN"), "AUTODOC_TEST_BIN")
local sock = t.tmp("run", "autodoc.sock")
local cfg = t.tmp("config.toml")
vim.fn.writefile({
  "[server]",
  string.format("socket = %q", sock),
  string.format("state_dir = %q", t.tmp("state", "autodoc")),
  string.format("data_dir = %q", t.tmp("data", "autodoc")),
  "[follow]",
  'poll_interval = "50ms"',
}, cfg)
local function kbdir(name)
  local d = t.tmp(name)
  vim.fn.mkdir(d, "p")
  vim.fn.writefile({ "# Agents" }, d .. "/AGENTS.md")
  return vim.fs.normalize(d)
end

-- auto-core.kb: the primary record, and what set_primary was handed
local primary, handed
package.loaded["auto-core.kb"] = {
  primary = function() return primary end,
  set_primary = function(_, spec, opts)
    if not (opts and opts.confirmed == true) then return false, "not_confirmed" end
    handed = { workspace = spec.workspace, root = spec.root }
    primary = vim.deepcopy(handed)
    return true, nil
  end,
}

-- scripted prompts: the next answers, and what was asked
local selects, inputs, answers = {}, {}, {}
vim.ui.select = function(items, opts, cb)
  selects[#selects + 1] = { items = items, prompt = opts.prompt }
  local idx = table.remove(answers, 1)
  vim.schedule(function() cb(idx and items[idx] or nil, idx) end)
end
vim.ui.input = function(opts, cb)
  inputs[#inputs + 1] = opts
  local v = table.remove(answers, 1)
  vim.schedule(function() cb(v) end)
end
local function reset()
  selects, inputs, answers, handed = {}, {}, {}, nil
end

local autodoc = require("autodoc")
local session = require("autodoc.session")
local link = require("autodoc.kb.link")
autodoc.setup({ bin = bin, config = cfg, offer_link = false })

local function list()
  local got
  t.await(10000, function(done) session.workspaces(function(l) got = l; done() end) end)
  return got or {}
end
local function named(l, name)
  for _, w in ipairs(l) do if w.name == name then return w end end
end

t.section("what needs linking", function()
  t.eq(link.gap({ root = "/a/kb" }), "/a/kb", "a primary known by its root alone is a gap")
  t.eq(link.gap({ root = "/a/kb", workspace = "kb" }), nil, "one naming its workspace is not")
  t.eq(link.gap(nil), nil, "no primary is not")
  t.eq(link.default_name(vim.fn.stdpath("config") .. "/.auto-agents-config/kb"), "AutoVimKB",
    "AutoVim's global KB is offered AutoVimKB")
  t.eq(link.default_name("/src/myproj/.auto-agents/kb"), "myproj", "a project-local KB is offered its project's name")
  t.eq(link.default_name("/notes/vault"), "vault", "any other folder its own name")
end)

t.section("a workspace already serves the root: link it, add nothing", function()
  reset()
  local root = kbdir("served")
  t.await(10000, function(done) require("autodoc.api").add_location("Served", root, function() done() end) end)
  local before = #list()
  primary = { root = root }
  link.reset_for_tests()
  answers = { 1 }
  link.check(list())
  t.ok(t.wait(5000, function() return handed ~= nil end), "set_primary is called")
  t.eq(handed, { workspace = "Served", root = root }, "with the serving workspace's name and the root, confirmed")
  t.ok(selects[1] and selects[1].items[1]:find("Served", 1, true) ~= nil, "the question names the workspace", vim.inspect(selects[1]))
  t.eq(#inputs, 0, "no name is asked")
  t.eq(#list(), before, "no workspace is added")
end)

t.section("none serves it: add it under the confirmed name, then link it", function()
  reset()
  local root = kbdir("fresh")
  primary = { root = root }
  link.reset_for_tests()
  answers = { 1, "FreshKB" }
  link.check(list())
  t.ok(t.wait(10000, function() return handed ~= nil end), "set_primary is called")
  t.eq(inputs[1] and inputs[1].default, "fresh", "the name is asked, prefilled with the default")
  local w = named(list(), "FreshKB")
  t.ok(w and vim.fs.normalize(w.root) == root, "the daemon serves the root as FreshKB", vim.inspect(w))
  t.eq(handed, { workspace = "FreshKB", root = root }, "and it is linked under that name")
end)

t.section("asked once per root per session; not now changes nothing", function()
  reset()
  local root = kbdir("later")
  primary = { root = root }
  link.reset_for_tests()
  answers = { 2 }
  link.check(list())
  t.wait(2000, function() return #selects == 1 end)
  vim.wait(200)
  t.eq(#selects, 1, "asked")
  t.eq(handed, nil, "not now: nothing linked")
  t.ok(not link.dismissed()[root], "and nothing remembered")
  link.check(list())
  vim.wait(300)
  t.eq(#selects, 1, "a second listing in the session does not ask again")
end)

t.section("don't ask again is kept, and :AutodocLinkKb asks anyway", function()
  reset()
  local root = kbdir("never")
  primary = { root = root }
  link.reset_for_tests()
  answers = { 3 }
  link.check(list())
  t.ok(t.wait(3000, function() return link.dismissed()[root] == true end), "the root is remembered in the state file")
  t.ok(vim.uv.fs_stat(vim.fn.stdpath("state") .. "/" .. link.STATE_FILE) ~= nil, "under stdpath('state')")
  link.reset_for_tests() -- a new session
  link.check(list())
  vim.wait(300)
  t.eq(#selects, 1, "a later session does not ask about a dismissed root")
  answers = { 1, "NeverKB" }
  vim.cmd("AutodocLinkKb")
  t.ok(t.wait(10000, function() return handed ~= nil end), ":AutodocLinkKb asks, dismissed or not, and links")
  t.eq(handed and handed.workspace, "NeverKB", "under the confirmed name")
end)

t.section("the listing offers it when offer_link is on, and not when off", function()
  reset()
  local root = kbdir("listed")
  primary = { root = root }
  link.reset_for_tests()
  session.configure({ bin = bin, config = cfg, offer_link = false })
  list()
  vim.wait(300)
  t.eq(#selects, 0, "offer_link = false: a listing asks nothing")
  session.configure({ bin = bin, config = cfg, offer_link = true })
  answers = { 2 }
  list()
  t.ok(t.wait(3000, function() return #selects == 1 end), "offer_link = true: the listing asks")
  primary = { root = root, workspace = "Whatever" }
  link.reset_for_tests()
  list()
  vim.wait(300)
  t.eq(#selects, 1, "a primary that names its workspace is never asked about")
end)

t.finish()
