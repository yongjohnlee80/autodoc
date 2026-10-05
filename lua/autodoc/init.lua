---AutoDoc in Neovim: the KB drawer, search, the preview, the KB tools, over the AutoDoc daemon.
---
---`setup()` connects nothing: the first call that needs the daemon finds it (and, through the
---binary, starts it), so loading the plugin costs nothing.
---
---  require("autodoc").setup({
---    bin = nil,     -- the autodoc binary; default: the plugin's own build, then PATH
---    config = nil,  -- autodoc's config.toml; default: the binary's own default
---  })
---@module 'autodoc'

local M = {}

local _options = {}
local _augroup = nil

---options is what setup was given.
function M.options() return _options end

function M.setup(opts)
  _options = vim.tbl_extend("force", { bin = nil, config = nil }, opts or {})
  local session = require("autodoc.session")
  session.configure(_options)
  require("autodoc.verbs").register()
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
