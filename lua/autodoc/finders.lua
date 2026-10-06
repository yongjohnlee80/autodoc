---The KB pickers beside the search (`<leader>mf`), as the TUI has them:
---
---  files      `<leader>mF`  a document of the selected KB by name (the TUI's SPC o): index.list,
---                           paged, matched by the picker
---  recent     `<leader>mr`  the files opened last, newest first, across KBs (the TUI's SPC r): the
---                           daemon's `tui.recent` preference, so Neovim and the TUI keep one list
---  backlinks  `<leader>ml`  the documents linking to this file (the TUI's SPC l): graph.backlinks
---
---Each is a snacks.picker when snacks is installed (the file previewed, `<M-p>` sends it to a
---preview slot), else vim.ui.select. Opening a KB file in Neovim puts it first among the recent
---files, as opening it in the TUI does, while a session is up: opening a file never starts the
---daemon.
---@module 'autodoc.finders'

local M = {}

M.PAGE = 1000
-- The TUI's: the preference, and how many files it keeps (tui/recent.go).
M.PREF_RECENT = "tui.recent"
M.MAX_RECENT = 20

local function session() return require("autodoc.session") end
local function notify(msg, level) vim.notify(msg, level or vim.log.levels.INFO, { title = "autodoc" }) end

---present shows items, `{ file, text, pos? }`, in a picker titled title.
---@param title string
---@param items table[]
function M.present(title, items)
  local picker = require("autodoc.picker")
  local snacks = picker.snacks()
  if not snacks then
    return vim.ui.select(items, { prompt = title, format_item = function(it) return it.text end }, function(choice)
      if choice then picker._open_at(vim.tbl_extend("keep", choice, { pos = { 1, 0 } })) end
    end)
  end
  snacks.picker.pick({
    source = "autodoc",
    title = title,
    items = items,
    format = "file",
    preview = "file",
    confirm = "jump",
    actions = {
      autodoc_preview = function(p, item)
        item = item or (p.current and p:current())
        if item then picker.to_preview(item) end
      end,
    },
    win = { input = { keys = { ["<M-p>"] = { "autodoc_preview", mode = { "i", "n" }, desc = "send to a preview slot" } } } },
  })
end

---with_root calls back with the selected KB's name and root, or tells why there is none.
---@param cb fun(ws: string, root: string)
local function with_selected(cb)
  session().resolve_selected(function(ws)
    if not ws then
      return notify("autodoc: no KB selected, and this project has no primary KB: <leader>mw chooses one", vim.log.levels.WARN)
    end
    require("autodoc.picker").root_of(ws, function(root, err)
      if not root then return notify(err and err.message or ("autodoc: no workspace named " .. ws), vim.log.levels.WARN) end
      cb(ws, root)
    end)
  end)
end

-- ─── files: a document by name ─────────────────────────────────

---list_paths lists every path of workspace ws: index.list, a page at a time. `cb(paths, err)`.
---@param ws string
---@param cb fun(paths: string[]|nil, err: table|nil)
function M.list_paths(ws, cb)
  local paths = {}
  local function page(after)
    session().request("index.list", { ws, after, M.PAGE }, function(res, err)
      if err then return cb(nil, err) end
      for _, d in ipairs(type(res) == "table" and res.docs or {}) do paths[#paths + 1] = d.path end
      if res.more and #(res.docs or {}) > 0 then return page(res.docs[#res.docs].path) end
      cb(paths, nil)
    end)
  end
  page("")
end

---files picks a document of the selected KB by name.
function M.files()
  with_selected(function(ws, root)
    M.list_paths(ws, function(paths, err)
      if err then return notify("autodoc: " .. err.message, vim.log.levels.WARN) end
      if #paths == 0 then return notify("autodoc: " .. ws .. " has no documents") end
      local items = {}
      for i, p in ipairs(paths) do items[i] = { idx = i, file = root .. "/" .. p, path = p, text = p } end
      M.present("KB " .. ws .. " · files", items)
    end)
  end)
end

-- ─── recent files, shared with the TUI ─────────────────────────

---decode_recent reads the preference's JSON: a list of `{workspace, path}`; anything else is none.
---@param s string|nil
---@return { workspace: string, path: string }[]
function M.decode_recent(s)
  if type(s) ~= "string" or s == "" then return {} end
  local ok, v = pcall(vim.json.decode, s)
  if not ok or not vim.islist(v) then return {} end
  local out = {}
  for _, d in ipairs(v) do
    if type(d) == "table" and type(d.workspace) == "string" and type(d.path) == "string" then
      out[#out + 1] = { workspace = d.workspace, path = d.path }
    end
  end
  return out
end

---with_recent is docs with d first, once, at most MAX_RECENT of them (the TUI's withRecent).
---@param docs { workspace: string, path: string }[]
---@param d { workspace: string, path: string }
---@return { workspace: string, path: string }[]
function M.with_recent(docs, d)
  local out = { d }
  for _, o in ipairs(docs) do
    if #out >= M.MAX_RECENT then break end
    if not (o.workspace == d.workspace and o.path == d.path) then out[#out + 1] = o end
  end
  return out
end

---read_recent reads the recent files from the daemon's preferences. `cb(docs, err)`.
---@param cb fun(docs: table[]|nil, err: table|nil)
function M.read_recent(cb)
  session().call("preference.list", {}, function(prefs, err)
    if err then return cb(nil, err) end
    cb(M.decode_recent(type(prefs) == "table" and prefs[M.PREF_RECENT] or nil), nil)
  end)
end

-- The files noted and not yet written, oldest first; one read-modify-write runs at a time, so two
-- quick opens never read the same list and overwrite one another.
local _pending, _writing = {}, false

---drain writes the next noted file: it reads the SHARED list first (the TUI changes it too) and
---skips the write only when that list already has the file first.
local function drain()
  if _writing then return end
  local d = table.remove(_pending, 1)
  if not d then return end
  if not session().is_ready() then
    _pending = {}
    return
  end
  _writing = true
  local function done()
    _writing = false
    drain()
  end
  M.read_recent(function(docs)
    if not docs then return done() end
    local head = docs[1]
    if head and head.workspace == d.workspace and head.path == d.path then return done() end
    session().call("preference.set", { M.PREF_RECENT, vim.json.encode(M.with_recent(docs, d)) }, function() done() end)
  end)
end

---note_recent puts file rel of workspace ws first among the recent files, when a session is up.
---Neovim's own notes are written in turn; a TUI writing between this read and write can still
---lose its entry (the daemon has no atomic update of a preference), as the TUI's own write can.
---@param ws string
---@param rel string
function M.note_recent(ws, rel)
  if not session().is_ready() then return end
  _pending[#_pending + 1] = { workspace = ws, path = rel }
  drain()
end

---on_open is the BufReadPost hook: a file inside a listed KB is noted as recent.
---@param path string an absolute path
function M.on_open(path)
  if not session().is_ready() then return end
  local ws, rel = session().workspace_of(vim.fs.normalize(path))
  if ws and rel and rel ~= "" then M.note_recent(ws, rel) end
end

---recent picks among the files opened last, across KBs; a KB that is gone is left out.
function M.recent()
  M.read_recent(function(docs, err)
    if err then return notify("autodoc: " .. err.message, vim.log.levels.WARN) end
    session().workspaces(function(list, werr)
      if werr then return notify("autodoc: " .. werr.message, vim.log.levels.WARN) end
      local roots = {}
      for _, w in ipairs(list or {}) do roots[w.name] = w.root end
      local items = {}
      for _, d in ipairs(docs or {}) do
        if roots[d.workspace] then
          items[#items + 1] = { idx = #items + 1, file = vim.fs.normalize(roots[d.workspace]) .. "/" .. d.path,
            path = d.path, text = d.workspace .. " · " .. d.path }
        end
      end
      if #items == 0 then return notify("autodoc: no recent KB files yet") end
      M.present("KB · recent files", items)
    end)
  end)
end

-- ─── backlinks ─────────────────────────────────────────────────

---backlinks picks among the documents that link to the current file.
---@param path string? an absolute path; the current buffer's by default
function M.backlinks(path)
  path = vim.fs.normalize(path or vim.api.nvim_buf_get_name(0))
  if path == "" then return notify("autodoc: this buffer has no file", vim.log.levels.WARN) end
  local function run()
    local ws, rel = session().workspace_of(path)
    if not ws or not rel or rel == "" then
      return notify("autodoc: " .. vim.fn.fnamemodify(path, ":~") .. " is in no KB AutoDoc serves", vim.log.levels.WARN)
    end
    local root = vim.fs.normalize(session().workspace(ws).root)
    session().call("graph.backlinks", { ws, rel }, function(links, err)
      if err then return notify("autodoc: " .. err.message, vim.log.levels.WARN) end
      local items, seen = {}, {}
      for _, l in ipairs(type(links) == "table" and links or {}) do
        if not seen[l.path] then
          seen[l.path] = true
          items[#items + 1] = { idx = #items + 1, file = root .. "/" .. l.path, path = l.path,
            text = l.path .. (l.raw and l.raw ~= "" and ("  " .. l.raw) or "") }
        end
      end
      if #items == 0 then return notify("autodoc: nothing links to " .. rel) end
      M.present("KB " .. ws .. " · links to " .. rel, items)
    end)
  end
  if session().workspace_of(path) then return run() end
  session().workspaces(function(_, err)
    if err then return notify("autodoc: " .. err.message, vim.log.levels.WARN) end
    run()
  end)
end

-- Test seam.
function M._reset_for_tests() _pending, _writing = {}, false end

return M
