---autodoc.preview.config — the preview's options, shared by its modules.
---@module 'autodoc.preview.config'

local M = {}

---@class AutodocPreviewConfig
M.DEFAULTS = {
  -- Floats: per-slot width clamps to [min, max] and tracks its content;
  -- all six share one height (a fraction of the screen). The bottom row
  -- (a/s/d) sits cascade_x right and cascade_y down of its top-row pair.
  min_panel_width   = 60,
  max_panel_width   = 120,
  panel_height_frac = 0.85,
  cascade_x_frac    = 0.5,
  cascade_y         = 4,

  -- A visible slot re-renders this long after the last change to its source.
  debounce_ms = 150,

  -- Browser view: the `autodoc` binary, and the export theme (light, dark,
  -- sepia, retro or mono; nil follows 'background'). A nil binary is found as
  -- the daemon's is (autodoc.lifecycle): setup's opts.bin, then the plugin's
  -- own build (lazy `build = "make build"`), PATH, the managed cache.
  binary = nil,
  theme  = nil,

  -- Opens a URL or file with the system opener (`gx`, the browser view).
  -- nil means vim.ui.open.
  opener = nil,

  -- find(): the most files listed, and directories never descended into.
  find_max_files = 5000,
  find_skip      = { ".git", "node_modules", ".bare" },

  -- setup() registers the :AutodocPreview* commands unless false.
  commands = true,
  -- setup() maps the default <leader>m* keys only when true (autovim's
  -- spec defines them otherwise).
  keys = false,
}

---@type AutodocPreviewConfig
M.values = vim.deepcopy(M.DEFAULTS)

---@param opts table?
function M.set(opts)
  M.values = vim.tbl_deep_extend("force", vim.deepcopy(M.DEFAULTS), opts or {})
  return M.values
end

---@return string
function M.theme()
  return M.values.theme or (vim.o.background == "dark" and "dark" or "light")
end

---Open a URL or path with the configured opener.
---@param target string
function M.open(target)
  local fn = M.values.opener
  if type(fn) == "function" then return fn(target) end
  return vim.ui.open(target)
end

return M
