---autodoc.preview.render — AutoDoc's own trimmed Markdown renderer for
---the preview floats (ADR 1791210485 §2).
---
---Two steps:
---
---  build(lines, opts) → content   pure: source lines in, a description out
---  apply(buf, ns, content)        writes it into the PREVIEW buffer
---
---The source buffer is never touched: `build` reads a copy of its lines,
---and `apply` writes only to the float's own scratch buffer. That buffer
---holds the source text line for line, except where a line is drawn
---differently: a table becomes box-drawn lines, the frontmatter folds to
---one header line, and an image alone on its line becomes a placeholder.
---Everything else is decoration — extmarks with conceal, virtual text,
---signs and highlight — on top of treesitter's markdown highlighting
---(headings, emphasis, inline code, link concealing, and the injected
---language highlighting of fenced code), which the caller starts on the
---scratch buffer.
---
---What it draws: headings, lists and task boxes, block quotes and GitHub
---callouts (`> [!NOTE]` …, sign + colour), fenced code (a background and
---a language label), tables, horizontal rules, the frontmatter header
---(type · status · updated), links (text shown, destination recorded for
---`gx`), Mermaid blocks (source kept, with a hint that `B` opens them
---drawn in the browser) and images (a placeholder with alt text and
---path, `gx` opens the file). Out: inline images / Kitty graphics, OSC 8.
---@module 'autodoc.preview.render'

local inline = require("autodoc.preview.render.inline")
local tables = require("autodoc.preview.render.table")

local M = {}

M.highlights = require("autodoc.preview.render.highlights")

M.MERMAID_HINT = "mermaid · B: open drawn in browser"

M.CALLOUTS = {
  NOTE      = { icon = "ℹ", title = "Note",      hl = "AutodocPreviewNote" },
  TIP       = { icon = "✦", title = "Tip",       hl = "AutodocPreviewTip" },
  IMPORTANT = { icon = "❢", title = "Important", hl = "AutodocPreviewImportant" },
  WARNING   = { icon = "⚠", title = "Warning",   hl = "AutodocPreviewWarning" },
  CAUTION   = { icon = "✖", title = "Caution",   hl = "AutodocPreviewCaution" },
}

local BULLETS = { "•", "◦", "▪" }
local HEADING_ICONS = { "◆ ", "◇ ", "▸ ", "▹ ", "• ", "· " }
local FRONTMATTER_KEYS = { "type", "status", "updated" }

local strwidth = vim.api.nvim_strwidth

---@class AutodocPreviewContent
---@field lines string[]                                   preview buffer text
---@field marks { [1]: integer, [2]: integer, [3]: table }[] row, col, extmark opts (0-based)
---@field targets table<integer, AutodocPreviewTarget[]>   row → link/image spans
---@field headings { row: integer, level: integer, text: string, slug: string }[]
---@field mermaid integer[]                                rows carrying the Mermaid hint
---@field src integer[]                                    preview row (1-based) → source line (1-based)
---@field width integer

local function frontmatter_value(line)
  local key, value = line:match("^([%w_%-]+)%s*:%s*(.-)%s*$")
  if not key then return nil end
  value = value:gsub("%s+#.*$", "")
  value = value:gsub("^[\"'](.*)[\"']$", "%1")
  return key:lower(), value
end

---@param lines string[]
---@param opts? { width?: integer }
---@return AutodocPreviewContent
function M.build(lines, opts)
  opts = opts or {}
  local width = math.max(20, opts.width or 80)
  local c = { lines = {}, marks = {}, targets = {}, headings = {}, mermaid = {}, src = {}, width = width }
  local out, marks, src = c.lines, c.marks, c.src
  local n = #lines

  local function emit(text, si)
    out[#out + 1] = text
    src[#out] = si
    return #out - 1
  end
  local function mark(row, col, o)
    marks[#marks + 1] = { row, col, o }
  end
  local function add_targets(row, text, defs)
    local tg = inline.targets(text, defs)
    if #tg > 0 then
      c.targets[row] = tg
      for _, t in ipairs(tg) do
        if t.kind == "image" then
          mark(row, 0, { virt_text = { { "  ▣ " .. t.target, "AutodocPreviewImage" } }, virt_text_pos = "eol" })
        end
      end
    end
  end

  -- Reference definitions (`[label]: dest`), outside fenced code.
  local defs, in_fence = {}, nil
  for i = 1, n do
    local l = lines[i]
    local fence = l:match("^%s*(```+)") or l:match("^%s*(~~~+)")
    if fence then
      if not in_fence then in_fence = fence
      elseif fence:sub(1, 1) == in_fence:sub(1, 1) and #fence >= #in_fence
          and l:match("^%s*[`~]+%s*$") then in_fence = nil end
    elseif not in_fence then
      local label, dest = l:match("^ ? ? ?%[([^%]]+)%]:%s*(%S+)")
      if label then defs[label:lower()] = inline.destination(dest) end
    end
  end

  local i = 1

  -- Frontmatter → one header line.
  if n > 0 and lines[1]:match("^%-%-%-%s*$") then
    local close
    for j = 2, n do
      if lines[j]:match("^%-%-%-%s*$") or lines[j]:match("^%.%.%.%s*$") then close = j; break end
    end
    if close then
      local fields = {}
      for j = 2, close - 1 do
        local k, v = frontmatter_value(lines[j])
        if k and fields[k] == nil and v ~= "" then fields[k] = v end
      end
      local parts = {}
      for _, k in ipairs(FRONTMATTER_KEYS) do
        if fields[k] then parts[#parts + 1] = (k == "updated") and ("updated " .. fields[k]) or fields[k] end
      end
      local text = "≡ " .. (#parts > 0 and table.concat(parts, " · ") or "frontmatter")
      local row = emit(text, 1)
      mark(row, 0, { end_col = #text, hl_group = "AutodocPreviewFrontmatter" })
      mark(row, 0, {
        virt_text = { { ("  (%d lines of frontmatter)"):format(close), "AutodocPreviewCodeLabel" } },
        virt_text_pos = "eol",
      })
      i = close + 1
    end
  end

  local function is_block_start(l)
    return l:match("^%s*[%-%*%+]%s") or l:match("^%s*%d+[%.%)]%s") or l:match("^%s*>")
      or l:match("^%s*#") or l:match("^%s*```") or l:match("^%s*~~~")
  end

  while i <= n do
    local line = lines[i]
    local blank = line:match("^%s*$") ~= nil

    -- ── fenced code ───────────────────────────────────────────
    local _, fence, info = line:match("^(%s*)(```+)([^`]*)$")
    if not fence then _, fence, info = line:match("^(%s*)(~~~+)(.*)$") end
    if fence then
      local lang = vim.trim(info or ""):match("^([%w_%+%-%.#]+)") or ""
      local open_row = emit(line, i)
      mark(open_row, 0, { line_hl_group = "AutodocPreviewCode" })
      local first_content
      i = i + 1
      while i <= n do
        local l = lines[i]
        local closing = l:match("^%s*([`~]+)%s*$")
        if closing and closing:sub(1, 1) == fence:sub(1, 1) and #closing >= #fence
            and not closing:find(fence:sub(1, 1) == "`" and "~" or "`", 1, true) then
          local r = emit(l, i)
          mark(r, 0, { line_hl_group = "AutodocPreviewCode" })
          i = i + 1
          break
        end
        local r = emit(l, i)
        first_content = first_content or r
        mark(r, 0, { line_hl_group = "AutodocPreviewCode" })
        i = i + 1
      end
      local label_row = first_content or open_row
      if lang:lower() == "mermaid" then
        c.mermaid[#c.mermaid + 1] = label_row
        mark(label_row, 0, {
          virt_text = { { M.MERMAID_HINT, "AutodocPreviewHint" } },
          virt_text_pos = "right_align",
        })
      elseif lang ~= "" then
        mark(label_row, 0, {
          virt_text = { { lang, "AutodocPreviewCodeLabel" } },
          virt_text_pos = "right_align",
        })
      end
      goto continue
    end

    -- ── table ─────────────────────────────────────────────────
    if line:find("|", 1, true) and i < n then
      local aligns = tables.delimiter(lines[i + 1])
      if aligns then
        local rows = { tables.split(line) }
        local j = i + 2
        while j <= n and lines[j]:find("|", 1, true) and not lines[j]:match("^%s*$") do
          rows[#rows + 1] = tables.split(lines[j])
          j = j + 1
        end
        local drawn = tables.render(rows, aligns, width)
        local base = #out
        for k, l in ipairs(drawn.lines) do
          -- Map each drawn line to the source row it came from (header for the
          -- top rule, then roughly in step).
          emit(l, math.min(i + k - 1, j - 1))
        end
        for _, m in ipairs(drawn.marks) do mark(base + m[1], m[2], m[3]) end
        for r, tg in pairs(drawn.targets) do c.targets[base + r] = tg end
        i = j
        goto continue
      end
    end

    -- ── setext heading ────────────────────────────────────────
    if not blank and i < n and not is_block_start(line) then
      local under = lines[i + 1]
      local level = under:match("^ ? ? ?=+%s*$") and 1 or (under:match("^ ? ? ?%-+%s*$") and 2 or nil)
      if level then
        local hl = "AutodocPreviewH" .. level
        local row = emit(line, i)
        mark(row, 0, { end_col = #line, hl_group = hl })
        local text = vim.trim(line)
        c.headings[#c.headings + 1] = { row = row, level = level, text = text, slug = inline.slug(text) }
        local urow = emit(under, i + 1)
        mark(urow, 0, {
          virt_text = { { string.rep(level == 1 and "═" or "─", math.max(strwidth(line), 3)), hl } },
          virt_text_pos = "overlay",
          virt_text_win_col = 0,
        })
        i = i + 2
        goto continue
      end
    end

    -- ── horizontal rule ───────────────────────────────────────
    do
      local lead = line:match("^(%s*)")
      local compact = line:gsub("%s", "")
      if #lead <= 3 and #compact >= 3
          and (compact:match("^%-+$") or compact:match("^%*+$") or compact:match("^_+$")) then
        local row = emit(line, i)
        mark(row, 0, {
          virt_text = { { string.rep("─", width), "AutodocPreviewRule" } },
          virt_text_pos = "overlay",
          virt_text_win_col = 0,
        })
        i = i + 1
        goto continue
      end
    end

    -- ── ATX heading ───────────────────────────────────────────
    do
      local lead, hashes, sp = line:match("^( ? ? ?)(#+)(%s+)")
      if not hashes then lead, hashes = line:match("^( ? ? ?)(#+)$"); sp = "" end
      if hashes and #hashes <= 6 then
        local level = #hashes
        local hl = "AutodocPreviewH" .. level
        local row = emit(line, i)
        local pe = #lead + #hashes + #sp
        mark(row, #lead, { end_col = pe, conceal = "" })
        mark(row, #lead, { virt_text = { { HEADING_ICONS[level], hl } }, virt_text_pos = "inline" })
        if pe < #line then mark(row, pe, { end_col = #line, hl_group = hl }) end
        local text = vim.trim((line:sub(pe + 1):gsub("%s+#+%s*$", "")))
        c.headings[#c.headings + 1] = { row = row, level = level, text = text, slug = inline.slug(text) }
        add_targets(row, line, defs)
        i = i + 1
        goto continue
      end
    end

    -- ── block quote / callout ─────────────────────────────────
    if line:match("^%s*>") then
      local callout
      local head = lines[i]
      local qprefix, kind, fold, title = head:match("^(%s*>%s*)%[!(%a+)%]([%+%-]?)%s*(.*)$")
      if kind and M.CALLOUTS[kind:upper()] then callout = M.CALLOUTS[kind:upper()] end
      local hl = callout and callout.hl or "AutodocPreviewQuote"
      local first = true
      while i <= n and lines[i]:match("^%s*>") do
        local l = lines[i]
        local row = emit(l, i)
        local run = l:match("^([%s>]*)")
        for p = 1, #run do
          if run:sub(p, p) == ">" then
            mark(row, p - 1, { end_col = p, conceal = "▎", hl_group = hl })
          end
        end
        if first and callout then
          local s = #qprefix
          local e = s + #kind + 3 + #fold
          mark(row, s, { end_col = e, conceal = "" })
          local label = callout.icon .. " " .. (title == "" and callout.title or "")
          mark(row, s, { virt_text = { { label, hl } }, virt_text_pos = "inline" })
          if title ~= "" then mark(row, e, { end_col = #l, hl_group = hl }) end
          mark(row, 0, { sign_text = callout.icon, sign_hl_group = hl })
        end
        add_targets(row, l, defs)
        first = false
        i = i + 1
      end
      goto continue
    end

    -- ── list item / task box ──────────────────────────────────
    do
      local lead, marker, sp, rest = line:match("^(%s*)([%-%*%+])(%s+)(.*)$")
      local ordered = false
      if not marker then
        lead, marker, sp, rest = line:match("^(%s*)(%d+[%.%)])(%s+)(.*)$")
        ordered = marker ~= nil
      end
      if marker then
        local row = emit(line, i)
        local ms = #lead
        local box = rest:match("^%[([ xX%-])%]")
        local bs = ms + #marker + #sp
        if box then
          local done = box == "x" or box == "X"
          if not ordered then mark(row, ms, { end_col = bs, conceal = "" }) end
          if ordered then mark(row, ms, { end_col = ms + #marker, hl_group = "AutodocPreviewListNumber" }) end
          mark(row, bs, {
            end_col = bs + 3,
            conceal = done and "☑" or "☐",
            hl_group = done and "AutodocPreviewTaskDone" or "AutodocPreviewTaskOpen",
          })
        elseif ordered then
          mark(row, ms, { end_col = ms + #marker, hl_group = "AutodocPreviewListNumber" })
        else
          local depth = math.floor(#lead:gsub("\t", "  ") / 2) % #BULLETS + 1
          mark(row, ms, { end_col = ms + 1, conceal = BULLETS[depth], hl_group = "AutodocPreviewBullet" })
        end
        add_targets(row, line, defs)
        i = i + 1
        goto continue
      end
    end

    -- ── image alone on its line → placeholder ─────────────────
    do
      local alt, path = inline.image_only(line)
      if alt then
        local text = "▣ " .. (alt ~= "" and alt or "image") .. "  " .. path
        local row = emit(text, i)
        mark(row, 0, { end_col = #text, hl_group = "AutodocPreviewImage" })
        c.targets[row] = { { s = 0, e = #text, target = path, kind = "image" } }
        i = i + 1
        goto continue
      end
    end

    -- ── link definition / paragraph ───────────────────────────
    do
      local row = emit(line, i)
      if line:match("^ ? ? ?%[[^%]]+%]:%s*%S") then
        mark(row, 0, { end_col = #line, hl_group = "AutodocPreviewLinkDef" })
        local dest = line:match("^ ? ? ?%[[^%]]+%]:%s*(%S+)")
        local s = line:find(dest, 1, true) - 1
        c.targets[row] = { { s = s, e = s + #dest, target = inline.destination(dest), kind = "link" } }
      elseif not blank then
        add_targets(row, line, defs)
      end
      i = i + 1
    end

    ::continue::
  end

  if #out == 0 then out[1] = "" ; src[1] = 1 end
  return c
end

---Write `content` into the preview buffer `buf` under namespace `ns`.
---@param buf integer
---@param ns integer
---@param content AutodocPreviewContent
function M.apply(buf, ns, content)
  local was = vim.bo[buf].modifiable
  vim.bo[buf].modifiable = true
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, content.lines)
  vim.bo[buf].modifiable = was
  vim.api.nvim_buf_clear_namespace(buf, ns, 0, -1)
  local set = vim.api.nvim_buf_set_extmark
  for _, m in ipairs(content.marks) do
    local o = m[3]
    o.strict = false
    set(buf, ns, m[1], m[2], o)
  end
end

---The display width the longest preview line needs.
---@param content AutodocPreviewContent
---@return integer
function M.max_width(content)
  local w = 0
  for _, l in ipairs(content.lines) do
    local lw = strwidth(l)
    if lw > w then w = lw end
  end
  return w
end

return M
