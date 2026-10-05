---autodoc.preview.render.inline — the inline pieces the renderer needs
---outside treesitter: where links and images point (for `gx`), the plain
---text of a table cell, and the "image alone on its line" shape.
---
---Treesitter's markdown_inline highlighting does the styling and the
---concealing of emphasis, code spans and link syntax in the float. This
---module only answers "what does the thing under the cursor point at",
---which highlighting cannot tell us, and strips markup where a box-drawn
---table needs the visible width of a cell.
---@module 'autodoc.preview.render.inline'

local M = {}

---A link's destination as written: `<dest> "title"` or `dest "title"`.
---@param raw string
---@return string
function M.destination(raw)
  raw = vim.trim(raw or "")
  if raw:sub(1, 1) == "<" then
    local inner = raw:match("^<([^>]*)>")
    if inner then return inner end
  end
  return raw:match("^(%S+)") or ""
end

---@class AutodocPreviewTarget
---@field s integer      0-based start byte column
---@field e integer      0-based end byte column (exclusive)
---@field target string  destination as written
---@field kind "link"|"image"

local function overlaps(list, s, e)
  for _, t in ipairs(list) do
    if s < t.e and e > t.s then return true end
  end
  return false
end

---Every link and image on `line`, by byte span.
---@param line string
---@param defs table<string, string>? reference definitions, lower-cased label → destination
---@return AutodocPreviewTarget[]
function M.targets(line, defs)
  local out = {}
  if not line:find("[%[<h]") then return out end

  -- Images first: their `[alt](dest)` tail would otherwise read as a link.
  local init = 1
  while true do
    local s, e, _, dest = line:find("!%[([^%]]*)%]%(([^%)]*)%)", init)
    if not s then break end
    out[#out + 1] = { s = s - 1, e = e, target = M.destination(dest), kind = "image" }
    init = e + 1
  end

  init = 1
  while true do
    local s, e, _, dest = line:find("%[([^%]]*)%]%(([^%)]*)%)", init)
    if not s then break end
    if not overlaps(out, s - 1, e) then
      out[#out + 1] = { s = s - 1, e = e, target = M.destination(dest), kind = "link" }
    end
    init = e + 1
  end

  if defs and next(defs) then
    init = 1
    while true do
      local s, e, text, label = line:find("%[([^%]]+)%]%[([^%]]*)%]", init)
      if not s then break end
      local key = (label ~= "" and label or text):lower()
      if defs[key] and not overlaps(out, s - 1, e) then
        out[#out + 1] = { s = s - 1, e = e, target = defs[key], kind = "link" }
      end
      init = e + 1
    end
    init = 1
    while true do
      local s, e, label = line:find("%[([^%]]+)%]", init)
      if not s then break end
      local nextc = line:sub(e + 1, e + 1)
      local key = label:lower()
      if defs[key] and nextc ~= "(" and nextc ~= "[" and nextc ~= ":"
          and not overlaps(out, s - 1, e) then
        out[#out + 1] = { s = s - 1, e = e, target = defs[key], kind = "link" }
      end
      init = e + 1
    end
  end

  init = 1
  while true do
    local s, e, url = line:find("<(%a[%w+.%-]*:[^>%s]+)>", init)
    if not s then break end
    if not overlaps(out, s - 1, e) then
      out[#out + 1] = { s = s - 1, e = e, target = url, kind = "link" }
    end
    init = e + 1
  end

  init = 1
  while true do
    local s, e = line:find("https?://[^%s<>%)%]\"'`]+", init)
    if not s then break end
    local url = line:sub(s, e)
    local trimmed = url:gsub("[%.,;:!%?]+$", "")
    e = s + #trimmed - 1
    if not overlaps(out, s - 1, e) then
      out[#out + 1] = { s = s - 1, e = e, target = trimmed, kind = "link" }
    end
    init = e + 1
  end

  table.sort(out, function(a, b) return a.s < b.s end)
  return out
end

---`alt, path` when the line holds nothing but one image.
---@param line string
---@return string? alt, string? path
function M.image_only(line)
  local alt, dest = line:match("^%s*!%[([^%]]*)%]%(([^%)]*)%)%s*$")
  if not alt then return nil end
  return alt, M.destination(dest)
end

---The visible text of a table cell: links and images become their text,
---emphasis and code-span delimiters go, escapes resolve.
---@param text string
---@return string
function M.plain(text)
  if not text:find("[%[%*_`~\\!]") then return text end
  local s = text
  s = s:gsub("!%[([^%]]*)%]%b()", "%1")
  s = s:gsub("%[([^%]]*)%]%b()", "%1")
  s = s:gsub("%[([^%]]*)%]%[[^%]]*%]", "%1")
  s = s:gsub("`([^`]+)`", "%1")
  s = s:gsub("%*%*(.-)%*%*", "%1")
  s = s:gsub("~~(.-)~~", "%1")
  s = s:gsub("%*([^%*%s][^%*]-)%*", "%1")
  s = (" " .. s .. " ")
    :gsub("(%W)__(.-)__(%W)", "%1%2%3")
    :gsub("(%W)_([^_%s][^_]-)_(%W)", "%1%2%3")
  s = s:sub(2, -2)
  s = s:gsub("\\([%p])", "%1")
  return s
end

---GitHub-style heading anchor: lower-case, punctuation dropped, spaces to `-`.
---@param text string
---@return string
function M.slug(text)
  local s = M.plain(text):lower()
  s = s:gsub("[^%w%s%-_\128-\255]", "")
  s = vim.trim(s):gsub("%s", "-")
  return s
end

return M
