-- The kb drawer hosted by auto-finder, against the real auto-finder.nvim and a real daemon: the
-- cases each plugin's own suite cannot cover, because each needs the other on the runtimepath
-- (as autodb's tests/composition.lua does for the dbase drawer, ADR-0078).
--
--   C2  auto-core + auto-finder + AutoDoc: auto-finder's setup registers its kb section as the
--       drawer's host unprompted; the drawer mounts there with auto-finder's identity (filetype,
--       b:auto_finder_view, buffer name), lists the KB from the daemon, and no AutoDoc panel opens;
--       closing the section releases it.
--   C4  a runtime host transition: self-hosted first, then auto-finder gains the kb section and
--       takes over; never two live views.
--
-- Not a *_spec.lua: run-all.sh runs it only when AUTODOC_TEST_AUTOFINDER names an auto-finder.nvim
-- checkout (the sibling one is used when it has the kb section).
local t = require("helpers")

local af = assert(os.getenv("AUTODOC_TEST_AUTOFINDER"), "AUTODOC_TEST_AUTOFINDER")
vim.opt.runtimepath:append(af)

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
local kb = t.tmp("kb")
vim.fn.mkdir(kb .. "/adrs", "p")
vim.fn.writefile({ "# AGENTS" }, kb .. "/AGENTS.md")
vim.fn.writefile({ "# First", "", "a decision about kestrels" }, kb .. "/adrs/0001-first.md")
vim.fn.mkdir(t.tmp("project"), "p")
vim.cmd("cd " .. vim.fn.fnameescape(t.tmp("project")))

local autodoc = require("autodoc")
local session = require("autodoc.session")
local drawer = require("autodoc.views.drawer")
local host = require("autodoc.views.host")
local panel = require("autodoc.views.panel")
drawer.POLL_MS = 150

local function open()
  local res
  host.open(function(ok, v) res = { ok = ok, v = v } end)
  t.wait(2000, function() return res ~= nil end)
  return res or {}
end

local function find_row(view, pred)
  for _, r in ipairs(view._rows()) do if pred(r) then return r end end
end

autodoc.setup({ bin = bin, config = cfg })
local _, aerr = t.await(20000, function(done) session.call("workspace.add", { "kb", kb }, done) end)
t.ok(aerr == nil, "workspace.add", aerr and aerr.message)
-- indexed before the drawer opens, so the tree is the listing's, not the change feed's
t.ok(t.wait(15000, function()
  local st = t.await(5000, function(done) session.call("index.status", { "kb" }, done) end)
  return st and st.docs == 2
end), "the KB is indexed")

t.section("C2: auto-finder hosts the drawer", function()
  host._reset_for_tests()
  panel._reset_for_tests()
  panel.setup()
  -- kb is not one of auto-finder's default sections: a user asks for it
  require("auto-finder").setup({ sections = { "config", "files", "kb" } })
  local section = require("auto-finder.views.kb")
  t.ok(section._available_for_tests() == true, "auto-finder's kb facade finds AutoDoc")
  -- not registered by hand: a user opens the drawer without visiting the section first
  t.ok(host.has_host("auto-finder"), "auto-finder's setup registered it as a drawer host, unprompted")

  local res = open()
  t.ok(res.ok == true and host.owner() == "auto-finder", "the drawer mounts on auto-finder, not AutoDoc's panel",
    vim.inspect(res) .. " owner=" .. tostring(host.owner()))
  local view = host.view()
  local b = view and view:bufnr()
  t.ok(b ~= nil and vim.api.nvim_buf_is_valid(b), "a view with a live buffer is mounted")
  if not b then return end
  t.eq(vim.bo[b].filetype, "auto-finder", "its filetype is auto-finder's")
  t.eq(vim.b[b].auto_finder_view, "kb", "b:auto_finder_view is kb")
  t.ok(vim.api.nvim_buf_get_name(b):find("auto-finder://kb", 1, true) ~= nil, "the buffer is named auto-finder://kb",
    vim.api.nvim_buf_get_name(b))
  t.eq(panel.window(), nil, "no AutoDoc panel opened")

  t.ok(t.wait(10000, function() return find_row(view, function(r) return r.kind == "workspace" and r.ws == "kb" end) ~= nil end),
    "the drawer lists the KB from the daemon")
  view._dispatch("<CR>", (find_row(view, function(r) return r.kind == "workspace" and r.ws == "kb" end)))
  t.ok(t.wait(10000, function() return find_row(view, function(r) return r.kind == "dir" and r.ws == "kb" and r.rel == "adrs" end) ~= nil end),
    "expanded on auto-finder, it lists the KB's folders")

  vim.cmd("AutodocDrawer")
  t.ok(host.owner() == nil, ":AutodocDrawer toggles the drawer auto-finder hosts: closed")
  vim.cmd("AutodocDrawer")
  t.eq(host.owner(), "auto-finder", "and open again, on auto-finder")
  t.eq(panel.window(), nil, "still with no AutoDoc panel")

  view = host.view()
  section.on_close()
  t.eq(host.owner(), nil, "closing the section releases the drawer")
  t.eq(view and view:bufnr(), nil, "and the view is disposed")
end)

t.section("C4: a runtime host transition", function()
  host._reset_for_tests()
  panel._reset_for_tests()
  panel.setup()
  require("auto-finder").setup({ sections = { "config", "files" } })
  t.ok(not host.has_host("auto-finder"), "without a kb section, auto-finder is not a host")
  local res = open()
  t.ok(res.ok == true and host.owner() == "autodoc", "AutoDoc self-hosts", vim.inspect(res))
  local self_view = host.view()
  t.ok(self_view and self_view:bufnr() ~= nil and panel.window() ~= nil, "in its own panel, with a buffer")

  -- auto-finder gains the kb section through its real slot path
  require("auto-finder")._rebuild_section_registry({ "config", "files", "kb" }, { no_force_open = true })
  t.ok(host.has_host("auto-finder"), "the new section registers auto-finder as a host")
  res = open()
  t.ok(res.ok == true and host.owner() == "auto-finder", "the higher-priority host takes over", vim.inspect(res))
  t.eq(self_view:bufnr(), nil, "the self-hosted view was disposed: never two live views")
  t.ok(t.wait(2000, function() return panel.window() == nil end), "and AutoDoc's panel closed")

  require("auto-finder")._rebuild_section_registry({ "config", "files" }, { no_force_open = true })
  t.ok(not host.has_host("auto-finder"), "removing the section unregisters auto-finder")
  t.ok(host.owner() == nil, "and tears its drawer down")
  res = open()
  t.ok(res.ok == true and host.owner() == "autodoc", "the next open falls back to AutoDoc's panel", vim.inspect(res))
end)

t.finish()
