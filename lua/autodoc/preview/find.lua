---autodoc.preview.find — pick a Markdown file under the cwd, then a slot.
---
---Any Markdown file, in a knowledge base or not (ADR 1791210485 §1):
---searching the KB itself is `<leader>fk`, elsewhere. The listing walks
---the directory with vim.fs.dir and offers it through vim.ui.select, so a
---picker that replaces vim.ui.select (snacks, telescope-ui-select, fzf-lua)
---gives fuzzy matching without the preview depending on it. Dot
---directories follow auto-core's `files.show_dotfiles` preference.
---@module 'autodoc.preview.find'

local config = require("autodoc.preview.config")
local layout = require("autodoc.preview.layout")

local M = {}

local function show_dotfiles()
  local ok, v = pcall(function() return require("auto-core").files.get_show_dotfiles() end)
  if not ok then return true end
  return v ~= false
end

---Markdown files under `cwd`, sorted, as absolute paths.
---@param cwd string
---@return string[] files, boolean truncated
function M.list(cwd)
  local cfg = config.values
  local skip = {}
  for _, s in ipairs(cfg.find_skip or {}) do skip[s] = true end
  local dots = show_dotfiles()
  local files, truncated = {}, false
  for name, kind in vim.fs.dir(cwd, {
    depth = 64,
    skip = function(d)
      local base = vim.fs.basename(d)
      if skip[base] then return false end
      if not dots and base:sub(1, 1) == "." then return false end
      return true
    end,
  }) do
    if kind == "file" or kind == "link" then
      local base = vim.fs.basename(name)
      if (dots or base:sub(1, 1) ~= ".")
          and (name:match("%.md$") or name:match("%.markdown$")) then
        if #files >= cfg.find_max_files then
          truncated = true
          break
        end
        files[#files + 1] = vim.fs.joinpath(cwd, name)
      end
    end
  end
  table.sort(files)
  return files, truncated
end

---Ask which slot `path` goes into, then render it there.
---@param path string
---@param render fun(slot: string, path: string)
function M.pick_slot(path, render)
  vim.ui.select(layout.SLOTS, {
    prompt = ("Render %s into panel:"):format(vim.fs.basename(path)),
    format_item = function(slot) return layout.LABELS[slot] or slot end,
  }, function(slot)
    if slot then render(slot, path) end
  end)
end

---@param opts? { cwd?: string }
---@param render fun(slot: string, path: string)
function M.find(opts, render)
  opts = opts or {}
  local cwd = vim.fs.normalize(opts.cwd or vim.fn.getcwd())
  local files, truncated = M.list(cwd)
  if #files == 0 then
    vim.notify("autodoc.preview: no Markdown files under " .. cwd, vim.log.levels.WARN)
    return
  end
  if truncated then
    vim.notify(("autodoc.preview: listing the first %d Markdown files under %s")
      :format(#files, cwd), vim.log.levels.INFO)
  end
  local prefix = cwd:gsub("/$", "") .. "/"
  vim.ui.select(files, {
    prompt = "Markdown:",
    format_item = function(p)
      return p:sub(1, #prefix) == prefix and p:sub(#prefix + 1) or p
    end,
  }, function(choice)
    if choice then M.pick_slot(choice, render) end
  end)
end

return M
