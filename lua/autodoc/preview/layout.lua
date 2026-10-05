---autodoc.preview.layout — where each of the six floats sits.
---
---The cascade md-harpoon users know: 1 2 3 across the top, a s d a few
---rows lower and shifted right of their top-row pair. Panel 1 is pinned
---to the left margin and panel d flush to the right one, the rest derive
---from those two, so both screen corners always show; on a narrow screen
---the layout width shrinks toward min_panel_width and then the cascade
---collapses (overlap is acceptable, off-screen is not).
---@module 'autodoc.preview.layout'

local config = require("autodoc.preview.config")

local M = {}

M.SLOTS = { "1", "2", "3", "a", "s", "d" }

M.LABELS = {
  ["1"] = "upper left (1)",
  ["2"] = "upper middle (2)",
  ["3"] = "upper right (3)",
  a     = "left (a)",
  s     = "middle (s)",
  d     = "right (d)",
}

local TOP = { ["1"] = true, ["2"] = true, ["3"] = true }

---@param slot any
---@return boolean
function M.valid(slot)
  return type(slot) == "string" and M.LABELS[slot] ~= nil
end

---The column of each slot.
---@return table<string, integer>
function M.columns()
  local cfg = config.values
  local cols = vim.o.columns
  local margin = 1
  local available = cols - 2 * margin
  local W = cfg.max_panel_width

  local function cascade(w)
    local slack = available - w
    if slack <= 0 then return 0 end
    return math.floor(slack * cfg.cascade_x_frac / 2)
  end

  local cx = cascade(W)
  while 2 * W + cx > available and W > cfg.min_panel_width do
    W = W - 1
    cx = cascade(W)
  end
  if 2 * W + cx > available then cx = 0 end

  local x1 = margin
  local xd = math.max(margin, cols - W - margin)
  local x3 = math.max(margin, xd - cx)
  local x2 = math.floor((x1 + x3) / 2)
  return { ["1"] = x1, ["2"] = x2, ["3"] = x3, a = x1 + cx, s = x2 + cx, d = xd }
end

---The widest a float may be on this screen.
---@return integer
function M.max_width()
  return math.max(20, math.min(config.values.max_panel_width, vim.o.columns - 4))
end

---row, col, width, height of `slot` for content this tall and wide.
---@param slot string
---@param content_lines integer
---@param content_width integer
---@return integer row, integer col, integer width, integer height
function M.geometry(slot, content_lines, content_width)
  local cfg = config.values
  local width = math.max(math.min(cfg.min_panel_width, M.max_width()),
    math.min(M.max_width(), content_width + 4))
  local height = math.max(1, math.min(content_lines, math.floor(vim.o.lines * cfg.panel_height_frac)))
  local row = TOP[slot] and 1 or (1 + cfg.cascade_y)
  return row, M.columns()[slot], width, height
end

return M
