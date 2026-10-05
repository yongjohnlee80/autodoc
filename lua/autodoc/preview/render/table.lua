---autodoc.preview.render.table — GitHub pipe tables drawn with box
---borders and wrapped to a width.
---
---A table's source lines are replaced, in the preview buffer only, by
---box-drawn lines. Columns keep their natural width while the table fits;
---when it doesn't, the widest columns give way first (water-filling) and
---cell text wraps on words inside its column, so the table always fits
---the float instead of running off its right edge.
---@module 'autodoc.preview.render.table'

local inline = require("autodoc.preview.render.inline")

local M = {}

local strwidth = vim.api.nvim_strwidth

---Column alignments when `line` is a delimiter row (`|---|:-:|`), else nil.
---@param line string
---@return ("left"|"center"|"right")[]?
function M.delimiter(line)
  if not line:find("-", 1, true) or not line:find("^%s*|?%s*:?%-") then return nil end
  local cells = M.split(line)
  if #cells == 0 then return nil end
  local aligns = {}
  for i, c in ipairs(cells) do
    local l, dashes, r = c:match("^(:?)(%-+)(:?)$")
    if not dashes then return nil end
    aligns[i] = (l ~= "" and r ~= "") and "center" or (r ~= "" and "right" or "left")
  end
  return aligns
end

---Split a table row into trimmed cells. Pipes escaped with `\` or inside
---a code span don't split.
---@param line string
---@return string[]
function M.split(line)
  local s = vim.trim(line)
  if s:sub(1, 1) == "|" then s = s:sub(2) end
  if s:sub(-1) == "|" and s:sub(-2, -2) ~= "\\" then s = s:sub(1, -2) end
  local cells, buf, in_code = {}, {}, false
  local i, n = 1, #s
  while i <= n do
    local ch = s:sub(i, i)
    if ch == "\\" and s:sub(i + 1, i + 1) == "|" then
      buf[#buf + 1] = "\\|"
      i = i + 2
    else
      if ch == "`" then in_code = not in_code end
      if ch == "|" and not in_code then
        cells[#cells + 1] = vim.trim(table.concat(buf))
        buf = {}
      else
        buf[#buf + 1] = ch
      end
      i = i + 1
    end
  end
  cells[#cells + 1] = vim.trim(table.concat(buf))
  return cells
end

---Wrap `text` into lines no wider than `w` display cells: on spaces, and
---hard-splitting a word longer than the column.
---@param text string
---@param w integer
---@return string[]
local function wrap(text, w)
  if strwidth(text) <= w then return { text } end
  local out, cur, curw = {}, "", 0
  for word in text:gmatch("%S+") do
    local ww = strwidth(word)
    if ww > w then
      if cur ~= "" then out[#out + 1] = cur; cur, curw = "", 0 end
      local nchars = vim.fn.strchars(word)
      local piece, pw = "", 0
      for ci = 0, nchars - 1 do
        local ch = vim.fn.strcharpart(word, ci, 1)
        local cw = strwidth(ch)
        if pw + cw > w and piece ~= "" then
          out[#out + 1] = piece
          piece, pw = "", 0
        end
        piece, pw = piece .. ch, pw + cw
      end
      cur, curw = piece, pw
    elseif cur == "" then
      cur, curw = word, ww
    elseif curw + 1 + ww <= w then
      cur, curw = cur .. " " .. word, curw + 1 + ww
    else
      out[#out + 1] = cur
      cur, curw = word, ww
    end
  end
  if cur ~= "" or #out == 0 then out[#out + 1] = cur end
  return out
end
M._wrap = wrap

---Column widths: natural widths when they fit `avail`, else the narrow
---columns keep theirs and the rest share what is left equally.
---@param natural integer[]
---@param avail integer
---@return integer[]
local function fit(natural, avail)
  local total = 0
  for _, w in ipairs(natural) do total = total + w end
  if total <= avail then return vim.deepcopy(natural) end
  local n = #natural
  local widths, fixed = {}, {}
  local remaining, open = avail, n
  local changed = true
  while changed and open > 0 do
    changed = false
    local share = math.floor(remaining / open)
    for i = 1, n do
      if not fixed[i] and natural[i] <= share then
        widths[i], fixed[i] = natural[i], true
        remaining, open = remaining - natural[i], open - 1
        changed = true
      end
    end
  end
  if open > 0 then
    local share = math.max(3, math.floor(remaining / open))
    local extra = math.max(0, remaining - share * open)
    for i = 1, n do
      if not fixed[i] then
        widths[i] = share + (extra > 0 and 1 or 0)
        if extra > 0 then extra = extra - 1 end
      end
    end
  end
  return widths
end

local function pad(text, w, align)
  local gap = w - strwidth(text)
  if gap <= 0 then return text end
  if align == "right" then return string.rep(" ", gap) .. text end
  if align == "center" then
    local l = math.floor(gap / 2)
    return string.rep(" ", l) .. text .. string.rep(" ", gap - l)
  end
  return text .. string.rep(" ", gap)
end

---@class AutodocPreviewTableOut
---@field lines string[]
---@field marks { [1]: integer, [2]: integer, [3]: table }[]  row offset, col, extmark opts
---@field targets table<integer, AutodocPreviewTarget[]>      row offset → targets

---Draw a table. `rows[1]` is the header row.
---@param rows string[][]
---@param aligns string[]
---@param width integer  display cells the table may use
---@return AutodocPreviewTableOut
function M.render(rows, aligns, width)
  local ncols = #aligns
  for _, r in ipairs(rows) do ncols = math.max(ncols, #r) end

  local texts, links, natural = {}, {}, {}
  for c = 1, ncols do natural[c] = 3 end
  for ri, r in ipairs(rows) do
    texts[ri], links[ri] = {}, {}
    for c = 1, ncols do
      local raw = r[c] or ""
      local t = inline.plain(raw)
      texts[ri][c] = t
      local tg = inline.targets(raw)
      links[ri][c] = tg[1]
      natural[c] = math.max(natural[c], strwidth(t))
    end
  end

  -- Each column costs its width + 2 padding + 1 border, plus the last border.
  local avail = width - (3 * ncols + 1)
  local widths = fit(natural, math.max(avail, ncols))

  local out = { lines = {}, marks = {}, targets = {} }
  local B = "AutodocPreviewTableBorder"

  local function rule(l, m, r)
    local parts = {}
    for c = 1, ncols do parts[c] = string.rep("─", widths[c] + 2) end
    local line = l .. table.concat(parts, m) .. r
    local row = #out.lines
    out.lines[row + 1] = line
    out.marks[#out.marks + 1] = { row, 0, { end_col = #line, hl_group = B } }
  end

  local function emit_row(ri, head)
    local wrapped, height = {}, 1
    for c = 1, ncols do
      wrapped[c] = wrap(texts[ri][c], widths[c])
      height = math.max(height, #wrapped[c])
    end
    for k = 1, height do
      local row = #out.lines
      local parts, col = { "│" }, #"│"
      out.marks[#out.marks + 1] = { row, 0, { end_col = col, hl_group = B } }
      for c = 1, ncols do
        local text = pad(wrapped[c][k] or "", widths[c], aligns[c] or "left")
        local cs = col + 1
        parts[#parts + 1] = " " .. text .. " │"
        local ce = cs + #text
        if head and text:find("%S") then
          out.marks[#out.marks + 1] = { row, cs, { end_col = ce, hl_group = "AutodocPreviewTableHead" } }
        end
        local tg = links[ri][c]
        if tg and (wrapped[c][k] or "") ~= "" then
          out.targets[row] = out.targets[row] or {}
          table.insert(out.targets[row], { s = cs, e = ce, target = tg.target, kind = tg.kind })
        end
        col = ce + 1
        out.marks[#out.marks + 1] = { row, col, { end_col = col + #"│", hl_group = B } }
        col = col + #"│"
      end
      out.lines[row + 1] = table.concat(parts)
    end
    return height
  end

  -- Body rows get separators only when one of them wraps, so a tall row
  -- can't be read as two.
  local any_wrap = false
  for ri = 2, #rows do
    for c = 1, ncols do
      if strwidth(texts[ri][c]) > widths[c] then any_wrap = true end
    end
  end

  rule("┌", "┬", "┐")
  emit_row(1, true)
  if #rows > 1 then rule("├", "┼", "┤") end
  for ri = 2, #rows do
    emit_row(ri, false)
    if any_wrap and ri < #rows then rule("├", "┼", "┤") end
  end
  rule("└", "┴", "┘")
  return out
end

return M
