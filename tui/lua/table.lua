-- :table <cols> <rows> — a Markdown table under the cursor's line: a header of Column1…, the
-- delimiter row (five dashes a column: enough for Markdown, short on the page), and rows of
-- Item<col>.<row> to type over. In Insert mode Tab walks the cells,
-- selecting each one's text so typing replaces it; past the last cell it adds a row.
autodoc.command{
  name = "table",
  args = "<cols> <rows>",
  help = "insert a Markdown table; Tab walks its cells",
  run = function(ed, args)
    local cols, rows = tonumber(args[1] or "3"), tonumber(args[2] or "3")
    if not cols or not rows or cols < 1 or rows < 1 or cols ~= math.floor(cols) or rows ~= math.floor(rows) then
      error("table <cols> <rows>: two whole numbers, 1 or more", 0)
    end
    if cols > 64 or rows > 1000 then
      error("table: at most 64 columns and 1000 rows", 0)
    end
    local function row(cell)
      local cells = {}
      for c = 1, cols do cells[c] = cell(c) end
      return "| " .. table.concat(cells, " | ") .. " |"
    end
    local out = {
      row(function(c) return "Column" .. c end),
      row(function() return "-----" end),
    }
    for r = 1, rows do
      out[#out + 1] = row(function(c) return "Item" .. c .. "." .. r end)
    end
    local text = table.concat(out, "\n")
    local line = ed:cursor()
    if ed:line(line) == "" then
      ed:insert_at(line, 1, text)
    else
      -- under the cursor's line, with a blank line before it: Markdown needs one
      ed:insert_at(line, #ed:line(line) + 1, "\n\n" .. text)
      line = line + 2
    end
    ed:set_cursor(line, 3)
  end,
}
