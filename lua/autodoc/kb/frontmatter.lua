-- autodoc.kb.frontmatter: read a Markdown file's YAML frontmatter at the level the KB tooling
-- needs: its line range, and each top-level key with its line and value (a scalar, a flow list, or
-- a block list). It never rewrites anything; the migration edits lines it locates here.

local M = {}

---The frontmatter's closing line (1-based) when line 1 opens one, else nil.
---@param lines string[]
---@return integer?
function M.range(lines)
  if not lines[1] or not lines[1]:match("^%-%-%-%s*$") then return nil end
  for i = 2, math.min(#lines, 400) do
    if lines[i]:match("^%-%-%-%s*$") or lines[i]:match("^%.%.%.%s*$") then return i end
  end
  return nil
end

local function strip_comment(v)
  -- a ` #` outside quotes starts a comment
  local q
  for i = 1, #v do
    local c = v:sub(i, i)
    if q then
      if c == q then q = nil end
    elseif c == '"' or c == "'" then
      q = c
    elseif c == "#" and (i == 1 or v:sub(i - 1, i - 1):match("%s")) then
      return v:sub(1, i - 1)
    end
  end
  return v
end

local function unquote(v)
  v = vim.trim(v)
  local q = v:sub(1, 1)
  if (q == '"' or q == "'") and v:sub(-1) == q and #v >= 2 then return v:sub(2, -2) end
  return v
end

M.unquote = unquote

---Parse a scalar or flow-list value.
local function value(raw)
  local v = vim.trim(strip_comment(raw))
  if v == "" then return nil end
  if v:sub(1, 1) == "[" and v:sub(-1) == "]" then
    local items = {}
    for item in v:sub(2, -2):gmatch("[^,]+") do
      local s = unquote(item)
      if s ~= "" then items[#items + 1] = s end
    end
    return items
  end
  return unquote(v)
end

---Parse the frontmatter of `lines`.
---@return table? fm  {close = <line>, keys = {name -> {line, value, raw}}, order = {names}}
function M.parse(lines)
  local close = M.range(lines)
  if not close then return nil end
  local fm = { close = close, keys = {}, order = {} }
  local cur
  for i = 2, close - 1 do
    local ln = lines[i]
    local key, rest = ln:match("^([%w_][%w_%-%.]*)%s*:%s?(.*)$")
    if key then
      cur = { line = i, raw = rest, value = value(rest) }
      if not fm.keys[key] then
        fm.keys[key] = cur
        fm.order[#fm.order + 1] = key
      else
        fm.keys[key].duplicate = true
      end
    elseif cur and ln:match("^%s+%-%s") or (cur and ln:match("^%-%s")) then
      local item = unquote(strip_comment(ln:gsub("^%s*%-%s*", "", 1)))
      if type(cur.value) ~= "table" then cur.value = {} end
      cur.value[#cur.value + 1] = item
    end
  end
  return fm
end

---Parse frontmatter from text.
function M.parse_text(text)
  return M.parse(vim.split(text, "\n", { plain = true }))
end

return M
