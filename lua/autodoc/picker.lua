---`<leader>fk`: search the selected KB (ADR 1791209945 §8).
---
---A snacks.picker custom source when snacks is installed: the query is debounced, then
---`search.query(selected, q, {stages?})` runs live; each hit is an item
---`{ file = root/path, pos = {line_start, 0}, text = breadcrumb · snippet }`, previewed as the file
---at the hit and jumped to through snacks' jump (which AutoVim patches to be winfixbuf-safe). The
---title says how the answer was made (the reply's `stages`: what ran, and why the rest did not).
---`<M-l>` / `<M-s>` / `<M-r>` toggle lexical / semantic / rerank live; a retriever (lexical or
---semantic) always stays on, since a ranker only re-orders what they find. `<M-p>` sends the hit
---to a preview slot through `autodoc.preview` when it is installed.
---
---Without snacks it falls back to vim.ui.input + vim.ui.select, with the same request.
---@module 'autodoc.picker'

local M = {}

-- How long the query waits for the next keystroke before it is sent.
M.DEBOUNCE_MS = 150
M.LIMIT = 50

M.STAGES = { "lexical", "semantic", "rerank" }
local RETRIEVERS = { lexical = true, semantic = true }

-- The stages this Neovim searches with, across pickers: all on is the daemon's auto.
M.enabled = { lexical = true, semantic = true, rerank = true }

local function notify(msg, level) vim.notify(msg, level or vim.log.levels.INFO, { title = "autodoc" }) end

---stages is the request's stages: nil when every stage is on (auto), else the ones on, in order.
---@return string[]|nil
function M.stages()
  local out, all = {}, true
  for _, s in ipairs(M.STAGES) do
    if M.enabled[s] then out[#out + 1] = s else all = false end
  end
  if all then return nil end
  return out
end

---toggle flips a stage, refusing to turn off the last retriever.
---@param stage string
---@return boolean changed, string|nil why
function M.toggle(stage)
  if M.enabled[stage] == nil then return false, "unknown stage " .. tostring(stage) end
  if M.enabled[stage] and RETRIEVERS[stage] then
    local other = stage == "lexical" and "semantic" or "lexical"
    if not M.enabled[other] then
      return false, "a retriever must stay on: a ranker only re-orders what lexical or semantic search finds"
    end
  end
  M.enabled[stage] = not M.enabled[stage]
  return true, nil
end

---request_opts is search.query's options for the current toggles.
function M.request_opts()
  local opts = { limit = M.LIMIT }
  local st = M.stages()
  if st then opts.stages = st end
  return opts
end

---items turns a search.query reply into picker items under root.
---@param result table
---@param root string
---@return table[]
function M.items(result, root)
  local out = {}
  root = vim.fs.normalize(root or "")
  for i, h in ipairs(type(result) == "table" and result.hits or {}) do
    local crumb = h.breadcrumb and h.breadcrumb ~= "" and h.breadcrumb or h.path
    local snippet = tostring(h.snippet or ""):gsub("%s+", " ")
    out[#out + 1] = {
      idx = i,
      score = h.score,
      file = root .. "/" .. h.path,
      path = h.path,
      pos = { tonumber(h.line_start) or 1, 0 },
      end_pos = h.line_end and { tonumber(h.line_end), 0 } or nil,
      text = crumb .. " · " .. vim.trim(snippet),
      hit = h,
    }
  end
  return out
end

---title says how the answer was made: the stages performed, and each skipped one's reason.
---@param result table|nil
---@param ws string|nil
---@return string
function M.title(result, ws)
  local head = "KB" .. (ws and (" " .. ws) or "")
  local st = type(result) == "table" and result.stages or nil
  if type(st) ~= "table" then
    local asked = M.stages() or M.STAGES
    return head .. " · " .. table.concat(asked, " + ")
  end
  local performed = st.performed or {}
  local text = #performed > 0 and table.concat(performed, " + ") or "nothing ran"
  if #performed == 1 then text = performed[1] .. " only" end
  local why = {}
  for _, s in ipairs(M.STAGES) do
    local reason = type(st.skipped) == "table" and st.skipped[s] or nil
    if reason and reason ~= "" then why[#why + 1] = s .. " skipped: " .. reason end
  end
  if #why > 0 then text = text .. ": " .. table.concat(why, "; ") end
  return head .. " · " .. text
end

---root_of finds the workspace's root, listing the workspaces when none were listed yet.
---@param ws string
---@param cb fun(root: string|nil, err: table|nil)
function M.root_of(ws, cb)
  local session = require("autodoc.session")
  local w = session.workspace(ws)
  if w then return cb(w.root, nil) end
  session.workspaces(function(_, err)
    if err then return cb(nil, err) end
    local found = session.workspace(ws)
    if not found then return cb(nil, { message = "autodoc: no workspace named " .. ws }) end
    cb(found.root, nil)
  end)
end

---query runs one search on the selected KB: cb(items, result, err).
---@param q string
---@param cb fun(items: table[]|nil, result: table|nil, err: table|nil)
function M.query(q, cb)
  local api = require("autodoc.api")
  require("autodoc.session").resolve_selected(function(ws)
    if not ws then
      return cb(nil, nil, { message = "autodoc: no KB selected, and this project has no primary KB: s in the kb drawer selects one" })
    end
    M.root_of(ws, function(root, rerr)
      if not root then return cb(nil, nil, rerr) end
      api.search(q, M.request_opts(), function(result, err)
        if err then return cb(nil, nil, err) end
        cb(M.items(result, root), result, nil)
      end, ws)
    end)
  end)
end

---to_preview asks which preview slot the item's file goes into (the preview's own prompt), then
---renders it there, when autodoc.preview is installed. The preview renders a whole file: the hit's
---line is not passed.
---@param item table
function M.to_preview(item)
  local ok, preview = pcall(require, "autodoc.preview")
  if not ok or type(preview) ~= "table" or type(preview.render_path) ~= "function" then
    return notify("autodoc: the preview is not in this build", vim.log.levels.WARN)
  end
  -- the preview's own slot prompt, then render_path(slot, path), as its find does
  local fok, find = pcall(require, "autodoc.preview.find")
  if not fok or type(find.pick_slot) ~= "function" then
    return notify("autodoc: the preview's slot prompt is not in this build", vim.log.levels.WARN)
  end
  local function failed(err) notify("autodoc: preview: " .. tostring(err), vim.log.levels.ERROR) end
  -- the render runs in the prompt's callback, after this returns: it is guarded there too
  local rok, err = pcall(find.pick_slot, item.file, function(slot, path)
    local ok2, err2 = pcall(preview.render_path, slot, path)
    if not ok2 then failed(err2) end
  end)
  if not rok then failed(err) end
end

---open_at opens file at line in an editor window (the fallback's jump), never a panel.
local function open_at(item)
  local target = require("autodoc.views.panel").editor_target_winid()
  if target then vim.api.nvim_set_current_win(target) end
  local ok, err = pcall(vim.cmd, "edit " .. vim.fn.fnameescape(item.file))
  if not ok then return notify("autodoc: " .. tostring(err), vim.log.levels.ERROR) end
  pcall(vim.api.nvim_win_set_cursor, 0, { item.pos[1], 0 })
end
M._open_at = open_at

---fallback is the picker without snacks: ask for the query, then choose a hit.
function M.fallback(opts)
  opts = opts or {}
  vim.ui.input({ prompt = M.title(nil, require("autodoc.api").selected()) .. " › ", default = opts.query }, function(q)
    if not q or vim.trim(q) == "" then return end
    M.query(vim.trim(q), function(items, result, err)
      if err then return notify(err.message or tostring(err), vim.log.levels.WARN) end
      if #items == 0 then return notify("autodoc: no hits for " .. q) end
      vim.ui.select(items, {
        prompt = M.title(result, require("autodoc.api").selected()),
        format_item = function(it) return it.path .. ":" .. it.pos[1] .. "  " .. it.text end,
      }, function(choice)
        if choice then open_at(choice) end
      end)
    end)
  end)
end

---source is the snacks.picker spec (exposed so it can be tested without opening a picker).
function M.source(opts)
  opts = opts or {}
  local last_result = nil
  local function retitle(picker)
    picker.title = M.title(last_result, require("autodoc.api").selected())
    pcall(function() picker:update_titles() end)
  end
  local function toggler(stage)
    return function(picker)
      local changed, why = M.toggle(stage)
      if not changed then return notify("autodoc: " .. why, vim.log.levels.WARN) end
      last_result = nil
      retitle(picker)
      picker:find({ refresh = true })
    end
  end
  return {
    source = "autodoc",
    title = M.title(nil, require("autodoc.api").selected()),
    live = true,
    search = opts.query,
    supports_live = true,
    format = "file",
    preview = "file",
    ---finder: a live search, debounced (a keystroke during the wait aborts this run, so only the
    ---last query is sent), then search.query; the reply's items are given to snacks.
    finder = function(_, ctx)
      local q = vim.trim(ctx.filter.search or "")
      if q == "" then return {} end
      return function(cb)
        local async = ctx.async
        async:sleep(M.DEBOUNCE_MS)
        local items, result, err, done = nil, nil, nil, false
        vim.schedule(function()
          M.query(q, function(i, r, e)
            items, result, err, done = i, r, e, true
            async:resume()
          end)
        end)
        while not done do async:suspend() end
        if err then
          vim.schedule(function() notify(err.message or tostring(err), vim.log.levels.WARN) end)
          return
        end
        last_result = result
        vim.schedule(function() if ctx.picker then retitle(ctx.picker) end end)
        for _, it in ipairs(items or {}) do cb(it) end
      end
    end,
    actions = {
      autodoc_lexical = toggler("lexical"),
      autodoc_semantic = toggler("semantic"),
      autodoc_rerank = toggler("rerank"),
      autodoc_preview = function(picker, item)
        item = item or (picker.current and picker:current())
        if item then M.to_preview(item) end
      end,
    },
    win = {
      input = {
        keys = {
          ["<M-l>"] = { "autodoc_lexical", mode = { "i", "n" }, desc = "toggle lexical" },
          ["<M-s>"] = { "autodoc_semantic", mode = { "i", "n" }, desc = "toggle semantic" },
          ["<M-r>"] = { "autodoc_rerank", mode = { "i", "n" }, desc = "toggle rerank" },
          ["<M-p>"] = { "autodoc_preview", mode = { "i", "n" }, desc = "send to a preview slot" },
        },
      },
    },
    confirm = "jump",
  }
end

---snacks is the snacks module when its picker is available.
function M.snacks()
  local ok, snacks = pcall(require, "snacks")
  if ok and type(snacks) == "table" and snacks.picker and type(snacks.picker.pick) == "function" then
    return snacks
  end
  return nil
end

---open searches the selected KB: snacks when it is there, else the fallback.
---@param opts { query: string? }?
function M.open(opts)
  local snacks = M.snacks()
  if not snacks then return M.fallback(opts) end
  return snacks.picker.pick(M.source(opts))
end

return M
