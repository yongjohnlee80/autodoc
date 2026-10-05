---autodoc.preview.browser — the browser view through AutoDoc's Go export.
---
---The text shown is the buffer's CURRENT text, saved or not: it is written
---to a snapshot `<dir>/<name>.md` in a private per-session directory
---(mode 0700), then
---
---  autodoc --export html --base <the buffer's dir, or cwd> --theme <t>
---          --output <dir>/<name>.html <snapshot>
---
---runs, and the page opens with the system opener. `--base` makes the
---page's relative links point at the original files rather than the
---temp directory (ADR 1791210485 §2a); Mermaid is drawn offline by the
---page itself. The directory is removed on VimLeavePre (`cleanup`).
---@module 'autodoc.preview.browser'

local config = require("autodoc.preview.config")

local M = {}

local dir         -- the session directory, created on first use
local names = {}  -- source identity → snapshot name, stable for the session
local taken = {}  -- snapshot name → source identity

local function warn(msg)
  vim.notify("autodoc.preview: " .. msg, vim.log.levels.WARN)
end

---The session directory, created 0700 on first use.
---@return string?
function M.dir()
  if dir and vim.fn.isdirectory(dir) == 1 then return dir end
  local tmp = vim.uv.os_tmpdir() or "/tmp"
  local path, err = vim.uv.fs_mkdtemp(vim.fs.joinpath(tmp, "autodoc-preview-XXXXXX"))
  if not path then
    warn("cannot create a snapshot directory: " .. tostring(err))
    return nil
  end
  vim.uv.fs_chmod(path, tonumber("700", 8))
  dir = path
  return dir
end

---Remove the session directory (VimLeavePre).
function M.cleanup()
  if dir then vim.fn.delete(dir, "rf") end
  dir, names, taken = nil, {}, {}
end

---A snapshot name for a source: its file name without the Markdown
---extension, or `untitled-<bufnr>`; a second source with the same name
---gets a numeric suffix.
---@param ident string
---@param base string
---@return string
local function name_for(ident, base)
  if names[ident] then return names[ident] end
  local name = base:gsub("%.markdown$", ""):gsub("%.md$", ""):gsub("[^%w%._%-]", "_")
  if name == "" then name = "untitled" end
  local candidate, k = name, 2
  while taken[candidate] and taken[candidate] ~= ident do
    candidate = name .. "-" .. k
    k = k + 1
  end
  names[ident], taken[candidate] = candidate, ident
  return candidate
end

---@class AutodocPreviewSource
---@field lines string[]
---@field path string?   absolute path of the source, when it has one
---@field bufnr integer?

---Export `source` and open the page.
---@param source AutodocPreviewSource
---@param opts? { on_done?: fun(ok: boolean, html: string?, argv: string[]?) }
---@return string[]? argv the command run
function M.open(source, opts)
  opts = opts or {}
  local function done(ok, html, argv)
    if opts.on_done then opts.on_done(ok, html, argv) end
  end
  local bin = config.values.binary
  if vim.fn.executable(bin) ~= 1 then
    warn(("`%s` not found on PATH (needed for the browser view)"):format(bin))
    return done(false)
  end
  local d = M.dir()
  if not d then return done(false) end

  local ident, base_name, base
  if source.path then
    ident = source.path
    base_name = vim.fs.basename(source.path)
    base = vim.fs.dirname(source.path)
  else
    ident = "buf:" .. tostring(source.bufnr)
    base_name = "untitled-" .. tostring(source.bufnr)
    base = vim.fn.getcwd()
  end
  local name = name_for(ident, base_name)
  local snapshot = vim.fs.joinpath(d, name .. ".md")
  local html = vim.fs.joinpath(d, name .. ".html")

  local text = table.concat(source.lines, "\n") .. "\n"
  local f, err = io.open(snapshot, "w")
  if not f then
    warn("cannot write the snapshot: " .. tostring(err))
    return done(false)
  end
  f:write(text)
  f:close()

  local argv = {
    bin, "--export", "html",
    "--base", base,
    "--theme", config.theme(),
    "--output", html,
    snapshot,
  }
  vim.system(argv, { text = true }, function(res)
    vim.schedule(function()
      if res.code ~= 0 then
        warn(("export failed (exit %d): %s"):format(res.code, vim.trim(res.stderr or "")))
        return done(false, nil, argv)
      end
      config.open(html)
      done(true, html, argv)
    end)
  end)
  return argv
end

return M
