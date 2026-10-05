-- autodoc.kb.refs: find every path-bearing reference in a KB document, and resolve it against a
-- tree. A port of the 2026-10-06 inventory script (kbrefs.py) that ADR 1791209946 §8 is based on,
-- with its classes:
--
--   wikilink          [[path/qualified]] (sub path:*), [[bare-name]] (sub bare:*, never rewritten)
--   mdlink            [text](target): rel-dot, rel-dotdot, rel-bare, abs-kb, tilde-kb, $KB_ROOT,
--                     external-url / anchor-only / abs-outside-kb (never rewritten)
--   bare-path         prefixed mentions in prose and inline code: abs-kb, tilde-kb,
--                     $AUTO_AGENTS_KB_ROOT, $KB_ROOT, shared/, agents/<n>/
--   frontmatter-path  the same prefixed forms inside frontmatter, plus other-relpath: a plain
--                     `dir/file.md` value (relative to the file, else to the KB root)
--
-- Each reference carries its context (prose, code, fence, frontmatter): the migration rewrites
-- every context but fence, which it reports. Second `---` blocks in a todo body count as
-- frontmatter. Differences from kbrefs.py, all deliberate: the KB root is the one being scanned
-- (not a fixed path); an absolute KB path must end at a path boundary; frontmatter relative paths
-- may climb with any number of `../`.

local util = require("autodoc.kb.util")

local M = {}

-- ─── tree views ─────────────────────────────────────────────────────

---A view of a tree: its files and directories (relative paths), for resolving references.
---@param files string[]
---@param dirs string[]?  directories that exist besides files' parents (empty ones)
function M.view(files, dirs)
  local v = { files = {}, cf = {}, dirs = {}, by_base = {}, list = files }
  for _, f in ipairs(files) do
    v.files[f] = true
    v.cf[util.fold(f)] = v.cf[util.fold(f)] or f
    local b = util.fold(util.basename(f))
    v.by_base[b] = v.by_base[b] or {}
    table.insert(v.by_base[b], f)
    local d = util.dirname(f)
    while d ~= "" and not v.dirs[d] do
      v.dirs[d] = true
      d = util.dirname(d)
    end
  end
  for _, d in ipairs(dirs or {}) do v.dirs[d] = true end
  return v
end

-- ─── resolution ─────────────────────────────────────────────────────

local function split_frag(t)
  local p, frag = t:match("^([^#?]*)([#?].*)$")
  if p then return p, frag end
  return t, ""
end
M.split_frag = split_frag

---Resolve a relative target from file src: a file, a file with `.md` added, or a directory
---(returned with a trailing "/"). "OUTSIDE" when it leaves the KB; nil when nothing is there.
function M.resolve_rel(src, target, view)
  local t = util.unquote(split_frag(target))
  if t == "" then return nil end
  local full = util.normpath(util.join(util.dirname(src), t))
  if full == ".." or full:sub(1, 3) == "../" then return "OUTSIDE" end
  if full == "." then return nil end
  if view.files[full] then return full end
  if view.files[full .. ".md"] then return full .. ".md" end
  if view.dirs[full] then return full .. "/" end
  return nil
end

---Resolve a KB-root-relative path (a prefixed or bare mention): the file, the file with `.md`,
---or the directory with a trailing "/".
function M.resolve_root(rel, view)
  local p = split_frag(rel)
  p = util.unquote(p):gsub("/+$", "")
  if p == "" then return nil end
  p = util.normpath(p)
  if p:sub(1, 2) == ".." then return nil end
  if view.files[p] then return p end
  if view.files[p .. ".md"] then return p .. ".md" end
  if view.dirs[p] then return p .. "/" end
  return nil
end

local function has_ext(target)
  return target:match("%.md$") ~= nil or util.basename(target):find(".", 1, true) ~= nil
end
M.has_ext = has_ext

---Resolve a path-qualified wikilink target (Obsidian-like): from the vault root, relative to the
---source, case-insensitively from the root, then by path suffix. Returns resolved, how.
function M.resolve_wiki(src, target, view)
  local t = has_ext(target) and target or (target .. ".md")
  if t:sub(1, 1) == "/" then t = t:sub(2) end
  local r = util.normpath(t)
  if view.files[r] then return r, "root" end
  local rr = util.normpath(util.join(util.dirname(src), t))
  if view.files[rr] then return rr, "relative" end
  local ci = view.cf[util.fold(r)]
  if ci then return ci, "root-ci" end
  if target:sub(1, 1) ~= "." then
    local want = "/" .. util.fold(r)
    local hits = {}
    for _, f in ipairs(view.by_base[util.fold(util.basename(r))] or {}) do
      local ff = util.fold(f)
      if ff:sub(-#want) == want then hits[#hits + 1] = f end
    end
    if #hits == 1 then return hits[1], "suffix" end
    if #hits > 1 then
      table.sort(hits, function(a, b) return #a < #b or (#a == #b and a < b) end)
      return hits[1], "suffix-ambiguous"
    end
  end
  return nil, "unresolved"
end

-- ─── scanning ───────────────────────────────────────────────────────

local W = "[%w_\128-\255]"
local PATHCH = "[%w_%.@%%%+~%-/\128-\255]"
local LB_BAD = "[%w_%./%-%$}\128-\255]" -- (?<![\w./\-$}])

local function strip_trailing(s)
  return (s:gsub("[%.,;:%)'\"`%*]+$", ""))
end

---Per line: fence flag and inline-code spans ({start, stop} with stop exclusive).
local function code_mask(lines, from)
  local out, fence = {}, nil
  for i, ln in ipairs(lines) do
    if i < from then
      out[i] = { fence = false, spans = {} }
    else
      local tok = ln:match("^%s*(```+)") or ln:match("^%s*(~~~+)")
      if tok then
        if fence == nil then
          fence = tok:sub(1, 1):rep(3)
          out[i] = { fence = true, spans = {} }
          goto continue
        elseif tok:sub(1, 3) == fence then
          fence = nil
          out[i] = { fence = true, spans = {} }
          goto continue
        end
      end
      if fence then
        out[i] = { fence = true, spans = {} }
      else
        local spans, pos = {}, 1
        while true do
          local a, b = ln:find("`+", pos)
          if not a then break end
          local run = ln:sub(a, b)
          local c = ln:find(run, b + 2, true)
          if c then
            spans[#spans + 1] = { a, c + #run }
            pos = c + #run
          else
            pos = b + 1
          end
        end
        out[i] = { fence = false, spans = spans }
      end
    end
    ::continue::
  end
  return out
end

local function ctx_of(m, pos)
  if m.fence then return "fence" end
  for _, s in ipairs(m.spans) do
    if s[1] <= pos and pos < s[2] then return "code" end
  end
  return "prose"
end

local function in_spans(spans, pos)
  for _, s in ipairs(spans) do
    if s[1] <= pos and pos < s[2] then return true end
  end
  return false
end

-- Markdown link at position i (a "["): returns stop (exclusive), target, target_start, embed.
local function parse_mdlink(ln, i)
  local j = i + 1
  local n = #ln
  while j <= n do
    local c = ln:sub(j, j)
    if c == "]" then break end
    if c == "[" then
      local k = j + 1
      while k <= n and ln:sub(k, k) ~= "]" and ln:sub(k, k) ~= "[" do k = k + 1 end
      if ln:sub(k, k) ~= "]" then return nil end
      j = k + 1
    else
      j = j + 1
    end
  end
  if j > n or ln:sub(j + 1, j + 1) ~= "(" then return nil end
  local k = j + 2
  while ln:sub(k, k):match("%s") do k = k + 1 end
  if ln:sub(k, k) == "<" then k = k + 1 end
  local ts = k
  while k <= n and not ln:sub(k, k):match("[%)%s>]") do k = k + 1 end
  if k == ts then return nil end
  local target = ln:sub(ts, k - 1)
  if ln:sub(k, k) == ">" then k = k + 1 end
  local save = k
  local sp = k
  while ln:sub(sp, sp):match("%s") do sp = sp + 1 end
  local q = ln:sub(sp, sp)
  if sp > k and (q == '"' or q == "'") then
    local e = sp + 1
    while e <= n and ln:sub(e, e) ~= '"' and ln:sub(e, e) ~= "'" do e = e + 1 end
    if e <= n then k = e + 1 else k = save end
  end
  while ln:sub(k, k):match("%s") do k = k + 1 end
  if ln:sub(k, k) ~= ")" then return nil end
  return k + 1, target, ts
end

---Build the scanner's prefix set for a KB root.
---@param root string absolute KB root
---@param agent_names string[]
function M.prefixes(root, agent_names)
  local p = { abs = { root } }
  local real = vim.uv.fs_realpath(root)
  if real and real ~= root then p.abs[#p.abs + 1] = real end
  local home = vim.env.HOME
  if home and home ~= "" and root:sub(1, #home + 1) == home .. "/" then
    p.tilde = "~" .. root:sub(#home + 1)
  end
  p.agents = {}
  for _, n in ipairs(agent_names or {}) do p.agents[n] = true end
  return p
end

M.SHARED_SUBS = { adrs = true, conventions = true, playbooks = true, reference = true, glossary = true, routes = true,
  sources = true, synthesis = true, ["bug-fixes"] = true, prs = true, scripts = true, agents = true }

-- the prefix forms, in kbrefs.py's order; each finder returns a list of {start, text, prefix, kbrel}
local function find_bare(ln, px)
  local found = {}
  -- abs-kb (optionally file://) and tilde-kb
  local function literal(lit, sub)
    local pos = 1
    while true do
      local a, b = ln:find(lit, pos, true)
      if not a then break end
      local nx = ln:sub(b + 1, b + 1)
      if nx == "" or nx == "/" or not nx:match(PATHCH) then
        local s = a
        local prefix = lit
        if ln:sub(a - 7, a - 1) == "file://" then
          s = a - 7
          prefix = "file://" .. lit
        end
        local e = b
        if nx == "/" then
          e = b + 1
          while ln:sub(e + 1, e + 1):match(PATHCH) do e = e + 1 end
        end
        local raw = ln:sub(s, e)
        local text = strip_trailing(raw)
        found[#found + 1] = { start = s, text = text, prefix = prefix, kbrel = text:sub(#prefix + 1):gsub("^/", ""), sub = sub }
      end
      pos = b + 1
    end
  end
  for _, a in ipairs(px.abs) do literal(a, "abs-kb") end
  if px.tilde then literal(px.tilde, "tilde-kb") end
  for _, var in ipairs({ { "AUTO_AGENTS_KB_ROOT", "$AUTO_AGENTS_KB_ROOT" }, { "KB_ROOT", "$KB_ROOT" } }) do
    for _, form in ipairs({ "$" .. var[1] .. "/", "${" .. var[1] .. "}/" }) do
      local pos = 1
      while true do
        local a, b = ln:find(form, pos, true)
        if not a then break end
        local e = b
        while ln:sub(e + 1, e + 1):match(PATHCH) do e = e + 1 end
        if e > b then
          local text = strip_trailing(ln:sub(a, e))
          if #text > #form then
            found[#found + 1] = { start = a, text = text, prefix = form:sub(1, -2), kbrel = text:sub(#form + 1), sub = var[2] }
          end
        end
        pos = b + 1
      end
    end
  end
  -- shared/<sub> and agents/<name>
  local function rooted(word, ok_name, sub)
    local pos = 1
    while true do
      local a, b = ln:find(word .. "/", pos, true)
      if not a then break end
      local prev = a > 1 and ln:sub(a - 1, a - 1) or ""
      if prev == "" or not prev:match(LB_BAD) then
        local name_end = b
        while ln:sub(name_end + 1, name_end + 1):match("[%w_%-\128-\255]") do name_end = name_end + 1 end
        -- the name is the longest known one that ends at a word boundary
        local matched
        for e = name_end, b + 1, -1 do
          local name = ln:sub(b + 1, e)
          local nx = ln:sub(e + 1, e + 1)
          if ok_name(name) and (nx == "" or not nx:match(W)) then
            matched = e
            break
          end
        end
        if matched then
          local e = matched
          while ln:sub(e + 1, e + 1):match(PATHCH) do e = e + 1 end
          local text = strip_trailing(ln:sub(a, e))
          found[#found + 1] = { start = a, text = text, prefix = "", kbrel = text, sub = sub }
        end
      end
      pos = b + 1
    end
  end
  rooted("shared", function(n) return M.SHARED_SUBS[n] end, "shared/")
  rooted("agents", function(n) return px.agents[n] end, "agents/<n>/")
  return found
end

---Scan one document. Returns a list of reference records:
---{class, sub, line, col, old, ctx, fm_key, kind = "wiki"|"rel"|"root"|"none", target, embed}
---  col/old: the span the migration rewrites (the path text only; anchors, aliases and titles stay)
---@param src string relative path of the document
---@param text string
---@param opts table {px = M.prefixes(), todo = bool}
function M.scan(src, text, opts)
  local px = opts.px
  local lines = util.split_lines(text)
  local refs = {}
  local fm_close = 0
  if lines[1] and lines[1]:match("^%-%-%-%s*$") then
    for i = 2, math.min(#lines, 400) do
      if lines[i]:match("^%-%-%-%s*$") or lines[i]:match("^%.%.%.%s*$") then
        fm_close = i
        break
      end
    end
  end
  -- second frontmatter blocks in a todo body
  local fm2 = {}
  if opts.todo and fm_close > 0 then
    local i = fm_close + 1
    while i <= #lines do
      if lines[i]:match("^%-%-%-%s*$") and lines[i + 1] and lines[i + 1]:match("^[%w_][%w_%-]*%s*:") then
        local j = i + 1
        while j <= #lines and not lines[j]:match("^%-%-%-%s*$") do
          fm2[j] = true
          j = j + 1
        end
        i = j + 1
      else
        i = i + 1
      end
    end
  end
  local mask = code_mask(lines, fm_close + 1)
  local fm_key
  for i, ln in ipairs(lines) do
    local in_fm = (i > 1 and i < fm_close) or fm2[i] == true
    if in_fm then
      local k = ln:match("^([%a_][%w_%-]*)%s*:")
      if k then fm_key = k end
    end
    local function ctx(pos)
      if in_fm then return "frontmatter" end
      return ctx_of(mask[i], pos)
    end
    local function add(r)
      r.line = i
      r.fm_key = in_fm and fm_key or nil
      refs[#refs + 1] = r
    end
    local consumed = {}

    -- 1. wikilinks
    local pos = 1
    while true do
      local s = ln:find("[[", pos, true)
      if not s then break end
      local e = ln:find("]]", s + 2, true)
      if not e then break end
      local inner = ln:sub(s + 2, e - 1)
      if inner == "" or inner:find("[%[%]]") then
        pos = s + 1
      else
        local start = s
        local embed = s > 1 and ln:sub(s - 1, s - 1) == "!"
        if embed then start = s - 1 end
        consumed[#consumed + 1] = { start, e + 2 }
        local target = inner:match("^[^|#^]*")
        local lead = target:match("^%s*")
        target = vim.trim(target)
        local col = s + 2 + #lead
        local c = ctx(s)
        if target == "" then
          add({ class = "wikilink", sub = "self-anchor", ctx = c, kind = "none", old = "", col = col })
        elseif target:find("/", 1, true) then
          local sub = "path:other"
          local stripped = target:gsub("^[%./]+", "")
          if stripped:sub(1, 7) == "shared/" then
            sub = "path:shared/"
          elseif target:sub(1, 1) == "." then
            sub = "path:relative"
          elseif target:sub(1, 7) == "agents/" then
            sub = "path:agents/"
          end
          add({ class = "wikilink", sub = sub, ctx = c, kind = "wiki", old = target, col = col, target = target, embed = embed })
        else
          add({ class = "wikilink", sub = "bare", ctx = c, kind = "bare", old = target, col = col, target = target })
        end
        pos = e + 2
      end
    end

    -- 2. markdown links
    pos = 1
    while true do
      local s = ln:find("[", pos, true)
      if not s then break end
      if in_spans(consumed, s) then
        pos = s + 1
      else
        local stop, tgt, ts = parse_mdlink(ln, s)
        if not stop then
          pos = s + 1
        else
          local start = (s > 1 and ln:sub(s - 1, s - 1) == "!") and s - 1 or s
          consumed[#consumed + 1] = { start, stop }
          local c = ctx(s)
          local rec = { class = "mdlink", ctx = c, old = tgt, col = ts, target = tgt }
          local raw = tgt:gsub("^file://", "")
          local is_file_url = raw ~= tgt
          local matched_prefix
          for _, a in ipairs(px.abs) do
            if raw == a or raw:sub(1, #a + 1) == a .. "/" then matched_prefix = a end
          end
          if not matched_prefix and px.tilde and (raw == px.tilde or raw:sub(1, #px.tilde + 1) == px.tilde .. "/") then
            matched_prefix = px.tilde
          end
          if tgt:match("^[%a][%w+.%-]*:") and not is_file_url then
            rec.sub, rec.kind = "external-url", "none"
          elseif tgt:sub(1, 1) == "#" then
            rec.sub, rec.kind = "anchor-only", "none"
          elseif matched_prefix then
            rec.sub = matched_prefix == px.tilde and "tilde-kb" or "abs-kb"
            rec.kind = "root"
            rec.prefix = (is_file_url and "file://" or "") .. matched_prefix
            rec.kbrel = raw:sub(#matched_prefix + 2)
          elseif raw:match("^%${?KB_ROOT}?/") or raw:match("^%${?AUTO_AGENTS_KB_ROOT}?/") then
            rec.sub, rec.kind = "$KB_ROOT", "root"
            rec.prefix = (is_file_url and "file://" or "") .. raw:match("^(%${?[%w_]+}?)/")
            rec.kbrel = raw:sub(#raw:match("^(%${?[%w_]+}?/)") + 1)
          elseif raw:sub(1, 1) == "/" or raw:sub(1, 1) == "~" or raw:sub(1, 1) == "$" then
            rec.sub, rec.kind = "abs-outside-kb", "none"
          else
            rec.sub = raw:sub(1, 2) == "./" and "rel-dot" or (raw:sub(1, 3) == "../" and "rel-dotdot" or "rel-bare")
            rec.kind = "rel"
          end
          add(rec)
          pos = stop
        end
      end
    end

    -- 3. prefixed bare paths outside the spans already classified
    local taken = vim.deepcopy(consumed)
    for _, m in ipairs(find_bare(ln, px)) do
      if not in_spans(taken, m.start) then
        taken[#taken + 1] = { m.start, m.start + #m.text }
        add({
          class = in_fm and "frontmatter-path" or "bare-path",
          sub = m.sub,
          ctx = ctx(m.start),
          kind = "root",
          old = m.text,
          col = m.start,
          prefix = m.prefix,
          kbrel = m.kbrel,
        })
      end
    end

    -- 4. plain relative .md paths in frontmatter
    if in_fm then
      pos = 1
      while true do
        local a, b = ln:find("[%w_%.%-/\128-\255]+", pos)
        if not a then break end
        local run = ln:sub(a, b)
        local prev = a > 1 and ln:sub(a - 1, a - 1) or ""
        if prev ~= "$" and not in_spans(taken, a) then
          -- the longest prefix ending in ".md" at a word boundary
          local best
          local from = 1
          while true do
            local x, y = run:find("%.md", from)
            if not x then break end
            local nx = run:sub(y + 1, y + 1)
            if nx == "" or not nx:match(W) then best = y end
            from = y + 1
          end
          if best then
            local cand = run:sub(1, best)
            local body = cand:gsub("^%./", "", 1)
            while body:sub(1, 3) == "../" do body = body:sub(4) end
            if body:find("/", 1, true) and body:match("^[%w_%-%.\128-\255][%w_%.%-/\128-\255]*$")
              and not body:find("//", 1, true) and body:sub(1, 1) ~= "/" then
              taken[#taken + 1] = { a, a + #cand }
              add({ class = "frontmatter-path", sub = "other-relpath", ctx = "frontmatter", kind = "fmrel", old = cand, col = a, target = cand })
            end
          end
        end
        pos = b + 1
      end
    end
  end
  return refs
end

---Resolve a reference against a view, from source src. Returns target (file, or dir with "/"),
---and how ("root", "relative", "suffix", … for wikilinks; "rel" or "root" for fmrel).
function M.resolve(ref, src, view, text)
  text = text or ref.old
  if ref.kind == "wiki" then
    return M.resolve_wiki(src, text, view)
  elseif ref.kind == "rel" then
    local r = M.resolve_rel(src, text, view)
    if r == "OUTSIDE" then return nil, "outside" end
    return r, "rel"
  elseif ref.kind == "root" then
    local rel = text
    if ref.prefix and ref.prefix ~= "" then
      local raw = text
      if raw:sub(1, #ref.prefix) ~= ref.prefix then return nil, "prefix-changed" end
      rel = raw:sub(#ref.prefix + 1):gsub("^/", "")
    end
    return M.resolve_root(rel, view), "root"
  elseif ref.kind == "fmrel" then
    local how = ref.how
    if how == nil or how == "rel" then
      local r = M.resolve_rel(src, text, view)
      if r and r ~= "OUTSIDE" then return r, "rel" end
      if how == "rel" then return nil, "rel" end
    end
    local r = M.resolve_root(text, view)
    return r, "root"
  end
  return nil, "none"
end

return M
