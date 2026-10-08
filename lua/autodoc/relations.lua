---What a KB document is connected to, by the edge's kind and direction (ADR 1791430651 §4.4, the
---TUI's Relations drawer): superseded by · supersedes · sources · cited by · amends · amended by ·
---related · adr · links · backlinks, then what names nothing and the supersession loops. Depth 2
---adds the documents two links away under the first-ring one that reaches them.
---
---  rows(ws, rel, data)   the rows, a pure function of what the daemon answered (tui/relations.go)
---  load(ws, rel, deep)   graph.links, graph.backlinks, graph.unresolved (and graph.neighborhood)
---  pick(path?)           `<leader>ml`: the current file's relations in a picker, a kind each
---  open(ws, rel)         opens a related document, the one left behind going on the history
---  back()                `<leader>mo`: the document opened before, as the TUI's SPC b
---  popup()               the right-click menu's "Go to related…" and "Back"
---@module 'autodoc.relations'

local M = {}

-- Body links' kinds (core/index.BodyKinds).
local BODY = { "wikilink", "embed", "markdown" }

---The sections, in order: links OUT of the document of the kinds out, and links INTO it of the
---kinds in. A relation in is the other side's relation out: one naming this document
---superseded_by makes this its successor.
M.SECTIONS = {
  { label = "superseded by", out = { "superseded_by" }, ["in"] = { "supersedes" } },
  { label = "supersedes", out = { "supersedes" }, ["in"] = { "superseded_by" } },
  { label = "sources", out = { "sources" } },
  { label = "cited by", ["in"] = { "sources" } },
  { label = "amends", out = { "amends" } },
  { label = "amended by", ["in"] = { "amends" } },
  { label = "related", out = { "related" }, ["in"] = { "related" } },
  { label = "adr", out = { "adr" } },
  { label = "links", out = BODY },
  { label = "backlinks", ["in"] = { "wikilink", "embed", "markdown", "adr" } },
}

-- How many documents back goes (the TUI's maxHistory).
M.MAX_HISTORY = 32

local function session() return require("autodoc.session") end
local function notify(msg, level) vim.notify(msg, level or vim.log.levels.INFO, { title = "autodoc" }) end
local WARN = vim.log.levels.WARN

---@class autodoc.RelEdge
---@field path string the other end ("" when unresolved)
---@field raw string as written
---@field kind string
---@field resolved boolean

---@class autodoc.RelRow
---@field kind "heading"|"doc"|"beyond"|"unresolved"
---@field text string
---@field ws string|nil
---@field path string|nil a document to open
---@field section string|nil

---rows lays the relations of rel out: each section with what it holds, its count in its heading;
---under each document's first row, at depth 2, the documents beyond it; then the unresolved and
---the loops. It also answers how many first-ring documents there are.
---@param ws string
---@param rel string
---@param data { out: autodoc.RelEdge[], ["in"]: autodoc.RelEdge[], cycles: string[]?, edges: string[][]? }
---@return autodoc.RelRow[] rows, integer neighbours
function M.rows(ws, rel, data)
  local out, inn = data.out or {}, data["in"] or {}
  local ring = { [rel] = true }
  for _, e in ipairs(out) do if e.resolved then ring[e.path] = true end end
  for _, e in ipairs(inn) do ring[e.path] = true end
  -- second[n]: the documents beyond n, each with how it is linked to n
  local second = {}
  for _, e in ipairs(data.edges or {}) do
    local src, dst, kind = e[1], e[2], e[3]
    if ring[src] and src ~= rel and not ring[dst] then
      second[src] = second[src] or {}
      table.insert(second[src], { path = dst, how = kind .. " →" })
    elseif ring[dst] and dst ~= rel and not ring[src] then
      second[dst] = second[dst] or {}
      table.insert(second[dst], { path = src, how = "← " .. kind })
    end
  end
  local rows, under, neighbours = {}, {}, 0
  for _, s in ipairs(M.SECTIONS) do
    local paths, seen = {}, {}
    for _, e in ipairs(out) do
      if e.resolved and vim.tbl_contains(s.out or {}, e.kind) and not seen[e.path] then
        seen[e.path] = true
        paths[#paths + 1] = e.path
      end
    end
    for _, e in ipairs(inn) do
      if vim.tbl_contains(s["in"] or {}, e.kind) and not seen[e.path] then
        seen[e.path] = true
        paths[#paths + 1] = e.path
      end
    end
    if #paths > 0 then
      rows[#rows + 1] = { kind = "heading", text = string.format("%s (%d)", s.label, #paths) }
      for _, p in ipairs(paths) do
        rows[#rows + 1] = { kind = "doc", text = p, ws = ws, path = p, section = s.label }
        if not under[p] then
          under[p] = true
          neighbours = neighbours + 1
          local beyond = second[p] or {}
          table.sort(beyond, function(a, b) return a.path .. a.how < b.path .. b.how end)
          local last
          for _, b in ipairs(beyond) do
            local key = b.path .. "\0" .. b.how
            if key ~= last then
              last = key
              rows[#rows + 1] = { kind = "beyond", text = "› " .. b.path .. "  (" .. b.how .. ")", ws = ws, path = b.path,
                section = s.label }
            end
          end
        end
      end
    end
  end
  local unresolved = {}
  for _, e in ipairs(out) do
    if not e.resolved then unresolved[#unresolved + 1] = e.raw .. "  (" .. e.kind .. ", names no document)" end
  end
  for _, c in ipairs(data.cycles or {}) do unresolved[#unresolved + 1] = c .. "  (supersession goes round a loop)" end
  if #unresolved > 0 then
    rows[#rows + 1] = { kind = "heading", text = string.format("unresolved (%d)", #unresolved) }
    for _, u in ipairs(unresolved) do rows[#rows + 1] = { kind = "unresolved", text = u } end
  end
  return rows, neighbours
end

local function edges_of(res)
  local out = {}
  for _, l in ipairs(type(res) == "table" and res or {}) do
    out[#out + 1] = { path = l.path or "", raw = l.raw or "", kind = l.kind or "", resolved = l.resolved == true }
  end
  return out
end

local CODE_NOT_FOUND = -32061

---load reads what rel of workspace ws is connected to, and its second ring when deep. A document
---not indexed yet has none. `cb(rows, neighbours, err)`; request is the caller's (a view passes its
---own, which drops a superseded reply).
---@param ws string
---@param rel string
---@param deep boolean
---@param cb fun(rows: autodoc.RelRow[]|nil, neighbours: integer|nil, err: table|nil)
---@param request fun(method: string, params: table, cb: fun(res: any, err: table|nil))|nil
function M.load(ws, rel, deep, cb, request)
  request = request or session().request
  local data = {}
  local function step(method, params, apply, next)
    request(method, params, function(res, err)
      if err and err.code ~= CODE_NOT_FOUND then return cb(nil, nil, err) end
      apply(not err and res or nil)
      next()
    end)
  end
  local function done() cb(M.rows(ws, rel, data)) end
  step("graph.links", { ws, rel }, function(r) data.out = edges_of(r) end, function()
    step("graph.backlinks", { ws, rel }, function(r) data["in"] = edges_of(r) end, function()
      -- the workspace's supersession relations that loop: this document's
      step("graph.unresolved", { ws, { kinds = { "supersedes", "superseded_by" } } }, function(r)
        data.cycles = {}
        for _, u in ipairs(type(r) == "table" and r or {}) do
          if u.src == rel and u.reason == "cycle" then data.cycles[#data.cycles + 1] = u.raw end
        end
      end, function()
        if not deep then return done() end
        step("graph.neighborhood", { ws, rel, 2 }, function(r)
          data.edges = {}
          for _, e in ipairs(type(r) == "table" and r.edges or {}) do data.edges[#data.edges + 1] = { e.src, e.dst, e.kind } end
        end, done)
      end)
    end)
  end)
end

-- ─── the document in the editor, and going back ──────────────────

---@class autodoc.FileRef
---@field ws string
---@field rel string

M._history = {}

---current is the KB document in buf (the current buffer by default): its workspace and its path
---in it, or nil.
---@param buf integer?
---@return autodoc.FileRef|nil
function M.current(buf)
  buf = buf or vim.api.nvim_get_current_buf()
  if vim.bo[buf].buftype ~= "" then return nil end
  local name = vim.api.nvim_buf_get_name(buf)
  if name == "" then return nil end
  local ws, rel = session().workspace_of(vim.fs.normalize(name))
  if not ws or not rel or rel == "" then return nil end
  return { ws = ws, rel = rel }
end

local function same(a, b) return a and b and a.ws == b.ws and a.rel == b.rel end

---remember puts ref on the history, once at its top, at most MAX_HISTORY of them.
---@param ref autodoc.FileRef
function M.remember(ref)
  local h = M._history
  if same(h[#h], ref) then return end
  h[#h + 1] = ref
  while #h > M.MAX_HISTORY do table.remove(h, 1) end
end

---edit opens rel of workspace ws in an editor window, never a panel's; true once it is open.
local function edit(ws, rel)
  local w = session().workspace(ws)
  if not (w and w.root) then
    notify("autodoc: no workspace named " .. ws, WARN)
    return false
  end
  local target = require("autodoc.views.panel").editor_target_winid()
  if not target then
    target = vim.api.nvim_open_win(vim.api.nvim_create_buf(false, true), false, { split = "right", win = -1 })
    pcall(vim.api.nvim_set_option_value, "winfixbuf", false, { scope = "local", win = target })
  end
  vim.api.nvim_set_current_win(target)
  -- :edit asks about unsaved changes itself (E37 without 'hidden'): a refused open fails here
  local ok, err = pcall(vim.cmd, "edit " .. vim.fn.fnameescape(vim.fs.normalize(w.root) .. "/" .. rel))
  if not ok then
    notify("autodoc: " .. tostring(err), WARN)
    return false
  end
  return true
end

---open opens a related document; the KB document left behind goes on the history.
---@param ws string
---@param rel string
function M.open(ws, rel)
  local target = require("autodoc.views.panel").editor_target_winid()
  local prev = target and M.current(vim.api.nvim_win_get_buf(target)) or nil
  if edit(ws, rel) and prev and not same(prev, { ws = ws, rel = rel }) then M.remember(prev) end
end

---back opens the document opened before this one, which is not remembered again; its entry
---leaves the history only once it is open (an :edit refused over unsaved changes keeps it).
function M.back()
  local h = M._history
  local cur = M.current()
  while #h > 0 and same(h[#h], cur) do table.remove(h) end
  local prev = h[#h]
  if not prev then
    return notify("autodoc: nothing to go back to: <leader>mo returns to the documents opened before this one")
  end
  if edit(prev.ws, prev.rel) and same(h[#h], prev) then table.remove(h) end
end

-- ─── the picker: <leader>ml ────────────────────────────────────

---pick lists the relations of the file at path (the current buffer's by default) in a picker, a
---kind each; choosing one opens it, as the drawer's Enter does.
---@param path string?
function M.pick(path)
  path = vim.fs.normalize(path or vim.api.nvim_buf_get_name(0))
  if path == "" then return notify("autodoc: this buffer has no file", WARN) end
  local function run()
    local ws, rel = session().workspace_of(path)
    if not ws or not rel or rel == "" then
      return notify("autodoc: " .. vim.fn.fnamemodify(path, ":~") .. " is in no KB AutoDoc serves", WARN)
    end
    local root = vim.fs.normalize(session().workspace(ws).root)
    M.load(ws, rel, false, function(rows, _, err)
      if err then return notify("autodoc: " .. err.message, WARN) end
      local items, seen = {}, {}
      for _, r in ipairs(rows or {}) do
        if r.kind == "doc" and not seen[r.path] then
          seen[r.path] = true
          items[#items + 1] = { idx = #items + 1, file = root .. "/" .. r.path, ws = ws, path = r.path,
            text = string.format("%-14s %s", r.section, r.path) }
        end
      end
      if #items == 0 then return notify("autodoc: " .. rel .. " has no related documents") end
      M.present("KB " .. ws .. " · related to " .. rel, items)
    end, session().call)
  end
  if session().workspace_of(path) then return run() end
  session().workspaces(function(_, err)
    if err then return notify("autodoc: " .. err.message, WARN) end
    run()
  end)
end

---present shows the related documents; choosing one opens it through open, so it is remembered.
---@param title string
---@param items table[]
function M.present(title, items)
  local snacks = require("autodoc.picker").snacks()
  if not snacks then
    return vim.ui.select(items, { prompt = title, format_item = function(it) return it.text end }, function(choice)
      if choice then M.open(choice.ws, choice.path) end
    end)
  end
  snacks.picker.pick({
    source = "autodoc",
    title = title,
    items = items,
    format = "text",
    preview = "file",
    confirm = function(p, item)
      p:close()
      if item then vim.schedule(function() M.open(item.ws, item.path) end) end
    end,
  })
end

-- ─── the right-click menu ──────────────────────────────────────

M.POPUP_RELATED = "AutoDoc:\\ Go\\ to\\ related…"
M.POPUP_BACK = "AutoDoc:\\ Back"

---popup adds "Go to related…" and "Back" to Neovim's right-click menu (PopUp), enabled on a KB
---file and, for Back, while there is somewhere to go back to.
function M.popup()
  pcall(vim.cmd, "anoremenu PopUp.-autodoc- <Nop>")
  vim.cmd("nnoremenu PopUp." .. M.POPUP_RELATED .. " <Cmd>lua require('autodoc.relations').pick()<CR>")
  vim.cmd("nnoremenu PopUp." .. M.POPUP_BACK .. " <Cmd>lua require('autodoc.relations').back()<CR>")
  vim.api.nvim_create_autocmd("MenuPopup", {
    group = vim.api.nvim_create_augroup("autodoc.relations.popup", { clear = true }),
    desc = "autodoc: enable the relations' right-click rows on a KB file",
    callback = function()
      local kb = M.current() ~= nil
      pcall(vim.cmd, (kb and "nmenu enable " or "nmenu disable ") .. "PopUp." .. M.POPUP_RELATED)
      pcall(vim.cmd, ((kb and #M._history > 0) and "nmenu enable " or "nmenu disable ") .. "PopUp." .. M.POPUP_BACK)
    end,
  })
end

function M._reset_for_tests() M._history = {} end

return M
