---autodoc.views.panel — autodoc's own drawer surface, the fallback host (ADR 1791209945 §7; the
---pattern of autodb's panel.lua, ADR 0078 §3.3 / §3.5).
---
---When auto-finder is installed it registers its `kb` section as a host at priority 100 and this
---one never mounts. When autodoc is installed **alone**, on auto-core only, this is what puts the
---drawer on screen, so `:AutodocDrawer` always works.
---
---It is an ordinary consumer of `auto-core.ui.panel` + `auto-core.ui.section`. Two details are
---handled deliberately, as autodb's panel does:
---
---  1. **`panel.new` is idempotent per NAME and merges opts into the existing instance**, so this
---     panel is named `autodoc` and never another plugin's name.
---  2. **`Panel:close()` does not fan section hooks out**; only `Registry:dispose()` does. The
---     panel's `on_close` disposes the section registry, then calls the host registry's
---     `release`, without which the drawer would stay "mounted" and the next open would focus a
---     dead surface.
---@module 'autodoc.views.panel'

local log = require("autodoc.log")

local PANEL_NAME = "autodoc"
local SECTION = 0

local M = {}

local panel = nil            -- the auto-core panel instance
local section_registry = nil -- auto-core's SECTION registry (not the drawer host registry)
local release_mount = nil    -- the release handed to us by the drawer host registry

---teardown is the one idempotent close path: the panel's own on_close (a real `q`, or `:close`),
---and the host registry when this provider loses a handoff.
---@param notify_release boolean false when the host registry already knows
local function teardown(notify_release)
  local reg, rel = section_registry, release_mount
  section_registry, release_mount = nil, nil
  if reg then
    local ok, e = pcall(function() reg:dispose() end)
    if not ok then log.warn("panel: section registry dispose failed: " .. tostring(e)) end
  end
  if notify_release and rel then pcall(rel) end
end

local function ensure_panel()
  if panel then return panel end
  panel = require("auto-core").ui.panel.new({
    name = PANEL_NAME,
    side = "left",
    width = { default = 40, min = 30, max = 100 },
    filetype = "autodoc",
    on_close = function()
      -- host-initiated: the host registry has not been told yet, so release() is ours to call
      teardown(true)
    end,
  })
  return panel
end

local function is_open() return panel ~= nil and panel:_is_open() end

---editor_target_winid picks the window a document opens in, never a panel: the previous window
---when it is an ordinary editor window, else the first one in the tab. Floats, `winfixbuf`
---windows and ANY plugin's panel (auto-core's cross-plugin marker, so this works without
---auto-finder) are skipped, which is what keeps `:edit` from bouncing off a pinned window.
---@return integer|nil
function M.editor_target_winid()
  local function usable(w)
    if not (w and w ~= 0 and vim.api.nvim_win_is_valid(w)) then return false end
    local cfg = vim.api.nvim_win_get_config(w)
    if cfg and cfg.relative ~= nil and cfg.relative ~= "" then return false end
    local ok_fix, fixed = pcall(function() return vim.wo[w].winfixbuf end)
    if ok_fix and fixed then return false end
    local marker = vim.w[w].auto_core_panel_name
    if type(marker) == "string" and marker ~= "" then return false end
    return vim.bo[vim.api.nvim_win_get_buf(w)].buftype == ""
  end
  local prev = vim.fn.win_getid(vim.fn.winnr("#"))
  if usable(prev) then return prev end
  for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    if usable(w) then return w end
  end
  return nil
end

---provider is registered at priority 0, the reserved floor, so any real panel host outranks it.
M.provider = {
  id = "autodoc",
  priority = 0,

  -- auto-core is autodoc's hard dependency: if this runs at all, a panel is available
  available = function() return (pcall(require, "auto-core")) end,

  profile = {
    filetype      = "autodoc",
    buf_var       = "autodoc_view",
    buf_var_value = "drawer",
    buf_name      = "autodoc://drawer",
    editor_target_winid = function() return M.editor_target_winid() end,
  },

  ---mount opens the panel, attaches a one-section registry over the view, and returns the window
  ---the drawer landed in so the host registry can validate it.
  mount = function(view, release)
    local p = ensure_panel()
    local winid = p:open()
    if not winid then return nil end
    section_registry = require("auto-core").ui.section.attach(p, {
      {
        number = SECTION,
        name = "kb",
        get_buffer = function(pn) return view:get_buffer(pn.winid) end,
        on_focus = function(pn, b) return view:on_focus(pn.winid, b) end,
        -- the view's teardown belongs to the host registry (release), so it happens once
        on_close = function() end,
      },
    }, { default = SECTION })
    release_mount = release
    section_registry:focus(SECTION)
    return p.winid
  end,

  focus = function()
    if not (panel and section_registry) then return nil end
    if not is_open() then
      if not panel:open() then return nil end
    end
    section_registry:focus(SECTION)
    return panel.winid
  end,

  ---close is OUR surface teardown during a handoff: the registry disposes the view itself, so this
  ---must not call release() back at it.
  close = function()
    teardown(false)
    if panel and is_open() then pcall(function() panel:close() end) end
  end,
}

---setup registers autodoc's own panel as the fallback drawer host. Cheap: opens and connects
---nothing.
function M.setup()
  local ok, err = require("autodoc.views.host")._register_self(M.provider)
  if not ok then
    log.warn("panel: could not register the autodoc drawer host: " .. tostring(err and err.message))
  end
  return ok
end

---window is the panel's window while it is open (tests).
function M.window() return is_open() and panel.winid or nil end

function M._reset_for_tests()
  teardown(false)
  if panel then pcall(function() panel:dispose() end) end
  panel = nil
end

return M
