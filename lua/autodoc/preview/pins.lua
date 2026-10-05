---autodoc.preview.pins — per-project slot pins in auto-core state.
---
---A fresh namespace, `autodoc.preview` (persist json): md-harpoon's pins
---are not read (ADR 1791210485 §1). The pins of each project are kept
---under a key derived from the project's root — auto-core's active
---worktree, else the cwd — and `worktree:switched` moves the key to the
---worktree switched to.
---
---On disk: `{ pins = { [<key>] = { [slot] = { path, cursor } } } }`. The
---whole `pins` table is written at once, because a key may hold
---characters a dot-path `:set` would split on.
---@module 'autodoc.preview.pins'

local M = {}

M.NAMESPACE = "autodoc.preview"

local ns, key

local function core() return require("auto-core") end

function M.namespace()
  if not ns then
    ns = core().state.namespace(M.NAMESPACE, { defaults = { pins = {} }, persist = "json" })
  end
  return ns
end

---@param root string?
---@return string
local function key_for(root)
  if type(root) ~= "string" or root == "" then
    local ok, active = pcall(function() return core().git.worktree.get_active() end)
    root = ok and active or nil
  end
  if type(root) ~= "string" or root == "" then root = vim.fn.getcwd() end
  return vim.fn.sha256(vim.fs.normalize(root)):sub(1, 16)
end

---@return string
function M.key()
  key = key or key_for(nil)
  return key
end

---The project changed: pins now read from and write to `root`'s map.
---@param root string?
function M.switch(root)
  key = key_for(root)
end

---@return table<string, { path: string, cursor: integer[]? }>
function M.all()
  local pins = M.namespace():get("pins") or {}
  return pins[M.key()] or {}
end

---@param slot string
---@return { path: string, cursor: integer[]? }?
function M.get(slot)
  return M.all()[slot]
end

---@param slot string
---@param pin { path: string, cursor: integer[]? }?
function M.set(slot, pin)
  local n = M.namespace()
  local pins = vim.deepcopy(n:get("pins") or {})
  local k = M.key()
  pins[k] = pins[k] or {}
  pins[k][slot] = pin
  n:set("pins", pins)
end

---@param slot string
---@param cursor integer[]
function M.set_cursor(slot, cursor)
  local pin = M.get(slot)
  if not pin or vim.deep_equal(pin.cursor, cursor) then return end
  M.set(slot, { path = pin.path, cursor = cursor })
end

---Test hook: forget the cached key and namespace handle.
function M._reset()
  ns, key = nil, nil
end

return M
