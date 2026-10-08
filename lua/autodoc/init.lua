---AutoDoc in Neovim: the KB drawer, search, the preview, the KB tools, over the AutoDoc daemon.
---
---`setup()` connects nothing: the first call that needs the daemon finds it (and, through the
---binary, starts it), so loading the plugin costs nothing.
---
---  require("autodoc").setup({
---    bin = nil,     -- the autodoc binary; default: the plugin's own build, then PATH
---    config = nil,  -- autodoc's config.toml; default: the binary's own default
---    keys = false,  -- true maps the default keys below, and the preview's
---    preview = {},  -- the preview's options (autodoc.preview.config); false leaves it off
---    offer_restart = true, -- offer to restart a daemon older than this plugin (once per daemon)
---    offer_link = true, -- offer a workspace for a primary KB auto-core knows by its root alone
---    popup = nil,   -- the right-click menu's "Go to related…" and "Back"; nil follows keys
---  })
---
---Commands: `:AutodocDrawer` (toggle the kb drawer), `:AutodocSearch [query]` (search the
---selected KB), `:AutodocFiles` (a document by name), `:AutodocRecent` (the files opened last,
---shared with the TUI), `:AutodocRelations` (what this file is connected to, by kind),
---`:AutodocBack` (the document opened before), `:AutodocBacklinks` (what links to it), `:AutodocSelect
---[workspace]` (choose the KB to search), `:AutodocMaintenance [restart|install|versions]`, and
---`:AutodocKbMigrate` (move a KB to the v2 layout: a dry run, then --apply / --undo / --forget), and
---`:AutodocLinkKb` (link the project's primary KB to an AutoDoc workspace, adding one if none serves it).
---
---Default keys (opts.keys = true), the knowledge base's `<leader>m` group:
---  <leader>mf   search the selected KB: lexical + semantic + rerank (:AutodocSearch)
---  <leader>mF   find a document of the selected KB by name (:AutodocFiles)
---  <leader>mr   recent files, newest first, across KBs (:AutodocRecent)
---  <leader>ml   what this file is connected to, by kind: relations, links, backlinks (:AutodocRelations)
---  <leader>mo   back to the document opened before (:AutodocBack)
---  <leader>mk   the kb drawer (:AutodocDrawer)
---  <leader>mw   choose the KB to search (:AutodocSelect)
---  <leader>mX   maintenance: restart the daemon, install the binary, versions
---  <leader>fk   search, as <leader>mf
---  and the preview's <leader>m* (autodoc.preview.commands), unless opts.preview.keys says otherwise
---@module 'autodoc'

local M = {}

local _options = {}
local _augroup = nil

---options is what setup was given.
function M.options() return _options end

local function search() require("autodoc.picker").open() end
local function toggle_drawer()
  require("autodoc.views.host").toggle(function(ok, v)
    if not ok then vim.notify("autodoc: " .. tostring(v and v.message), vim.log.levels.WARN, { title = "autodoc" }) end
  end)
end

M.KEYS = {
  { "<leader>mf", search, "autodoc: search the selected KB" },
  { "<leader>mF", function() require("autodoc.finders").files() end, "autodoc: find a KB document by name" },
  { "<leader>mr", function() require("autodoc.finders").recent() end, "autodoc: recent KB files" },
  { "<leader>ml", function() require("autodoc.relations").pick() end, "autodoc: what this file is connected to" },
  { "<leader>mo", function() require("autodoc.relations").back() end, "autodoc: back to the document opened before" },
  { "<leader>mk", toggle_drawer, "autodoc: the kb drawer" },
  { "<leader>mw", function() vim.cmd("AutodocSelect") end, "autodoc: choose the KB to search" },
  { "<leader>mX", function() require("autodoc.maintenance").run() end, "autodoc: maintenance" },
  { "<leader>fk", search, "autodoc: search the selected KB" },
}

---setup_preview sets the preview up with opts.preview, its keys following opts.keys unless it
---names its own; false leaves it off (and takes down one set up before).
local function setup_preview(opts)
  local preview = require("autodoc.preview")
  if opts.preview == false then return preview.teardown() end
  local popts = vim.tbl_extend("keep", type(opts.preview) == "table" and opts.preview or {}, { keys = opts.keys == true })
  preview.setup(popts)
end

---select_command is :AutodocSelect: the named workspace, or a choice among them.
local function select_command(name)
  local session = require("autodoc.session")
  if name and name ~= "" then
    session.select(name)
    return vim.notify("autodoc: searching " .. name, vim.log.levels.INFO, { title = "autodoc" })
  end
  session.workspaces(function(list, err)
    if err then return vim.notify(err.message, vim.log.levels.WARN, { title = "autodoc" }) end
    local names = vim.tbl_map(function(w) return w.name end, list or {})
    vim.ui.select(names, { prompt = "Search which KB?" }, function(choice)
      if choice then session.select(choice) end
    end)
  end)
end

local function create_commands()
  vim.api.nvim_create_user_command("AutodocDrawer", toggle_drawer, { desc = "autodoc: toggle the kb drawer" })
  vim.api.nvim_create_user_command("AutodocSearch", function(c)
    require("autodoc.picker").open({ query = c.args ~= "" and c.args or nil })
  end, { nargs = "*", desc = "autodoc: search the selected KB" })
  local function finders() return require("autodoc.finders") end
  vim.api.nvim_create_user_command("AutodocFiles", function() finders().files() end,
    { desc = "autodoc: find a document of the selected KB by name" })
  vim.api.nvim_create_user_command("AutodocRecent", function() finders().recent() end,
    { desc = "autodoc: the KB files opened last, shared with the TUI" })
  vim.api.nvim_create_user_command("AutodocBacklinks", function() finders().backlinks() end,
    { desc = "autodoc: the documents linking to this file" })
  vim.api.nvim_create_user_command("AutodocRelations", function() require("autodoc.relations").pick() end,
    { desc = "autodoc: what this file is connected to, by kind" })
  vim.api.nvim_create_user_command("AutodocBack", function() require("autodoc.relations").back() end,
    { desc = "autodoc: back to the document opened before" })
  vim.api.nvim_create_user_command("AutodocMaintenance", function(c) require("autodoc.maintenance").run(c.args) end, {
    nargs = "?",
    complete = function(lead)
      return vim.tbl_filter(function(a) return vim.startswith(a, lead) end, require("autodoc.maintenance").ACTIONS)
    end,
    desc = "autodoc: restart the daemon, install the binary again, or show the versions",
  })
  -- the migration's module is loaded when it runs, not at setup
  local function migrate() return require("autodoc.kb.migrate") end
  vim.api.nvim_create_user_command("AutodocKbMigrate", function(c) migrate().command(c.fargs) end, {
    nargs = "*",
    complete = function(lead) return migrate().complete(lead) end,
    desc = "autodoc: migrate a KB to the v2 layout (dry run, --apply, --undo, --forget)",
  })
  vim.api.nvim_create_user_command("AutodocLinkKb", function() require("autodoc.kb.link").run() end,
    { desc = "autodoc: link the project's primary KB to an AutoDoc workspace, adding one if none serves it" })
  vim.api.nvim_create_user_command("AutodocSelect", function(c) select_command(c.args) end, {
    nargs = "?",
    desc = "autodoc: choose the KB to search",
    complete = function(lead)
      local out = {}
      for _, w in ipairs(require("autodoc.session").cached_workspaces()) do
        if vim.startswith(w, lead) then out[#out + 1] = w end
      end
      return out
    end,
  })
end

function M.setup(opts)
  _options = vim.tbl_extend("force", { bin = nil, config = nil, keys = false, preview = {}, offer_restart = true, offer_link = true }, opts or {})
  local popup = _options.popup
  if popup == nil then popup = _options.keys == true end
  local session = require("autodoc.session")
  session.configure(_options)
  require("autodoc.verbs").register()
  -- this build's managed KB documents go to auto-core on every load; it keeps the newest and brings
  -- KBs up to them (the session for listed KBs, auto-agents for the primary before each spawn)
  local pok, ok, err = pcall(require("autodoc.kb.managed").provide)
  if not pok or not ok then
    require("autodoc.log").warn("providing the managed KB documents to auto-core failed: " .. tostring(pok and err or ok))
  end
  require("autodoc.views.panel").setup()
  create_commands()
  setup_preview(_options)
  if _options.keys then
    for _, k in ipairs(M.KEYS) do vim.keymap.set("n", k[1], k[2], { desc = k[3] }) end
  end
  if popup then require("autodoc.relations").popup() end
  _augroup = vim.api.nvim_create_augroup("autodoc", { clear = true })
  vim.api.nvim_create_autocmd("BufWritePost", {
    group = _augroup,
    desc = "autodoc: index a saved KB file at once",
    callback = function(ev)
      if ev.file and ev.file ~= "" then session.on_write(vim.fn.fnamemodify(ev.file, ":p")) end
    end,
  })
  vim.api.nvim_create_autocmd("BufReadPost", {
    group = _augroup,
    desc = "autodoc: a KB file opened is first among the recent files (shared with the TUI)",
    callback = function(ev)
      if ev.file and ev.file ~= "" then require("autodoc.finders").on_open(vim.fn.fnamemodify(ev.file, ":p")) end
    end,
  })
end

return M
