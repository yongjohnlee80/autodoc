-- setup's user commands and keys: :AutodocDrawer toggles the drawer in autodoc's own panel,
-- :AutodocSearch opens the picker, :AutodocSelect chooses the KB to search; the <leader>m group
-- (and <leader>fk) is mapped only when opts.keys is true; the preview is set up with them, its keys
-- following opts.keys.
local t = require("helpers")

local bin = assert(os.getenv("AUTODOC_TEST_BIN"), "AUTODOC_TEST_BIN")
local kb = t.tmp("kb")
vim.fn.mkdir(kb, "p")
vim.fn.writefile({ "# Birds", "", "kestrel" }, kb .. "/birds.md")
local cfg = t.tmp("config.toml")
vim.fn.writefile({
  "[server]",
  string.format("socket = %q", t.tmp("run", "autodoc.sock")),
  string.format("state_dir = %q", t.tmp("state", "autodoc")),
  string.format("data_dir = %q", t.tmp("data", "autodoc")),
}, cfg)
vim.fn.mkdir(t.tmp("project"), "p")
vim.cmd("cd " .. vim.fn.fnameescape(t.tmp("project")))
vim.g.mapleader = " "

local autodoc = require("autodoc")
local session = require("autodoc.session")
local host = require("autodoc.views.host")

t.section("setup without keys", function()
  local kbm = package.loaded["auto-core.kb"] or select(2, pcall(require, "auto-core.kb"))
  if type(kbm) == "table" and kbm._reset_for_tests then kbm._reset_for_tests() end
  autodoc.setup({ bin = bin, config = cfg })
  local provided = type(kbm) == "table" and kbm.managed and kbm.managed() or {}
  t.ok(provided["KB_OPERATIONS.md"] and provided["KB_OPERATIONS.md"].version == require("autodoc.kb.version").autodoc,
    "setup hands this build's managed KB documents to auto-core", vim.inspect(vim.tbl_keys(provided)))
  for _, c in ipairs({ "AutodocDrawer", "AutodocSearch", "AutodocSelect", "AutodocKbMigrate", "AutodocFiles",
    "AutodocRecent", "AutodocBacklinks", "AutodocMaintenance", "AutodocLinkKb" }) do
    t.ok(vim.fn.exists(":" .. c) == 2, ":" .. c .. " exists")
  end
  t.eq(package.loaded["autodoc.kb.migrate"], nil, "the migration's module is not loaded by setup")
  t.eq(vim.fn.getcompletion("AutodocKbMigrate --ap", "cmdline"), { "--apply" }, ":AutodocKbMigrate completes its flags")
  t.eq(vim.fn.maparg("<leader>fk", "n"), "", "no <leader>fk unless opts.keys")
  t.ok(vim.fn.exists(":AutodocPreviewFind") == 2,
    "the preview's commands are set up too")
  t.eq(vim.fn.maparg("<leader>mp", "n"), "", "and its keys are not mapped")
  t.eq(vim.fn.maparg("<leader>mf", "n"), "", "nor the KB group's")
  t.eq(vim.fn.getcompletion("AutodocMaintenance re", "cmdline"), { "restart" }, ":AutodocMaintenance completes its actions")
end)

t.section("setup with the preview off", function()
  autodoc.setup({ bin = bin, config = cfg, keys = true, preview = false })
  t.eq(vim.fn.maparg("<leader>mp", "n"), "", "preview = false maps none of its keys")
  t.ok(vim.fn.exists(":AutodocPreviewFind") ~= 2,
    "and takes its commands down")
  t.ok(vim.fn.maparg("<leader>fk", "n") ~= "", "while the rest of setup stands")
end)

t.section("setup with the preview's own keys", function()
  autodoc.setup({ bin = bin, config = cfg, keys = true, preview = { keys = false } })
  t.eq(vim.fn.maparg("<leader>mp", "n"), "", "preview.keys = false wins over opts.keys")
end)

t.section("setup with keys", function()
  autodoc.setup({ bin = bin, config = cfg, keys = true })
  local want = {
    ["<leader>mf"] = "autodoc: search the selected KB",
    ["<leader>fk"] = "autodoc: search the selected KB",
    ["<leader>mF"] = "autodoc: find a KB document by name",
    ["<leader>mr"] = "autodoc: recent KB files",
    ["<leader>ml"] = "autodoc: what links to this file",
    ["<leader>mk"] = "autodoc: the kb drawer",
    ["<leader>mw"] = "autodoc: choose the KB to search",
    ["<leader>mX"] = "autodoc: maintenance",
  }
  for lhs, desc in pairs(want) do
    local m = vim.fn.maparg(lhs, "n", false, true)
    t.eq(m.desc, desc, lhs .. " is the KB group's")
  end
  local pm = vim.fn.maparg("<leader>mp", "n", false, true)
  t.ok(pm.desc ~= nil and pm.desc:find("autodoc preview", 1, true) ~= nil, "the preview's find is <leader>mp, following opts.keys", vim.inspect(pm))
  local slot = vim.fn.maparg("<leader>m1", "n", false, true)
  t.ok(slot.desc ~= nil and slot.desc:find("autodoc preview", 1, true) ~= nil, "and its slots stay on <leader>m1…", vim.inspect(slot))
end)

t.section(":AutodocSelect", function()
  local _, err = t.await(20000, function(done) session.call("workspace.add", { "kb", kb }, done) end)
  t.ok(err == nil, "workspace.add", err and err.message)
  vim.cmd("AutodocSelect kb")
  t.eq(session.selected(), "kb", ":AutodocSelect kb selects it")
  t.await(5000, function(done) session.workspaces(done) end)
  t.eq(vim.fn.getcompletion("AutodocSelect k", "cmdline"), { "kb" }, "it completes workspace names")
end)

t.section(":AutodocDrawer", function()
  vim.cmd("AutodocDrawer")
  t.eq(host.owner(), "autodoc", ":AutodocDrawer opens the drawer in autodoc's own panel")
  vim.cmd("AutodocDrawer")
  t.eq(host.owner(), nil, "and again closes it")
end)

t.section(":AutodocSearch", function()
  local asked
  vim.ui.input = function(opts, cb) asked = opts; cb(nil) end
  vim.cmd("AutodocSearch kestrel")
  t.ok(asked ~= nil and asked.default == "kestrel", ":AutodocSearch opens the picker with its query (the fallback here)", vim.inspect(asked))
end)

t.finish()
