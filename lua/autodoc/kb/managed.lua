---autodoc.kb.managed: this build's managed KB documents, handed to auto-core.
---
---KB_OPERATIONS.md and _schema/frontmatter.yaml belong to AutoDoc, not to the KB (ADR 1791209946
---§3.1). Each load of the plugin gives auto-core this build's copies (`provide`), and auto-core keeps
---the newest of each, persisted, so the copy is at hand even in a session where AutoDoc never loads.
---auto-core is the ONE writer that brings a KB up to them (`auto-core.kb.sync_managed`): the session
---asks it for each listed KB, and auto-agents asks it for the primary KB before every spawn, so an agent
---always starts on the installed AutoDoc's operations document.
---@module 'autodoc.kb.managed'

local scaffold = require("autodoc.kb.scaffold")
local version = require("autodoc.kb.version")

local M = {}

M.PROVIDER = "autodoc"
-- The field each managed file declares its version in (frontmatter, or a leading `#` comment in
-- YAML); auto-core reads it back from a KB's copy to decide whether that copy is older.
M.VERSION_KEY = "autodoc_version"

---core_kb is auto-core.kb when it carries the managed-documents API (auto-core v0.3.1+), else nil.
---@return table|nil
local function core_kb()
  local ok, kb = pcall(require, "auto-core.kb")
  if ok and type(kb) == "table" and type(kb.provide_managed) == "function" and type(kb.sync_managed) == "function" then
    return kb
  end
  return nil
end

---files is this build's managed documents, rendered at this build's AutoDoc version.
---@param opts? {autodoc_version: string?, date: string?}
---@return {rel: string, version: string, text: string}[]
function M.files(opts)
  opts = opts or {}
  local vars = { autodoc_version = opts.autodoc_version or version.autodoc, date = opts.date or os.date("%Y-%m-%d") }
  local out = {}
  for _, f in ipairs(scaffold.files(vars)) do
    if f.managed then out[#out + 1] = { rel = f.rel, version = vars.autodoc_version, text = f.text } end
  end
  return out
end

---provide hands this build's managed documents to auto-core. An auto-core without the API answers
---false with "no_auto_core_kb" (nothing is stored, and nothing refreshes a KB).
---@param opts? {autodoc_version: string?, date: string?}
---@return boolean ok, string|nil err, table|nil report
function M.provide(opts)
  local kb = core_kb()
  if not kb then return false, "no_auto_core_kb", nil end
  return kb.provide_managed(M.PROVIDER, { version_key = M.VERSION_KEY, files = M.files(opts) })
end

---sync asks auto-core to bring root's managed documents up to the stored copies.
---@param root string
---@return boolean ok, string|nil err, table|nil report
function M.sync(root)
  local kb = core_kb()
  if not kb then return false, "no_auto_core_kb", nil end
  return kb.sync_managed(root)
end

return M
