---autodoc.preview — six floating Markdown previews with cursor memory
---(ADR 1791210485: AutoDoc absorbs md-harpoon).
---
---Six slots, `1 2 3` across the top and `a s d` a few rows lower, each a
---float showing one document rendered by AutoDoc's own renderer
---(`autodoc.preview.render`). Each slot remembers its document and where
---the cursor was; pins persist per project in auto-core state (namespace
---`autodoc.preview`) and follow `worktree:switched`.
---
---API:
---
---  require("autodoc.preview").setup(opts)        subscriptions, verbs, commands
---  require("autodoc.preview").teardown()         undo setup
---  require("autodoc.preview").focus(slot)        open / jump into a slot
---  require("autodoc.preview").render_current(slot)
---  require("autodoc.preview").render_path(slot, path)
---  require("autodoc.preview").close_all()        close floats, keep pins + cursors
---  require("autodoc.preview").find(opts)         Markdown file under cwd → slot
---  require("autodoc.preview").browser(slot?)     open in the browser (Go export)
---  require("autodoc.preview").commands()         define the :AutodocPreview* commands
---
---`slot` is one of "1" "2" "3" "a" "s" "d".
---
---focus(slot) does one of three things: the float is open → jump into
---it; closed but the slot has a document → reopen it with the cursor
---where it was left; never used → render the current buffer. render_*
---always load afresh, cursor at the top.
---
---A buffer-backed slot (render_current) re-renders, debounced, on the
---buffer's TextChanged/TextChangedI; a file-backed one (render_path, a
---restored pin) on auto-core's `core.file:modified` for its path.
---
---Inside a float: `q` / `<Esc>` close it, `gx` opens the link or image
---under the cursor (a Markdown file opens in the same slot, a `#heading`
---jumps, anything else goes to the system opener), `B` opens the
---document in the browser, Mermaid drawn.
---
---Default keys. The plugin maps none unless `setup({ keys = true })`;
---autovim's autodoc spec defines them:
---
---  <leader>m1 m2 m3 ma ms md   focus / open the slot (cursor restored)
---  <leader>m! m@ m# mA mS mD   render the current buffer into 1 2 3 a s d
---  <leader>mf                  find a Markdown file under cwd → pick a slot
---  <leader>mc                  close every float (pins and cursors kept)
---
---User commands (setup() defines them unless `commands = false`):
---:AutodocPreviewFocus {slot}, :AutodocPreviewRender {slot},
---:AutodocPreviewRenderPath {slot} {path}, :AutodocPreviewFind,
---:AutodocPreviewCloseAll, :AutodocPreviewBrowser [slot].
---
---Mailbox verbs (owner "autodoc"): preview.attach {slot, path},
---preview.view {slot}, preview.browser {slot?}. Topics published:
---`doc:pinned` {slot, path, source_bufnr} when a slot opens a document,
---`doc:unpinned` {slot, path} when a slot loses it (a worktree switch,
---or its unnamed buffer is wiped).
---
---Every subscription is made in setup(), never at module load, and the
---per-slot ones are removed when the slot's float closes.
---@module 'autodoc.preview'

local config  = require("autodoc.preview.config")
local layout  = require("autodoc.preview.layout")
local render  = require("autodoc.preview.render")
local pins    = require("autodoc.preview.pins")
local browser = require("autodoc.preview.browser")

local M = {}

M.SLOTS = layout.SLOTS

M.ns = vim.api.nvim_create_namespace("autodoc.preview")

---@class AutodocPreviewSlot
---@field kind "buffer"|"file"|nil
---@field bufnr integer?   source buffer (buffer-backed)
---@field path string?     absolute, normalized source path
---@field cursor integer[]?
---@field win integer?     the float
---@field buf integer?     the float's scratch buffer
---@field content AutodocPreviewContent?
---@field timer uv.uv_timer_t?
---@field group integer?   the slot's autocmd group while its float is open

---@type table<string, AutodocPreviewSlot>
local slots = {}
local configured = false
local subs = {}
local group

local function warn(msg)
  vim.notify("autodoc.preview: " .. msg, vim.log.levels.WARN)
end

local function core()
  local ok, c = pcall(require, "auto-core")
  if ok and type(c) == "table" then return c end
  return nil
end

local function publish(topic, payload)
  local c = core()
  if c and c.events then pcall(c.events.publish, topic, payload) end
end

local function norm(path)
  return vim.fs.normalize(vim.fn.fnamemodify(vim.fn.expand(path), ":p"))
end

local function is_markdown_path(p)
  return p:match("%.md$") ~= nil or p:match("%.markdown$") ~= nil or p:match("%.mdx$") ~= nil
end

local function is_markdown_buf(b)
  if vim.bo[b].filetype == "markdown" or vim.bo[b].filetype == "markdown.mdx" then return true end
  return is_markdown_path(vim.api.nvim_buf_get_name(b))
end

local function buf_path(b)
  local name = vim.api.nvim_buf_get_name(b)
  return name ~= "" and norm(name) or nil
end

local function visible(s)
  return s and s.win and vim.api.nvim_win_is_valid(s.win) and s.buf and vim.api.nvim_buf_is_valid(s.buf)
end

local function ensure_setup()
  if not configured then M.setup() end
end

---The slot's state, seeded from the project's pin on first use.
---@param slot string
---@return AutodocPreviewSlot
local function slot_state(slot)
  assert(layout.valid(slot), "autodoc.preview: unknown slot " .. tostring(slot))
  local s = slots[slot]
  if not s then
    s = {}
    local ok, pin = pcall(pins.get, slot)
    if ok and pin and type(pin.path) == "string" then
      s.kind, s.path, s.cursor = "file", pin.path, pin.cursor
    end
    slots[slot] = s
  end
  return s
end

---The source's current lines: a buffer-backed slot reads its buffer, a
---file-backed one the file on disk.
---@param s AutodocPreviewSlot
---@return string[]?
local function source_lines(s)
  if s.kind == "buffer" and s.bufnr and vim.api.nvim_buf_is_valid(s.bufnr) then
    return vim.api.nvim_buf_get_lines(s.bufnr, 0, -1, false)
  end
  if s.path and vim.fn.filereadable(s.path) == 1 then
    return vim.fn.readfile(s.path)
  end
  return nil
end

local function base_dir(s)
  if s.path then return vim.fs.dirname(s.path) end
  return vim.fn.getcwd()
end

local function content_width()
  return layout.max_width() - 4
end

local function stop_timer(s)
  if s.timer then
    s.timer:stop()
    if not s.timer:is_closing() then s.timer:close() end
    s.timer = nil
  end
end

---The float closed: remember the cursor, drop the slot's subscriptions.
local function on_float_closed(slot)
  local s = slots[slot]
  if not s then return end
  if s.win and vim.api.nvim_win_is_valid(s.win) then
    s.cursor = vim.api.nvim_win_get_cursor(s.win)
  end
  if s.path and s.cursor then pcall(pins.set_cursor, slot, s.cursor) end
  stop_timer(s)
  if s.group then
    pcall(vim.api.nvim_del_augroup_by_id, s.group)
    s.group = nil
  end
  s.win, s.buf, s.content = nil, nil, nil
end

local function close_float(slot)
  local s = slots[slot]
  if s and s.win and vim.api.nvim_win_is_valid(s.win) then
    vim.api.nvim_win_close(s.win, true)
  end
  if s and s.win then on_float_closed(slot) end
end

---Re-render a visible slot in place (window, focus and cursor kept).
---@param slot string
function M.refresh(slot)
  local s = slots[slot]
  if not visible(s) then return end
  local lines = source_lines(s)
  if not lines then return end
  local content = render.build(lines, { width = content_width() })
  local cursor = vim.api.nvim_win_get_cursor(s.win)
  render.apply(s.buf, M.ns, content)
  s.content = content
  local row, col, width, height = layout.geometry(slot, #content.lines, render.max_width(content))
  pcall(vim.api.nvim_win_set_config, s.win, { relative = "editor", row = row, col = col, width = width, height = height })
  local r = math.max(1, math.min(cursor[1], #content.lines))
  pcall(vim.api.nvim_win_set_cursor, s.win, { r, cursor[2] })
end

local function schedule_refresh(slot)
  local s = slots[slot]
  if not visible(s) then return end
  s.timer = s.timer or vim.uv.new_timer()
  s.timer:stop()
  s.timer:start(config.values.debounce_ms, 0, vim.schedule_wrap(function()
    M.refresh(slot)
  end))
end

-- ── links ──────────────────────────────────────────────────────

local function jump_heading(slot, frag)
  local s = slots[slot]
  if not visible(s) then return false end
  frag = frag:lower()
  for _, h in ipairs(s.content.headings) do
    if h.slug == frag then
      vim.api.nvim_win_set_cursor(s.win, { h.row + 1, 0 })
      return true
    end
  end
  return false
end

local function url_decode(p)
  return (p:gsub("%%(%x%x)", function(h) return string.char(tonumber(h, 16)) end))
end

---Open a link or image target found in `slot`'s document.
---@param slot string
---@param t AutodocPreviewTarget
function M.open_target(slot, t)
  local s = slot_state(slot)
  local target = t.target
  if target:sub(1, 1) == "#" then
    if not jump_heading(slot, target:sub(2)) then warn("no heading #" .. target:sub(2)) end
    return
  end
  if target:match("^%a[%w+.%-]+:") then
    return config.open(target)
  end
  local path, frag = target:match("^([^#]*)#(.*)$")
  path = url_decode(path or target)
  local abs
  if path:sub(1, 1) == "/" or path:sub(1, 1) == "~" then
    abs = norm(path)
  else
    abs = vim.fs.normalize(vim.fs.joinpath(base_dir(s), path))
  end
  if t.kind ~= "image" and is_markdown_path(abs) and vim.fn.filereadable(abs) == 1 then
    M.render_path(slot, abs)
    if frag and frag ~= "" then jump_heading(slot, frag) end
    return
  end
  return config.open(abs)
end

---The target under the cursor in `slot`'s float (or the line's only one).
---@param slot string
---@return AutodocPreviewTarget?
function M.target_at_cursor(slot)
  local s = slots[slot]
  if not visible(s) then return nil end
  local r, c = unpack(vim.api.nvim_win_get_cursor(s.win))
  local list = s.content.targets[r - 1]
  if not list or #list == 0 then return nil end
  for _, t in ipairs(list) do
    if c >= t.s and c < t.e then return t end
  end
  return list[1]
end

local function follow(slot)
  local t = M.target_at_cursor(slot)
  if not t then return warn("no link under the cursor") end
  M.open_target(slot, t)
end

-- ── the float ──────────────────────────────────────────────────

local function float_keymaps(slot, buf)
  local function map(lhs, fn, desc)
    vim.keymap.set("n", lhs, fn, { buffer = buf, nowait = true, silent = true, desc = desc })
  end
  map("q", function() close_float(slot) end, "close the preview")
  map("<Esc>", function() close_float(slot) end, "close the preview")
  map("gx", function() follow(slot) end, "open the link / image under the cursor")
  map("B", function() M.browser(slot) end, "open in the browser")
end

---Open `slot` on `source`. `fresh` = a new load: the cursor goes to the top.
---@param slot string
---@param source { kind: "buffer"|"file", bufnr: integer?, path: string? }
---@param fresh boolean
local function open(slot, source, fresh)
  ensure_setup()
  local s = slot_state(slot)
  local new_id = source.path or ("buf:" .. tostring(source.bufnr))
  local prev_id = s.path or (s.bufnr and ("buf:" .. s.bufnr))
  local same = prev_id == new_id
  close_float(slot)
  if fresh or not same then s.cursor = nil end
  s.kind, s.bufnr, s.path = source.kind, source.bufnr, source.path

  local lines = source_lines(s)
  if not lines then return warn("nothing to render for slot " .. slot) end
  local content = render.build(lines, { width = content_width() })

  local buf = vim.api.nvim_create_buf(false, true)
  vim.bo[buf].bufhidden = "wipe"
  vim.bo[buf].swapfile = false
  vim.b[buf].autodoc_preview_slot = slot
  render.apply(buf, M.ns, content)
  vim.bo[buf].modifiable = false
  pcall(vim.treesitter.start, buf, "markdown")

  local row, col, width, height = layout.geometry(slot, #content.lines, render.max_width(content))
  local name = s.path and vim.fs.basename(s.path) or "[No Name]"
  local win = vim.api.nvim_open_win(buf, true, {
    relative = "editor", row = row, col = col, width = width, height = height,
    style = "minimal", border = "rounded",
    title = (" %s — slot %s "):format(name, slot), title_pos = "center",
  })
  local wo = vim.wo[win]
  wo.wrap, wo.linebreak, wo.cursorline = true, true, true
  wo.conceallevel, wo.concealcursor = 2, "nc"
  wo.signcolumn = "auto"
  wo.foldenable = false
  vim.cmd("stopinsert")

  s.win, s.buf, s.content = win, buf, content
  float_keymaps(slot, buf)

  if s.cursor then
    local r = math.max(1, math.min(s.cursor[1], #content.lines))
    pcall(vim.api.nvim_win_set_cursor, win, { r, math.max(0, s.cursor[2] or 0) })
  end

  s.group = vim.api.nvim_create_augroup("autodoc.preview.slot." .. slot, { clear = true })
  vim.api.nvim_create_autocmd("WinLeave", {
    group = s.group, buffer = buf,
    callback = function()
      if vim.api.nvim_win_is_valid(win) then
        s.cursor = vim.api.nvim_win_get_cursor(win)
        if s.path then pcall(pins.set_cursor, slot, s.cursor) end
      end
    end,
  })
  vim.api.nvim_create_autocmd("WinClosed", {
    group = s.group, pattern = tostring(win),
    callback = function() on_float_closed(slot) end,
  })
  if s.kind == "buffer" then
    vim.api.nvim_create_autocmd({ "TextChanged", "TextChangedI" }, {
      group = s.group, buffer = s.bufnr,
      callback = function() schedule_refresh(slot) end,
    })
    vim.api.nvim_create_autocmd("BufWipeout", {
      group = s.group, buffer = s.bufnr,
      callback = function()
        vim.schedule(function() M._source_gone(slot) end)
      end,
    })
  end

  if s.path then pcall(pins.set, slot, { path = s.path, cursor = s.cursor }) end
  publish("doc:pinned", { slot = slot, path = s.path, source_bufnr = s.bufnr })
end

---A buffer-backed slot's buffer was wiped: a named one carries on from
---its file; an unnamed one has nothing left, so the slot is unpinned.
---@param slot string
function M._source_gone(slot)
  local s = slots[slot]
  if not s or s.kind ~= "buffer" then return end
  if s.bufnr and vim.api.nvim_buf_is_valid(s.bufnr) then return end
  if s.path then
    s.kind, s.bufnr = "file", nil
    return
  end
  close_float(slot)
  slots[slot] = {}
  publish("doc:unpinned", { slot = slot, path = nil })
end

-- ── public API ─────────────────────────────────────────────────

---Render the current buffer into `slot`, cursor at the top.
---@param slot string
function M.render_current(slot)
  assert(layout.valid(slot), "autodoc.preview: unknown slot " .. tostring(slot))
  local b = vim.api.nvim_get_current_buf()
  if vim.b[b].autodoc_preview_slot then return warn("this is a preview float, not a document") end
  if not is_markdown_buf(b) then return warn("buffer is not Markdown") end
  open(slot, { kind = "buffer", bufnr = b, path = buf_path(b) }, true)
end

---Render the Markdown file at `path` into `slot` without switching buffers.
---@param slot string
---@param path string
function M.render_path(slot, path)
  assert(layout.valid(slot), "autodoc.preview: unknown slot " .. tostring(slot))
  local p = norm(path)
  if vim.fn.filereadable(p) ~= 1 then return warn("file not readable: " .. p) end
  if not is_markdown_path(p) then return warn("not a Markdown file: " .. p) end
  open(slot, { kind = "file", path = p }, true)
end

---Focus `slot`: jump in, reopen its document, or render the current buffer.
---@param slot string
function M.focus(slot)
  ensure_setup()
  local s = slot_state(slot)
  if visible(s) then
    vim.api.nvim_set_current_win(s.win)
    vim.cmd("stopinsert")
    return
  end
  if s.kind == "buffer" and s.bufnr and vim.api.nvim_buf_is_valid(s.bufnr) then
    return open(slot, { kind = "buffer", bufnr = s.bufnr, path = s.path }, false)
  end
  if s.path and vim.fn.filereadable(s.path) == 1 then
    return open(slot, { kind = "file", path = s.path }, false)
  end
  local b = vim.api.nvim_get_current_buf()
  if vim.b[b].autodoc_preview_slot or not is_markdown_buf(b) then
    return warn("slot " .. slot .. " is empty and the current buffer is not Markdown")
  end
  open(slot, { kind = "buffer", bufnr = b, path = buf_path(b) }, false)
end

---Close every float; the slots keep their documents and cursors.
function M.close_all()
  for _, slot in ipairs(M.SLOTS) do close_float(slot) end
end

---Find a Markdown file under cwd (or opts.cwd), then pick a slot for it.
---@param opts? { cwd?: string }
function M.find(opts)
  ensure_setup()
  require("autodoc.preview.find").find(opts, M.render_path)
end

---Open a document in the browser: `slot`'s, else the focused slot's, else
---the current buffer — its current text, saved or not.
---@param slot string?
---@param opts? { on_done?: fun(ok: boolean, html: string?, argv: string[]?) }
---@return string[]? argv
function M.browser(slot, opts)
  ensure_setup()
  if slot == nil then
    local b = vim.api.nvim_get_current_buf()
    slot = vim.b[b].autodoc_preview_slot
  end
  local source
  if slot then
    local s = slot_state(slot)
    if s.kind == "buffer" and s.bufnr and vim.api.nvim_buf_is_valid(s.bufnr) then
      source = { lines = vim.api.nvim_buf_get_lines(s.bufnr, 0, -1, false), path = s.path, bufnr = s.bufnr }
    elseif s.path and vim.fn.filereadable(s.path) == 1 then
      local loaded = vim.fn.bufnr(s.path)
      local lines = (loaded > 0 and vim.api.nvim_buf_is_loaded(loaded))
        and vim.api.nvim_buf_get_lines(loaded, 0, -1, false) or vim.fn.readfile(s.path)
      source = { lines = lines, path = s.path }
    else
      warn("slot " .. slot .. " has no document")
      return nil
    end
  else
    local b = vim.api.nvim_get_current_buf()
    local name = vim.api.nvim_buf_get_name(b)
    if name ~= "" and not is_markdown_buf(b) then
      warn("buffer is not Markdown")
      return nil
    end
    source = { lines = vim.api.nvim_buf_get_lines(b, 0, -1, false), path = buf_path(b), bufnr = b }
  end
  return browser.open(source, opts)
end

---Define the :AutodocPreview* user commands (idempotent). The main
---`require("autodoc").setup()` may call this; setup() calls it unless
---`commands = false`.
function M.commands()
  require("autodoc.preview.commands").user_commands()
end

-- ── lifecycle ──────────────────────────────────────────────────

local function on_switch(payload)
  for slot, s in pairs(slots) do
    if s.path or s.bufnr then
      publish("doc:unpinned", { slot = slot, path = s.path })
    end
    close_float(slot)
    stop_timer(s)
  end
  slots = {}
  pins.switch(type(payload) == "table" and (payload.to or payload.cwd) or nil)
end

local function on_file_modified(payload)
  if type(payload) ~= "table" or type(payload.path) ~= "string" then return end
  local p = vim.fs.normalize(payload.path)
  for slot, s in pairs(slots) do
    if s.path == p and visible(s) then schedule_refresh(slot) end
  end
end

---Configure the preview and register its subscriptions, mailbox verbs,
---user commands and (with `keys = true`) the default keys. Calling it
---again replaces the previous setup.
---@param opts? table see autodoc.preview.config DEFAULTS
function M.setup(opts)
  if configured then M.teardown() end
  config.set(opts)
  render.highlights.apply()

  local c = core()
  if c and c.events then
    subs[#subs + 1] = c.events.subscribe("worktree:switched", on_switch)
    subs[#subs + 1] = c.events.subscribe("core.file:modified", on_file_modified)
  end

  group = vim.api.nvim_create_augroup("autodoc.preview", { clear = true })
  vim.api.nvim_create_autocmd("VimLeavePre", { group = group, callback = function() browser.cleanup() end })
  vim.api.nvim_create_autocmd("ColorScheme", { group = group, callback = function() render.highlights.apply() end })

  local commands = require("autodoc.preview.commands")
  commands.register_verbs()
  if config.values.commands ~= false then commands.user_commands() end
  if config.values.keys == true then commands.map_keys() end
  configured = true
end

---Undo setup(): close the floats, drop every subscription, verb, command
---and key, and remove the snapshot directory.
function M.teardown()
  for slot in pairs(slots) do close_float(slot) end
  for _, s in pairs(slots) do stop_timer(s) end
  slots = {}
  local c = core()
  for _, h in ipairs(subs) do
    if c and c.events then pcall(c.events.unsubscribe, h) end
  end
  subs = {}
  if group then pcall(vim.api.nvim_del_augroup_by_id, group); group = nil end
  local commands = require("autodoc.preview.commands")
  commands.unregister_verbs()
  commands.remove_user_commands()
  commands.unmap_keys()
  browser.cleanup()
  pins._reset()
  configured = false
end

---Test hook: a slot's live state.
---@param slot string
---@return AutodocPreviewSlot?
function M._slot(slot) return slots[slot] end

return M
