---AutoDoc in Neovim: the KB drawer, search, the preview, the KB tools, over the AutoDoc daemon.
---
---`setup()` connects nothing: the first call that needs the daemon finds it (and, through the
---binary, starts it), so loading the plugin costs nothing.
---
---  require("autodoc").setup({
---    bin = nil,     -- the autodoc binary; default: the plugin's own build, then PATH
---    config = nil,  -- autodoc's config.toml; default: the binary's own default
---    keys = false,  -- true maps the default keys below
---  })
---
---Commands: `:AutodocDrawer` (toggle the kb drawer), `:AutodocSearch [query]` (search the
---selected KB), `:AutodocSelect [workspace]` (choose the KB to search).
---
---Default keys (opts.keys = true):
---  <leader>fk   search the selected KB (:AutodocSearch)
---@module 'autodoc'

local M = {}

local _options = {}
local _augroup = nil

---options is what setup was given.
function M.options() return _options end

M.KEYS = {
  { "<leader>fk", function() require("autodoc.picker").open() end, "autodoc: search the selected KB" },
}

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
  vim.api.nvim_create_user_command("AutodocDrawer", function()
    require("autodoc.views.host").toggle(function(ok, v)
      if not ok then vim.notify("autodoc: " .. tostring(v and v.message), vim.log.levels.WARN, { title = "autodoc" }) end
    end)
  end, { desc = "autodoc: toggle the kb drawer" })
  vim.api.nvim_create_user_command("AutodocSearch", function(c)
    require("autodoc.picker").open({ query = c.args ~= "" and c.args or nil })
  end, { nargs = "*", desc = "autodoc: search the selected KB" })
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
  _options = vim.tbl_extend("force", { bin = nil, config = nil, keys = false }, opts or {})
  local session = require("autodoc.session")
  session.configure(_options)
  require("autodoc.verbs").register()
  require("autodoc.views.panel").setup()
  create_commands()
  if _options.keys then
    for _, k in ipairs(M.KEYS) do vim.keymap.set("n", k[1], k[2], { desc = k[3] }) end
  end
  _augroup = vim.api.nvim_create_augroup("autodoc", { clear = true })
  vim.api.nvim_create_autocmd("BufWritePost", {
    group = _augroup,
    desc = "autodoc: index a saved KB file at once",
    callback = function(ev)
      if ev.file and ev.file ~= "" then session.on_write(vim.fn.fnamemodify(ev.file, ":p")) end
    end,
  })
end

return M
