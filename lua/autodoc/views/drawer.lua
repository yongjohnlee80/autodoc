---autodoc.views.drawer — the `kb` drawer (ADR 1791209945 §7): three sections over the AutoDoc
---daemon, rendered into a scratch buffer that a host (auto-finder's `kb` section, or autodoc's own
---panel) displays.
---
---  AutoDoc 0.x · connected
---  ▼ Knowledge Base (3)
---    ★ global-kb      primary · watching · semantic ready
---      ▶ adrs/                       documents from index.documents, folded client-side
---      README.md
---    ● work-kb        selected · watching · embedding 412/980
---    ○ docs-archive   watching · embedding when opened
---  ▼ Embedding Models (2)
---    ● embeddinggemma  embeddinggemma:300m · in use
---  ▼ Reranker (1)
---    ● bge  BAAI/bge-reranker-v2-m3 · in use · window 50
---    ○ none   search is not re-ranked
---  ▼ Relations (3)          the KB file in the editor's (autodoc.relations, the TUI's SPC l)
---    global-kb · adrs/0203.md
---    superseded by (1)
---      adrs/1791.md
---
---**The Relations section follows the editor.** Entering a KB file's buffer reads what it is
---connected to (graph.links, graph.backlinks, graph.unresolved; graph.neighborhood at depth 2,
---`d`); another file's rows go at once, so they are never shown for this one. <CR> opens a row,
---and the document left behind goes on `<leader>mo`'s history. It follows the KB's change feed as
---the trees do: the cursor is read before the relations, and any change after it (this file's
---save once indexed, a link added in another file, an edit outside Neovim) reads them again.
---
---**The todos drawer's pattern:** a fixed section table, `▼/▶ Header (count)`, collapse state
---persisted in `auto-core.state` namespace `autodoc.ui`, and ONE keymap set whose handlers
---dispatch on the row's kind.
---
---**A pure renderer over autodoc's Lua surface.** Every byte arrives through `autodoc.session`;
---this view never dials a daemon or re-derives which KB is primary (auto-core.kb answers that).
---
---**Instances, not a singleton.** `new(profile)` returns a view with its own buffer, rows, tree
---and timer, and the host registry is its only constructor and disposer (ADR 0078).
---
---**Live without push (§4.1, §6).** The daemon never pushes. While the drawer is VISIBLE a timer
---pulls `sys.events` every POLL_MS (from the hello's `events` cursor) to refresh the sections a
---configuration change touched, `index.changes(ws, cursor)` for every EXPANDED KB to apply adds,
---edits, renames (a delete and an upsert) and removes to its document tree, and `index.status` for
---the progress text. A CursorExpired change feed re-lists through `index.documents`. A hidden
---drawer pulls nothing: the tick checks visibility first and stops its timer.
---
---**Staleness.** Every reply is guarded twice: `session.request` drops a reply from an epoch that
---ended, and a per-node request sequence drops a reply that a newer request for the same node
---superseded (a slow listing must never paint over a fresher one).
---
---No event subscription is made at module load: a view subscribes when it is mounted and releases
---everything on dispose.
---@module 'autodoc.views.drawer'

local host = require("autodoc.views.host")
local log = require("autodoc.log")

local M = {}

-- How often a visible drawer pulls the daemon's feeds.
M.POLL_MS = 2000
-- One page of index.documents / index.changes / sys.events.
M.PAGE = 500

M.STATE_NS = "autodoc.ui"

M.SECTIONS = {
  { key = "kb", title = "Knowledge Base" },
  { key = "embedding", title = "Embedding Models" },
  { key = "ranker", title = "Reranker" },
  { key = "relations", title = "Relations" },
}

local CODE_CURSOR_EXPIRED = -32063

-- The TUI's provider and ranker forms (tui/providers.go, tui/rankers.go): the store's kind, how the
-- form offers it, where it usually is, whether it takes a key, whether it is sent a context window,
-- whether its model is typed.
M.PROVIDER_KINDS = {
  { kind = "ollama", label = "Ollama (local)", base = "http://localhost:11434", key = false, window = true },
  { kind = "ollama-cloud", label = "Ollama Cloud", base = "https://ollama.com", key = true, window = true, key_required = true },
  { kind = "openai", label = "OpenAI-compatible", base = "https://api.openai.com", key = true, window = false },
}
M.RANKER_KINDS = {
  { kind = "tei", label = "TEI", base = "http://127.0.0.1:18080", model = false },
  { kind = "rerank-api", label = "Rerank API", base = "https://api.cohere.com/v2", model = true },
}
local DEFAULT_CONTEXT, MIN_CONTEXT, MAX_CONTEXT = 8192, 256, 262144
local MIN_WINDOW, MAX_WINDOW = 10, 100
M.POLICIES = { "always", "when opened", "never" }

local NS = vim.api.nvim_create_namespace("autodoc.drawer.hl")

local HL = {
  header   = "AutodocDrawerHeader",
  section  = "AutodocDrawerSection",
  primary  = "AutodocDrawerPrimary",
  selected = "AutodocDrawerSelected",
  item     = "AutodocDrawerItem",
  dir      = "AutodocDrawerDir",
  dim      = "AutodocDrawerDim",
  error    = "AutodocDrawerError",
}

local function apply_default_highlights()
  local defs = {
    [HL.header] = "Title", [HL.section] = "Statement", [HL.primary] = "Special",
    [HL.selected] = "Function", [HL.item] = "Normal", [HL.dir] = "Directory",
    [HL.dim] = "Comment", [HL.error] = "ErrorMsg",
  }
  for name, link in pairs(defs) do pcall(vim.api.nvim_set_hl, 0, name, { link = link, default = true }) end
end

-- ─── the session, through one seam ────────────────────────────────

local function session() return require("autodoc.session") end
local function api() return require("autodoc.api") end

local function notify(msg, level)
  vim.notify(msg, level or vim.log.levels.INFO, { title = "autodoc" })
end
local WARN, ERROR = vim.log.levels.WARN, vim.log.levels.ERROR

-- ─── collapse state, persisted (auto-core.state `autodoc.ui`) ───────

local _ui = nil
local function ui_state()
  if _ui then return _ui end
  local ok, state = pcall(require, "auto-core.state")
  if not ok or type(state) ~= "table" or type(state.namespace) ~= "function" then return nil end
  _ui = state.namespace(M.STATE_NS, { persist = "json" })
  return _ui
end

---collapsed reports whether a section is folded.
function M.collapsed(key)
  local s = ui_state()
  local stored = s and s:get("collapsed") or nil
  return type(stored) == "table" and stored[key] == true
end

local function toggle_collapsed(key)
  local s = ui_state()
  if not s then return end
  local stored = vim.deepcopy(s:get("collapsed") or {})
  stored[key] = not (stored[key] == true)
  s:set("collapsed", stored)
end

-- ─── dialogs: vim.ui.input / vim.ui.select / auto-core.ui.modal ────

---prompt_secret reads a key without echoing it (replaceable in tests). cb(nil) on cancel.
function M.prompt_secret(prompt, cb)
  local ok, v = pcall(vim.fn.inputsecret, prompt)
  if not ok then return cb(nil) end
  cb(v)
end

---confirm asks a yes/no question in auto-core's modal. An irreversible one puts the declining
---answer first, with the cursor on it, so a bare <CR> never fires it (auto-core enforces this).
---@param spec { title: string, body: string[]|string, yes: string, no: string?, irreversible: boolean? }
---@param on_yes fun()
---confirm asks a yes or no: on_yes on yes, on_no (when given) on an explicit no. Dismissing the
---prompt is neither: it ends what was asked.
local function confirm(spec, on_yes, on_no)
  local ok, modal = pcall(require, "auto-core.ui.modal")
  if not ok then
    local no = spec.no or "Cancel"
    vim.ui.select({ no, spec.yes }, { prompt = spec.title }, function(choice)
      if choice == spec.yes then on_yes() elseif choice == no and on_no then on_no() end
    end)
    return
  end
  modal.open({
    title = spec.title,
    body = spec.body,
    reversibility = spec.irreversible and "irreversible" or "reversible",
    default = (not spec.irreversible) and false or nil,
    items = {
      { label = spec.yes, value = true, role = "confirm" },
      { label = spec.no or "Cancel", value = false, role = "cancel" },
    },
    on_choice = function(v)
      if v == true then on_yes() elseif v == false and on_no then on_no() end
    end,
  })
end
M._confirm = confirm

---ask runs a chain of prompts, then done(answers), or done(nil) when one is cancelled. A field is
---{ key, prompt, default?, choices?, format?, skip?(answers), validate?(value, answers) -> why?,
---secret? }. An invalid answer is said and asked again, never a reason to drop the whole chain.
local function ask(fields, done)
  local answers, i = {}, 0
  local step
  local function take(f, v)
    if v == nil then return done(nil) end
    if type(v) == "string" and not f.choices then v = vim.trim(v) end
    if f.validate then
      local why = f.validate(v, answers)
      if why then
        notify(why, WARN)
        i = i - 1
        return step()
      end
    end
    answers[f.key] = v
    step()
  end
  step = function()
    i = i + 1
    local f = fields[i]
    if not f then return done(answers) end
    if f.skip and f.skip(answers) then return step() end
    local function resolve(x) if type(x) == "function" then return x(answers) end return x end
    if f.choices then
      vim.ui.select(resolve(f.choices), { prompt = resolve(f.prompt), format_item = f.format }, function(choice)
        take(f, choice)
      end)
    elseif f.secret then
      M.prompt_secret(resolve(f.prompt), function(v) take(f, v) end)
    else
      vim.ui.input({ prompt = resolve(f.prompt), default = resolve(f.default), completion = f.completion },
        function(v) take(f, v) end)
    end
  end
  step()
end
M._ask = ask

local function split_list(s)
  local out = {}
  for part in tostring(s or ""):gmatch("[^,]+") do
    local p = vim.trim(part)
    if p ~= "" then out[#out + 1] = p end
  end
  return out
end

-- ─── the document tree, built client-side from flat paths ───────────

---build_tree folds paths into nested folders: { dirs = { name -> node }, files = { {name, path} } }.
---@param docs table<string, table> path -> document
function M.build_tree(docs)
  local root = { dirs = {}, files = {}, rel = "" }
  for path in pairs(docs) do
    local node = root
    local parts = vim.split(path, "/", { plain = true })
    for k = 1, #parts - 1 do
      local name = parts[k]
      if not node.dirs[name] then
        node.dirs[name] = { dirs = {}, files = {}, rel = node.rel == "" and name or (node.rel .. "/" .. name) }
      end
      node = node.dirs[name]
    end
    node.files[#node.files + 1] = { name = parts[#parts], path = path }
  end
  return root
end

-- ─── status text ─────────────────────────────────────────────────

local FOLLOW = { watch = "watching", poll = "polling", starting = "starting", degraded = "degraded" }

---status_text says what a workspace is doing, from its index.status (nil while unknown).
function M.status_text(ws, st)
  local parts = {}
  if type(st) ~= "table" then return "" end
  if st.error then return "status: " .. tostring(st.error) end
  local f = type(st.following) == "table" and st.following.mode or nil
  if f and f ~= "" then parts[#parts + 1] = FOLLOW[f] or f end
  local e = st.embeddings
  local policy = ws and ws.embedding_policy or "always"
  if type(e) ~= "table" then
    parts[#parts + 1] = "lexical only"
  elseif e.semantic == "ready" then
    parts[#parts + 1] = "semantic ready"
  elseif e.semantic == "switching" then
    parts[#parts + 1] = "switching model"
  elseif e.semantic == "error" then
    parts[#parts + 1] = "semantic error"
  elseif e.semantic == "off" then
    parts[#parts + 1] = "lexical only"
  else
    local texts, pending = tonumber(e.texts) or 0, tonumber(e.pending) or 0
    if e.queue_state == "paused" then
      parts[#parts + 1] = policy == "never" and "not embedded" or "embedding when opened"
    elseif texts > 0 then
      parts[#parts + 1] = string.format("embedding %d/%d", texts - pending, texts)
    end
  end
  return table.concat(parts, " · ")
end

M.HELP = {
  "autodoc drawer — kb",
  "",
  "  Knowledge Base",
  "  <CR>  expand a KB or folder · open a document",
  "  s     select the KB to search (<leader>fk)",
  "  P     make it this project's primary KB (asks first)",
  "  A     add a location (a new folder is created)",
  "  r     rename        e  edit settings",
  "  d     remove the index (never the files; asks first)",
  "",
  "  Embedding Models / Reranker",
  "  <CR>  use it        a  add      e  edit      d  remove",
  "  <CR> on the Reranker's none: search is not re-ranked",
  "  w     the reranker's window",
  "",
  "  Relations (the KB file in the editor)",
  "  <CR>  open it (<leader>mo goes back)",
  "  d     the documents two links away, or not",
  "",
  "  ▼/▶ headers: <CR> folds a section",
  "  R     refresh       ?  this help",
}

-- ─── the view ────────────────────────────────────────────────────

---@class autodoc.DrawerProfile
---@field filetype            string
---@field buf_var             string
---@field buf_var_value       string
---@field buf_name            string
---@field editor_target_winid fun(): integer?

M.DEFAULT_PROFILE = {
  filetype      = "autodoc",
  buf_var       = "autodoc_view",
  buf_var_value = "drawer",
  buf_name      = "autodoc://drawer",
  editor_target_winid = function() return require("autodoc.views.panel").editor_target_winid() end,
}

---new builds a drawer instance bound to one host profile (frozen here).
---@param profile autodoc.DrawerProfile?
function M.new(profile)
  profile = vim.tbl_extend("force", M.DEFAULT_PROFILE, profile or {})

  local st = {
    bufnr = nil,
    rows = {},
    disposed = false,
    conn = { state = "idle" },
    seq = {},           -- node -> the sequence of its newest request
    epoch = nil,        -- the session epoch the data below was read in
    workspaces = nil, ws_err = nil,
    status = {},        -- workspace -> index.status
    providers = nil, prov_err = nil,
    rankers = nil, rank_err = nil,
    expanded = {},      -- workspace -> true
    dirs = {},          -- "ws\0rel" -> true (open folders)
    trees = {},         -- workspace -> { docs, cursor, loading, error }
    events_cursor = nil,
    -- the Relations section: the KB file it is of (nil: none), its rows, and whether at depth 2
    rel = { ref = nil, rows = nil, neighbours = nil, loading = false, err = nil, deep = false, why = nil },
    timer = nil,
    subs = {},
  }

  local rerender -- forward

  ---request issues method for one node; a reply superseded by a newer request for the node, or
  ---arriving after dispose, is dropped. session.request drops one from an ended epoch.
  local function request(node, method, params, apply)
    st.seq[node] = (st.seq[node] or 0) + 1
    local my = st.seq[node]
    session().request(method, params, function(res, err)
      if st.disposed or st.seq[node] ~= my then return end
      apply(res, err)
    end)
  end

  local function find_ws(name)
    for _, w in ipairs(st.workspaces or {}) do
      if w.name == name then return w end
    end
    return session().workspace(name)
  end

  local function primary()
    return api().primary()
  end

  local function is_primary(w, p) return session().is_primary(w, p) end

  -- ─── loading ───────────────────────────────────────────────────

  local function load_status(name)
    request("status:" .. name, "index.status", { name }, function(res, err)
      st.status[name] = err and { error = err.message } or res
      rerender()
    end)
  end

  local load_tree, follow_editor -- forward

  local function load_workspaces()
    request("workspaces", "workspace.list", {}, function(list, err)
      if err then
        st.ws_err = err.message
        return rerender()
      end
      st.ws_err = nil
      st.workspaces = list or {}
      session().remember_workspaces(st.workspaces)
      if not st.rel.ref and not st.rel.why then follow_editor() end -- the KBs are known now
      local names = {}
      for _, w in ipairs(st.workspaces) do
        names[w.name] = true
        load_status(w.name)
      end
      for name in pairs(st.expanded) do
        if not names[name] then
          st.expanded[name], st.trees[name] = nil, nil
        elseif not st.trees[name] then
          load_tree(name)
        end
      end
      rerender()
    end)
  end

  local function load_providers()
    request("providers", "embedding.providers", {}, function(res, err)
      st.providers, st.prov_err = (not err) and res or nil, err and err.message or nil
      rerender()
    end)
  end

  local function load_rankers()
    request("rankers", "ranker.list", {}, function(res, err)
      st.rankers, st.rank_err = (not err) and res or nil, err and err.message or nil
      rerender()
    end)
  end

  local function relations() return require("autodoc.relations") end

  ---load_relations reads what ref is connected to. Another file's rows go at once: until ref's are
  ---read the section has none, never the last file's. The KB's change cursor is read FIRST, so a
  ---change indexed while the relations are read is still seen by the next poll (read twice is
  ---harmless).
  local function load_relations(ref)
    local cur = st.rel
    if cur.ref and cur.ref.ws == ref.ws and cur.ref.rel == ref.rel then
      cur.loading = true
    else
      st.rel = { ref = ref, deep = cur.deep, loading = true }
    end
    local mine = st.rel
    local function req(method, params, cb) request("relations", method, params, cb) end
    req("index.status", { ref.ws }, function(stat, serr)
      if st.rel ~= mine then return end
      mine.cursor = not serr and type(stat) == "table" and stat.cursor or nil
      relations().load(ref.ws, ref.rel, mine.deep, function(rows, n, err)
        if st.rel ~= mine then return end
        mine.loading = false
        if err then
          mine.err = err.message
        else
          mine.rows, mine.neighbours, mine.err = rows, n, nil
        end
        rerender()
      end, req)
    end)
    rerender()
  end

  ---pull_relations is the Relations section's share of a poll: any change in its KB since the
  ---cursor reads the relations again, since a link into this file can come from any file. An
  ---expired cursor, or none (its read failed), reads them again too.
  local function pull_relations()
    local mine = st.rel
    if not mine.ref or mine.loading then return end
    -- the cursor's read failed: read it, and the relations after it, again (a cursor read alone
    -- would miss what changed since the relations were read)
    if mine.cursor == nil then return load_relations(mine.ref) end
    request("relchanges", "index.changes", { mine.ref.ws, mine.cursor, M.PAGE }, function(res, err)
      if st.rel ~= mine or mine.loading then return end
      if err then
        if err.code == CODE_CURSOR_EXPIRED then load_relations(mine.ref) end
        return
      end
      if #(res.changes or {}) > 0 then return load_relations(mine.ref) end
      mine.cursor = res.cursor
    end)
  end

  ---follow points the Relations section at the file in buf: a KB file's relations, or why there
  ---are none. A buffer that is no file (a panel, a picker, help) leaves the section as it is.
  local function follow(buf)
    if not (buf and vim.api.nvim_buf_is_valid(buf)) or vim.bo[buf].buftype ~= "" or vim.api.nvim_buf_get_name(buf) == "" then
      return
    end
    local ref = relations().current(buf)
    if not ref then
      if not session().is_ready() or #session().cached_workspaces() == 0 then return end -- not known yet
      st.seq.relations = (st.seq.relations or 0) + 1 -- a read in flight is of another file
      st.rel = { deep = st.rel.deep, why = "the file in the editor is in no KB AutoDoc serves" }
      return rerender()
    end
    local cur = st.rel
    if cur.ref and cur.ref.ws == ref.ws and cur.ref.rel == ref.rel and (cur.rows or cur.loading) then return end
    load_relations(ref)
  end

  ---follow_editor follows the file of the editor window (the one a <CR> opens into).
  follow_editor = function()
    local w = profile.editor_target_winid and profile.editor_target_winid() or nil
    if w then follow(vim.api.nvim_win_get_buf(w)) end
  end

  ---load_tree lists a KB's documents (sort path, paged until done). The change cursor is read
  ---FIRST, so a change made while the listing pages is applied after it (twice is harmless).
  load_tree = function(name)
    local tree = { docs = {}, loading = true }
    st.trees[name] = tree
    st.seq["changes:" .. name] = (st.seq["changes:" .. name] or 0) + 1 -- a pull in flight is stale now
    request("tree:" .. name, "index.status", { name }, function(stat, err)
      if st.trees[name] ~= tree then return end
      if err then
        tree.loading, tree.error = false, err.message
        return rerender()
      end
      tree.cursor = stat.cursor
      st.status[name] = stat
      local function page(after)
        request("tree:" .. name, "index.documents", { name, { sort = "path", limit = M.PAGE, after = after } },
          function(res, e)
            if st.trees[name] ~= tree then return end
            if e then
              tree.loading, tree.error = false, e.message
              return rerender()
            end
            for _, d in ipairs(res.docs or {}) do tree.docs[d.path] = d end
            if res.more then return page(res.next) end
            tree.loading = false
            rerender()
          end)
      end
      page(0)
    end)
    rerender()
  end

  ---pull_changes applies a KB's change feed to its tree: upsert adds (an edit is already there),
  ---delete removes, a rename is the two. An expired cursor re-lists.
  local function pull_changes(name, tree)
    request("changes:" .. name, "index.changes", { name, tree.cursor, M.PAGE }, function(res, err)
      if st.trees[name] ~= tree then return end
      if err then
        if err.code == CODE_CURSOR_EXPIRED then load_tree(name) end
        return
      end
      local changed = false
      for _, c in ipairs(res.changes or {}) do
        if c.op == "delete" then
          if tree.docs[c.path] then tree.docs[c.path], changed = nil, true end
        elseif not tree.docs[c.path] then
          tree.docs[c.path], changed = { path = c.path, generation = c.generation }, true
        else
          tree.docs[c.path].generation = c.generation
        end
      end
      tree.cursor = res.cursor
      if res.more then pull_changes(name, tree) end
      if changed then rerender() end
    end)
  end

  local function events_head()
    request("events", "sys.events", { -1, 1 }, function(res)
      if type(res) == "table" then st.events_cursor = res.cursor end
    end)
  end

  ---apply_events refreshes what a configuration event touched; a rename or removal also moves or
  ---drops the drawer's own state for that workspace.
  local function apply_events(evs)
    local ws, emb, rk = false, false, false
    for _, e in ipairs(evs or {}) do
      local kind = tostring(e.kind or "")
      if vim.startswith(kind, "workspace.") then
        ws = true
        if kind == "workspace.renamed" and e.workspace and e.detail and e.detail ~= "" then
          st.expanded[e.detail], st.trees[e.detail] = st.expanded[e.workspace], st.trees[e.workspace]
          st.expanded[e.workspace], st.trees[e.workspace] = nil, nil
          if st.trees[e.detail] then load_tree(e.detail) end
        elseif kind == "workspace.removed" and e.workspace then
          st.expanded[e.workspace], st.trees[e.workspace] = nil, nil
        end
      elseif vim.startswith(kind, "embedding.") then
        emb = true
      elseif vim.startswith(kind, "ranker.") then
        rk = true
      elseif vim.startswith(kind, "preference.") then
        emb, rk = true, true
      end
    end
    if ws then load_workspaces() end
    if emb then load_providers() end
    if rk then load_rankers() end
  end

  ---refresh_all (re)reads every section: on mount, on R, and when a new session begins.
  local function refresh_all()
    st.conn = { state = "connecting" }
    session().ensure(function(c, err)
      if st.disposed then return end
      if not c then
        st.conn = { state = "error", err = err }
        return rerender()
      end
      st.conn = { state = "ready" }
      local epoch = session().epoch()
      if st.epoch ~= epoch then
        -- a new session: the event log is followed from its hello, the trees re-listed
        st.epoch = epoch
        st.events_cursor = (c:hello() or {}).events
        for name in pairs(st.expanded) do st.trees[name] = nil end
      end
      load_workspaces()
      load_providers()
      load_rankers()
      for name in pairs(st.expanded) do
        if not st.trees[name] then load_tree(name) end
      end
      if st.rel.ref then load_relations(st.rel.ref) else follow_editor() end
      rerender()
    end)
    rerender()
  end

  -- ─── polling: only while visible ───────────────────────────────

  local function visible()
    return st.bufnr ~= nil and vim.api.nvim_buf_is_valid(st.bufnr) and #vim.fn.win_findbuf(st.bufnr) > 0
  end

  local function stop_polling()
    if st.timer then
      pcall(function() st.timer:stop() end)
      pcall(function() st.timer:close() end)
      st.timer = nil
    end
  end

  ---poll is one tick: hidden stops the timer before anything is asked.
  local function poll()
    if st.disposed or not visible() then return stop_polling() end
    if not session().is_ready() then return end
    if session().epoch() ~= st.epoch then return refresh_all() end
    if st.events_cursor ~= nil then
      request("events", "sys.events", { st.events_cursor, M.PAGE }, function(res, err)
        if err then
          if err.code == CODE_CURSOR_EXPIRED then
            events_head()
            load_workspaces(); load_providers(); load_rankers()
          end
          return
        end
        st.events_cursor = res.cursor
        apply_events(res.events)
      end)
    end
    for name in pairs(st.expanded) do
      local tree = st.trees[name]
      if tree and not tree.loading and tree.cursor then pull_changes(name, tree) end
    end
    pull_relations()
    for _, w in ipairs(st.workspaces or {}) do load_status(w.name) end
  end

  local function start_polling()
    if st.timer or st.disposed or not visible() then return end
    st.timer = vim.uv.new_timer()
    st.timer:start(M.POLL_MS, M.POLL_MS, vim.schedule_wrap(poll))
  end

  -- ─── rendering ─────────────────────────────────────────────────

  local function render()
    local b = st.bufnr
    if not (b and vim.api.nvim_buf_is_valid(b)) then return end
    apply_default_highlights()
    local lines, rows, hls = {}, {}, {}
    local function row(r, text, hl, dim_from)
      lines[#lines + 1] = text
      rows[#rows + 1] = r
      if hl then hls[#hls + 1] = { #lines - 1, 0, -1, hl } end
      if dim_from then hls[#hls + 1] = { #lines - 1, dim_from, -1, HL.dim } end
    end
    local function msg(section, indent, text, hl)
      row({ kind = "message", section = section }, string.rep(" ", indent) .. text, hl or HL.dim)
    end

    -- the header: the daemon's version and connection, or why there is none
    local s = session()
    local c = s.client()
    if st.conn.state == "error" then
      row({ kind = "header" }, "AutoDoc · not connected", HL.error)
      for _, l in ipairs(vim.split(tostring(st.conn.err or "no session"), "\n", { plain = true })) do
        msg(nil, 2, l, HL.dim)
      end
    elseif c and c:is_ready() then
      local h = c:hello() or {}
      row({ kind = "header" }, string.format("AutoDoc %s · connected", tostring(h.version or "?")), HL.header)
    else
      row({ kind = "header" }, "AutoDoc · connecting…", HL.header)
    end

    local p = primary()
    local selected = s.selected()

    for _, sec in ipairs(M.SECTIONS) do
      local folded = M.collapsed(sec.key)
      local count
      if sec.key == "kb" then
        count = st.workspaces and #st.workspaces or nil
      elseif sec.key == "embedding" then
        count = st.providers and #(st.providers.providers or {}) or nil
      elseif sec.key == "relations" then
        count = st.rel.why and 0 or st.rel.neighbours
      else
        count = st.rankers and #(st.rankers.rankers or {}) or nil
      end
      row({ kind = "section", section = sec.key },
        string.format("%s %s (%s)", folded and "▶" or "▼", sec.title, count and tostring(count) or "…"), HL.section)
      if not folded then
        if sec.key == "kb" then
          if st.ws_err then
            msg("kb", 2, st.ws_err, HL.error)
          elseif st.workspaces and #st.workspaces == 0 then
            msg("kb", 2, "(no knowledge bases: A adds a location)")
          end
          for _, w in ipairs(st.workspaces or {}) do
            local prim = is_primary(w, p)
            local sel = selected == w.name
            local mark = prim and "★" or (sel and "●" or "○")
            local tags = {}
            if prim then tags[#tags + 1] = "primary" end
            if sel then tags[#tags + 1] = "selected" end
            local stext = M.status_text(w, st.status[w.name])
            if stext ~= "" then tags[#tags + 1] = stext end
            local label = "  " .. mark .. " " .. w.name
            local text = label .. (#tags > 0 and ("   " .. table.concat(tags, " · ")) or "")
            row({ kind = "workspace", section = "kb", ws = w.name, workspace = w, primary = prim, selected = sel },
              text, prim and HL.primary or (sel and HL.selected or HL.item), #label)
            if st.expanded[w.name] then
              local tree = st.trees[w.name]
              if not tree or (tree.loading and next(tree.docs) == nil) then
                msg("kb", 6, "loading…")
              elseif tree.error then
                msg("kb", 6, tree.error, HL.error)
              elseif next(tree.docs) == nil then
                msg("kb", 6, "(no documents)")
              else
                local function draw(node, depth)
                  local names = vim.tbl_keys(node.dirs)
                  table.sort(names)
                  for _, n in ipairs(names) do
                    local d = node.dirs[n]
                    local id = w.name .. "\0" .. d.rel
                    local open = st.dirs[id] == true
                    row({ kind = "dir", section = "kb", ws = w.name, workspace = w, rel = d.rel, id = id },
                      string.rep("  ", depth) .. (open and "▼ " or "▶ ") .. n .. "/", HL.dir)
                    if open then draw(d, depth + 1) end
                  end
                  table.sort(node.files, function(a, b2) return a.name < b2.name end)
                  for _, f in ipairs(node.files) do
                    row({ kind = "doc", section = "kb", ws = w.name, workspace = w, path = f.path },
                      string.rep("  ", depth) .. "  " .. f.name, HL.item)
                  end
                end
                draw(M.build_tree(tree.docs), 3)
              end
            end
          end
        elseif sec.key == "relations" then
          local rl = st.rel
          if rl.why then
            msg("relations", 2, "(" .. rl.why .. ")")
          elseif not rl.ref then
            msg("relations", 2, "(open a KB file: what it is connected to shows here)")
          else
            local head = "  " .. rl.ref.ws .. " · " .. rl.ref.rel
            row({ kind = "rel_file", section = "relations", ws = rl.ref.ws, path = rl.ref.rel },
              head .. (rl.deep and "   depth 2 · d" or "   d: depth 2"), HL.selected, #head)
            if rl.err then
              msg("relations", 4, rl.err, HL.error)
            elseif not rl.rows then
              msg("relations", 4, "reading…")
            elseif #rl.rows == 0 then
              msg("relations", 4, "(nothing links with it)")
            end
            for _, r in ipairs(rl.rows or {}) do
              if r.kind == "heading" then
                row({ kind = "rel_heading", section = "relations" }, "    " .. r.text, HL.dir)
              elseif r.kind == "unresolved" then
                row({ kind = "rel_unresolved", section = "relations" }, "      " .. r.text, HL.error)
              else
                row({ kind = "rel_doc", section = "relations", ws = r.ws, path = r.path },
                  (r.kind == "beyond" and "        " or "      ") .. r.text, r.kind == "beyond" and HL.dim or HL.item)
              end
            end
          end
        elseif sec.key == "embedding" then
          if st.prov_err then
            msg("embedding", 2, st.prov_err, HL.error)
          elseif st.providers then
            if st.providers.error and st.providers.error ~= "" then
              msg("embedding", 2, st.providers.error, HL.error)
            end
            if #(st.providers.providers or {}) == 0 then
              msg("embedding", 2, "(no embedding models: a adds one)")
            end
            for _, pv in ipairs(st.providers.providers or {}) do
              local use = st.providers.active == pv.name
              local label = "  " .. (use and "●" or "○") .. " " .. pv.name
              local info = { pv.model ~= "" and pv.model or nil, use and "in use" or nil }
              local tail = table.concat(vim.tbl_filter(function(x) return x ~= nil end, info), " · ")
              row({ kind = "provider", section = "embedding", provider = pv, in_use = use },
                label .. (tail ~= "" and ("   " .. tail) or ""), use and HL.selected or HL.item, #label)
            end
          end
        else
          if st.rank_err then
            msg("ranker", 2, st.rank_err, HL.error)
          elseif st.rankers then
            -- ranker.list's supplied is "" when the build supplies none, and "" is true in Lua
            local supplied = st.rankers.supplied ~= nil and st.rankers.supplied ~= ""
            if supplied then msg("ranker", 2, "(this build supplies its ranker)") end
            if st.rankers.error and st.rankers.error ~= "" then msg("ranker", 2, st.rankers.error, HL.error) end
            if #(st.rankers.rankers or {}) == 0 then msg("ranker", 2, "(no rerankers: a adds one)") end
            for _, rk in ipairs(st.rankers.rankers or {}) do
              local use = st.rankers.active == rk.name
              local label = "  " .. (use and "●" or "○") .. " " .. rk.name
              local info = {}
              if rk.model and rk.model ~= "" then info[#info + 1] = rk.model end
              if use then
                info[#info + 1] = "in use"
                info[#info + 1] = "window " .. tostring(st.rankers.window)
              end
              row({ kind = "ranker", section = "ranker", ranker = rk, in_use = use },
                label .. (#info > 0 and ("   " .. table.concat(info, " · ")) or ""), use and HL.selected or HL.item, #label)
            end
            -- the TUI's "Don't use": a choice like the rankers, unless the build fixes its own
            if #(st.rankers.rankers or {}) > 0 and not supplied then
              local none = (st.rankers.active or "") == ""
              local label = "  " .. (none and "●" or "○") .. " none"
              row({ kind = "no_ranker", section = "ranker", in_use = none },
                label .. "   search is not re-ranked", none and HL.selected or HL.item, #label)
            end
          end
        end
      end
    end

    local win = vim.fn.bufwinid(b)
    local cursor = win ~= -1 and vim.api.nvim_win_get_cursor(win) or nil
    vim.bo[b].modifiable = true
    vim.api.nvim_buf_clear_namespace(b, NS, 0, -1)
    vim.api.nvim_buf_set_lines(b, 0, -1, false, lines)
    for _, h in ipairs(hls) do
      local lnum, cs, ce, group = h[1], h[2], h[3], h[4]
      if ce == -1 then
        pcall(vim.api.nvim_buf_set_extmark, b, NS, lnum, cs, { end_line = lnum + 1, end_col = 0, hl_group = group })
      end
    end
    vim.bo[b].modifiable = false
    if cursor and win ~= -1 then
      pcall(vim.api.nvim_win_set_cursor, win, { math.min(cursor[1], math.max(#lines, 1)), cursor[2] })
    end
    st.rows = rows
  end

  rerender = function()
    if st.disposed then return end
    render()
  end

  -- ─── actions ───────────────────────────────────────────────────

  local function row_under_cursor()
    if not (st.bufnr and vim.api.nvim_buf_is_valid(st.bufnr)) then return nil end
    local win = vim.api.nvim_get_current_win()
    if vim.api.nvim_win_get_buf(win) ~= st.bufnr then win = vim.fn.bufwinid(st.bufnr) end
    if win == -1 then return nil end
    return st.rows[vim.api.nvim_win_get_cursor(win)[1]]
  end

  local function failed(what, err)
    notify(string.format("autodoc: %s: %s", what, err and err.message or tostring(err)), ERROR)
  end

  ---open_doc opens a document as an ordinary buffer in an editor window, never the panel's (the
  ---host's editor_target_winid skips panels, floats and winfixbuf windows). With none, a new
  ---split is made, its winfixbuf cleared locally, so :edit cannot bounce off it.
  local function open_doc(r)
    local w = r.workspace or find_ws(r.ws)
    if not (w and w.root) then return end
    local path = vim.fs.normalize(w.root) .. "/" .. r.path
    local target = profile.editor_target_winid and profile.editor_target_winid() or nil
    if not target then
      target = vim.api.nvim_open_win(vim.api.nvim_create_buf(false, true), false, { split = "right", win = -1 })
      pcall(vim.api.nvim_set_option_value, "winfixbuf", false, { scope = "local", win = target })
    end
    vim.api.nvim_set_current_win(target)
    local ok, err = pcall(vim.cmd, "edit " .. vim.fn.fnameescape(path))
    if not ok then notify("autodoc: " .. tostring(err), ERROR) end
  end

  local function toggle_ws(r)
    if st.expanded[r.ws] then
      st.expanded[r.ws], st.trees[r.ws] = nil, nil
      st.seq["tree:" .. r.ws] = (st.seq["tree:" .. r.ws] or 0) + 1
      st.seq["changes:" .. r.ws] = (st.seq["changes:" .. r.ws] or 0) + 1
      rerender()
    else
      st.expanded[r.ws] = true
      load_tree(r.ws)
    end
  end

  local function select_ws(r)
    if not r or not r.ws then return end
    session().select(r.ws)
    notify("autodoc: searching " .. r.ws)
    rerender()
  end

  local function kb_available()
    local ok, kb = pcall(require, "auto-core.kb")
    return ok and type(kb) == "table" and type(kb.set_primary) == "function"
  end

  local function set_primary(r)
    if not (r and r.kind == "workspace") then return end
    if not kb_available() then
      return notify("autodoc: auto-core.kb is unavailable (an auto-core older than 0.3): the primary KB cannot be set here", WARN)
    end
    local w = r.workspace
    confirm({
      title = "Make " .. w.name .. " this project's primary KB?",
      body = {
        "KB:    " .. w.name,
        "root:  " .. tostring(w.root),
        "",
        "Agents spawned from now on use this KB (their KB root and AUTODOC_WORKSPACE).",
        "Agents already running keep the KB they started with.",
        "The todo store is untouched: todos stay where they are.",
        "",
        "Reversible: P on another KB makes that one primary.",
      },
      yes = "Make it primary",
      no = "Cancel",
    }, function()
      local ok, err = api().set_primary(w, true)
      if not ok then return failed("set the primary KB", { message = tostring(err) }) end
      notify("autodoc: " .. w.name .. " is this project's primary KB")
      rerender()
    end)
  end

  ---ask_dir asks for a folder, offering to create one that does not exist. The family has no
  ---picker that creates, so this is it: path completion, then a confirm, then mkdir -p.
  local function ask_dir(cb)
    vim.ui.input({ prompt = "KB folder (a new path is created): ", default = vim.fn.getcwd() .. "/", completion = "dir" },
      function(v)
        if v == nil or vim.trim(v) == "" then return end
        local dir = vim.fs.normalize(vim.fn.fnamemodify(vim.fn.expand(vim.trim(v)), ":p"))
        if #dir > 1 then dir = dir:gsub("/+$", "") end
        if vim.fn.isdirectory(dir) == 1 then return cb(dir) end
        if vim.fn.filereadable(dir) == 1 then
          notify("autodoc: " .. dir .. " is a file, not a folder", WARN)
          return ask_dir(cb)
        end
        confirm({
          title = "Create " .. dir .. "?",
          body = { "The folder does not exist.", "It is created, with any missing parents, empty." },
          yes = "Create it",
          no = "Cancel",
        }, function()
          if vim.fn.mkdir(dir, "p") == 0 and vim.fn.isdirectory(dir) == 0 then
            return failed("create " .. dir, { message = "mkdir failed" })
          end
          cb(dir)
        end)
      end)
  end

  local function offer_primary(w)
    if not kb_available() then return end
    set_primary({ kind = "workspace", workspace = w, ws = w.name })
  end

  local function offer_scaffold(w, after)
    if vim.fn.filereadable(w.root .. "/AGENTS.md") == 1 then return after() end
    local ok, scaffold = pcall(require, "autodoc.kb.scaffold")
    if not ok or type(scaffold) ~= "table" or type(scaffold.scaffold) ~= "function" then
      notify("autodoc: " .. w.root .. " is not a KB yet (no AGENTS.md); the KB scaffold is not in this build", WARN)
      return after()
    end
    confirm({
      title = "Scaffold a KB in " .. w.root .. "?",
      body = { "The folder has no AGENTS.md, so it is not a KB yet.",
        "The scaffold writes the KB layout (ADR 1791209946 §1): AGENTS.md and its folders.",
        "Nothing already there is overwritten." },
      yes = "Scaffold it",
      no = "Not now",
    }, function()
      local sok, res, serr = pcall(scaffold.scaffold, w.root, { name = w.name })
      if not sok or res == nil or res == false then
        failed("scaffold " .. w.root, { message = tostring(sok and serr or res) })
      else
        notify("autodoc: scaffolded a KB in " .. w.root)
      end
      after()
    end, after) -- not now: the folder is added as it is, and what follows is still asked
  end

  local function add_location()
    ask_dir(function(dir)
      ask({
        { key = "name", prompt = "Workspace name: ", default = vim.fn.fnamemodify(dir, ":t"),
          validate = function(v) if v == "" or v:find("/", 1, true) then return "autodoc: a name, without a /" end end },
      }, function(a)
        if not a then return end
        session().request("workspace.add", { a.name, dir }, function(w, err)
          if err then return failed("add " .. dir, err) end
          notify("autodoc: added " .. a.name .. " (" .. dir .. ")")
          load_workspaces()
          w = type(w) == "table" and w or { name = a.name, root = dir }
          offer_scaffold(w, function() offer_primary(w) end)
        end)
      end)
    end)
  end

  local function rename_ws(r)
    if not (r and r.kind == "workspace") then return end
    local old = r.ws
    ask({ { key = "name", prompt = "Rename " .. old .. " to: ", default = old,
      validate = function(v) if v == "" or v:find("/", 1, true) then return "autodoc: a name, without a /" end end } },
      function(a)
        if not a or a.name == old then return end
        session().request("workspace.rename", { old, a.name }, function(_, err)
          if err then return failed("rename " .. old, err) end
          st.expanded[a.name], st.trees[a.name] = st.expanded[old], nil
          st.expanded[old], st.trees[old] = nil, nil
          local p = primary()
          if p and p.workspace == old then
            notify("autodoc: renamed " .. old .. " to " .. a.name .. "; the primary KB still names " .. old .. ": P on it updates that", WARN)
          else
            if session().selected() == old then session().select(a.name) end
            notify("autodoc: renamed " .. old .. " to " .. a.name)
          end
          load_workspaces()
        end)
      end)
  end

  local function configure(name, settings, what)
    session().request("workspace.configure", { name, settings }, function(_, err)
      if err then return failed(what, err) end
      notify("autodoc: saved " .. name .. "'s " .. what)
      load_workspaces()
    end)
  end

  local function edit_ws(r)
    if not (r and r.kind == "workspace") then return end
    local w = r.workspace
    local items = { "patterns", "provider", "embedding_policy", "schema" }
    local labels = {
      patterns = "Patterns (include / exclude)",
      provider = "Embedding provider",
      embedding_policy = "Embedding policy",
      schema = "Frontmatter schema file",
    }
    vim.ui.select(items, { prompt = "Edit " .. w.name .. ":", format_item = function(i) return labels[i] end }, function(choice)
      if choice == "patterns" then
        ask({
          { key = "include", prompt = "Include patterns (comma-separated): ", default = table.concat(w.include or {}, ", ") },
          { key = "exclude", prompt = "Exclude patterns (comma-separated): ", default = table.concat(w.exclude or {}, ", ") },
        }, function(a)
          if a then configure(w.name, { include = split_list(a.include), exclude = split_list(a.exclude) }, "patterns") end
        end)
      elseif choice == "provider" then
        local names = { "" }
        for _, pv in ipairs(st.providers and st.providers.providers or {}) do names[#names + 1] = pv.name end
        vim.ui.select(names, { prompt = "Embed " .. w.name .. " with:",
          format_item = function(n) return n == "" and "(the daemon's)" or n end }, function(n)
          if n ~= nil then configure(w.name, { provider = n }, "provider") end
        end)
      elseif choice == "embedding_policy" then
        vim.ui.select(M.POLICIES, { prompt = "Embed " .. w.name .. ":" }, function(pol)
          if pol then configure(w.name, { embedding_policy = pol }, "embedding policy") end
        end)
      elseif choice == "schema" then
        ask({ { key = "schema", prompt = "Schema file (empty removes it): ",
          default = type(w.schema) == "table" and w.schema.path or "" } }, function(a)
          if a then configure(w.name, { schema = a.schema }, "schema") end
        end)
      end
    end)
  end

  local function remove_ws(r)
    if not (r and r.kind == "workspace") then return end
    local w = r.workspace
    confirm({
      title = "Remove " .. w.name .. " from AutoDoc?",
      body = {
        "root:  " .. tostring(w.root),
        "",
        "Its index (documents, sections, vectors) is deleted from AutoDoc's store.",
        "The files in the folder are NOT touched: nothing on disk is deleted.",
        "Adding the folder again indexes (and embeds) it again from scratch.",
      },
      yes = "Remove the index",
      no = "Keep it",
      irreversible = true,
    }, function()
      session().request("workspace.remove", { w.name }, function(_, err)
        if err then return failed("remove " .. w.name, err) end
        st.expanded[w.name], st.trees[w.name] = nil, nil
        if session().selected() == w.name then session().select(nil) end
        local p = primary()
        if p and p.workspace == w.name then
          notify("autodoc: removed " .. w.name .. "'s index; it is still this project's primary KB: P on another KB changes that", WARN)
        else
          notify("autodoc: removed " .. w.name .. "'s index; its files are untouched")
        end
        load_workspaces()
      end)
    end)
  end

  ---provider_form is the TUI's provider form as a prompt chain (tui/providers.go).
  local function provider_form(existing)
    local kinds = vim.deepcopy(M.PROVIDER_KINDS)
    if existing then
      table.sort(kinds, function(a, b) return (a.kind == existing.kind) and not (b.kind == existing.kind) end)
    end
    ask({
      { key = "kind", prompt = existing and ("Kind of " .. existing.name .. ":") or "Kind of provider:",
        choices = kinds, format = function(k) return k.label end },
      { key = "name", prompt = "Name: ", default = existing and existing.name or nil,
        validate = function(v) if v == "" then return "autodoc: a provider needs a name" end end },
      { key = "base_url", prompt = "Base URL: ",
        default = function(a) return (existing and existing.kind == a.kind.kind) and existing.base_url or a.kind.base end },
      { key = "model", prompt = "Model: ", default = existing and existing.model or nil,
        validate = function(v) if v == "" then return "autodoc: a provider needs a model" end end },
      { key = "context", prompt = "Context window (tokens): ",
        skip = function(a) return not a.kind.window end,
        default = function() return tostring(existing and existing.context ~= 0 and existing.context or DEFAULT_CONTEXT) end,
        validate = function(v)
          local n = tonumber(v)
          if v ~= "" and (not n or n ~= math.floor(n) or n < MIN_CONTEXT or n > MAX_CONTEXT) then
            return string.format("autodoc: the context window is a number of tokens (%d to %d), not %q", MIN_CONTEXT, MAX_CONTEXT, v)
          end
        end },
      { key = "key", secret = true,
        prompt = function(a)
          if existing and existing.has_key then return "API key (empty keeps the stored one): " end
          return a.kind.key_required and "API key (required): " or "API key (if the endpoint takes one): "
        end,
        skip = function(a) return not a.kind.key end,
        validate = function(v, a)
          if v == "" and a.kind.key_required and not (existing and existing.has_key) then
            return "autodoc: an Ollama Cloud provider needs its API key"
          end
        end },
    }, function(a)
      if not a then return end
      local spec = { name = a.name, kind = a.kind.kind, base_url = a.base_url, model = a.model }
      if a.kind.window and a.context and a.context ~= "" then spec.context = tonumber(a.context) end
      if not a.kind.key then
        spec.key = "" -- a local Ollama keeps none
      elseif a.key and a.key ~= "" then
        spec.key = a.key
      end
      local method, params = "embedding.add", { spec }
      if existing then method, params = "embedding.update", { existing.name, spec } end
      session().request(method, params, function(_, err)
        if err then return failed("save the provider " .. a.name, err) end
        notify("autodoc: saved the provider " .. a.name)
        load_providers()
      end)
    end)
  end

  ---ranker_form is the TUI's ranker form (tui/rankers.go): a TEI server names its own model.
  local function ranker_form(existing)
    local kinds = vim.deepcopy(M.RANKER_KINDS)
    if existing then
      table.sort(kinds, function(a, b) return (a.kind == existing.kind) and not (b.kind == existing.kind) end)
    end
    ask({
      { key = "kind", prompt = "Kind of reranker:", choices = kinds, format = function(k) return k.label end },
      { key = "name", prompt = "Name: ", default = existing and existing.name or nil,
        validate = function(v) if v == "" then return "autodoc: a reranker needs a name" end end },
      { key = "base_url", prompt = "Base URL: ",
        default = function(a) return (existing and existing.kind == a.kind.kind) and existing.base_url or a.kind.base end },
      { key = "model", prompt = "Model: ", default = existing and existing.model or nil,
        skip = function(a) return not a.kind.model end,
        validate = function(v) if v == "" then return "autodoc: a Rerank API reranker needs a model" end end },
      { key = "key", secret = true,
        prompt = function() return (existing and existing.has_key) and "API key (empty keeps the stored one): " or "API key (if the server takes one): " end },
    }, function(a)
      if not a then return end
      local spec = { name = a.name, kind = a.kind.kind, base_url = a.base_url, model = a.kind.model and a.model or "" }
      if a.key and a.key ~= "" then spec.key = a.key end
      local method, params = "ranker.add", { spec }
      if existing then method, params = "ranker.update", { existing.name, spec } end
      session().request(method, params, function(_, err)
        if err then return failed("save the reranker " .. a.name, err) end
        notify("autodoc: saved the reranker " .. a.name)
        load_rankers()
      end)
    end)
  end

  local function use_model(r)
    local is_provider = r.kind == "provider"
    local name = is_provider and r.provider.name or r.ranker.name
    if r.in_use then return notify("autodoc: " .. name .. " is already in use") end
    notify("autodoc: setting up " .. name .. "…")
    session().request(is_provider and "embedding.use" or "ranker.use", { name }, function(_, err)
      if err then return failed("use " .. name, err) end
      notify("autodoc: " .. name .. " is in use")
      if is_provider then load_providers() else load_rankers() end
      for _, w in ipairs(st.workspaces or {}) do load_status(w.name) end
    end)
  end

  ---stop_ranking uses no ranker (ranker.use ""): the hits stay in recall order.
  local function stop_ranking(r)
    if r.in_use then return notify("autodoc: search is already not re-ranked") end
    session().request("ranker.use", { "" }, function(_, err)
      if err then return failed("stop re-ranking", err) end
      notify("autodoc: search is not re-ranked: the hits are in recall order")
      load_rankers()
    end)
  end

  local function remove_model(r)
    local is_provider = r.kind == "provider"
    local name = is_provider and r.provider.name or r.ranker.name
    confirm({
      title = string.format("Remove the %s %s?", is_provider and "embedding provider" or "reranker", name),
      body = is_provider
          and { "Its key, its usage and its log go.", "If it is in use, semantic search stops until another is used." }
          or { "Its key, its usage and its log go.", "If it is in use, search is no longer re-ranked." },
      yes = "Remove it",
      no = "Keep it",
      irreversible = true,
    }, function()
      session().request(is_provider and "embedding.remove" or "ranker.remove", { name }, function(_, err)
        if err then return failed("remove " .. name, err) end
        notify("autodoc: removed " .. name)
        if is_provider then load_providers() else load_rankers() end
      end)
    end)
  end

  local function ranker_window()
    local current = st.rankers and st.rankers.window or nil
    ask({ { key = "n", prompt = string.format("Rerank the top N candidates (%d to %d): ", MIN_WINDOW, MAX_WINDOW),
      default = current and tostring(current) or nil,
      validate = function(v)
        local n = tonumber(v)
        if not n or n ~= math.floor(n) or n < MIN_WINDOW or n > MAX_WINDOW then
          return string.format("autodoc: the window is %d to %d candidates, not %q", MIN_WINDOW, MAX_WINDOW, v)
        end
      end } }, function(a)
      if not a then return end
      session().request("ranker.window", { tonumber(a.n) }, function(_, err)
        if err then return failed("set the window", err) end
        notify("autodoc: the reranker ranks the top " .. a.n .. " candidates")
        load_rankers()
      end)
    end)
  end


  local function help()
    local ok, float = pcall(require, "auto-core.ui.float")
    if ok and float and float.help_overlay then
      pcall(float.help_overlay, M.HELP, { title = "kb" })
    else
      notify(table.concat(M.HELP, "\n"))
    end
  end

  local actions = {}
  actions["<CR>"] = function(r)
    if not r then return end
    if r.kind == "section" then
      toggle_collapsed(r.section)
      return rerender()
    elseif r.kind == "workspace" then
      return toggle_ws(r)
    elseif r.kind == "dir" then
      st.dirs[r.id] = not st.dirs[r.id] or nil
      return rerender()
    elseif r.kind == "doc" then
      return open_doc(r)
    elseif r.kind == "rel_doc" then
      return relations().open(r.ws, r.path)
    elseif r.kind == "provider" or r.kind == "ranker" then
      return use_model(r)
    elseif r.kind == "no_ranker" then
      return stop_ranking(r)
    end
  end
  actions["s"] = function(r) if r and r.ws then select_ws(r) end end
  actions["P"] = function(r) set_primary(r) end
  actions["A"] = function() add_location() end
  actions["r"] = function(r) rename_ws(r) end
  actions["e"] = function(r)
    if not r then return end
    if r.kind == "workspace" then return edit_ws(r) end
    if r.kind == "provider" then return provider_form(r.provider) end
    if r.kind == "ranker" then return ranker_form(r.ranker) end
  end
  actions["d"] = function(r)
    if not r then return end
    if r.section == "relations" then
      st.rel.deep = not st.rel.deep
      if st.rel.ref then load_relations(st.rel.ref) else rerender() end
      return
    end
    if r.kind == "workspace" then return remove_ws(r) end
    if r.kind == "provider" or r.kind == "ranker" then return remove_model(r) end
  end
  actions["a"] = function(r)
    local sec = r and r.section
    if sec == "embedding" then return provider_form(nil) end
    if sec == "ranker" then return ranker_form(nil) end
    if sec == "kb" then return add_location() end
  end
  actions["w"] = function(r) if r and r.section == "ranker" then ranker_window() end end
  actions["R"] = function() refresh_all() end
  actions["?"] = function() help() end

  local DESC = {
    ["<CR>"] = "toggle / open / use", s = "select the KB to search", P = "make the KB primary",
    A = "add a location", r = "rename the KB", e = "edit", d = "remove · relations: depth 2", a = "add",
    w = "reranker window", R = "refresh", ["?"] = "help",
  }

  local function apply_keymaps(b)
    for lhs, fn in pairs(actions) do
      pcall(vim.keymap.set, "n", lhs, function() fn(row_under_cursor()) end,
        { buffer = b, silent = true, nowait = true, desc = "autodoc drawer: " .. DESC[lhs] })
    end
  end

  local function subscribe()
    local ok, events = pcall(require, "auto-core.events")
    if not ok then return end
    for _, h in pairs(st.subs) do pcall(events.unsubscribe, h) end
    st.subs = {}
    local s = session()
    local function later(fn) return function() vim.schedule(function() if not st.disposed then fn() end end) end end
    st.subs.connected = events.subscribe(s.TOPIC_CONNECTED, later(refresh_all))
    st.subs.disconnected = events.subscribe(s.TOPIC_DISCONNECTED, later(function()
      st.conn = { state = "idle" }
      rerender()
    end))
    st.subs.selected = events.subscribe(s.TOPIC_SELECTED, later(rerender))
    st.subs.primary = events.subscribe("core.kb:primary_changed", later(rerender))
  end

  local function unsubscribe()
    local ok, events = pcall(require, "auto-core.events")
    for _, h in pairs(st.subs) do if ok then pcall(events.unsubscribe, h) end end
    st.subs = {}
  end

  -- ─── the DrawerView contract (ADR 0078 §3.2) ───────────────────

  local view = {}

  function view:get_buffer(_winid)
    if st.bufnr and vim.api.nvim_buf_is_valid(st.bufnr) then
      vim.schedule(start_polling)
      return st.bufnr
    end
    local b = vim.api.nvim_create_buf(false, true)
    vim.bo[b].bufhidden = "hide"
    vim.bo[b].buftype = "nofile"
    vim.bo[b].swapfile = false
    vim.bo[b].filetype = profile.filetype
    vim.b[b][profile.buf_var] = profile.buf_var_value
    pcall(vim.api.nvim_buf_set_name, b, profile.buf_name)
    st.bufnr = b
    apply_keymaps(b)
    local group = vim.api.nvim_create_augroup("autodoc.drawer." .. b, { clear = true })
    vim.api.nvim_create_autocmd("BufWinEnter", { group = group, buffer = b, callback = function()
      vim.schedule(start_polling)
    end })
    vim.api.nvim_create_autocmd("BufWinLeave", { group = group, buffer = b, callback = function()
      vim.schedule(function() if not visible() then stop_polling() end end)
    end })
    -- the Relations section follows the editor's KB file while the drawer is on screen
    vim.api.nvim_create_autocmd("BufEnter", { group = group, callback = function(ev)
      if ev.buf == b or not visible() then return end
      vim.schedule(function() if not st.disposed then follow(ev.buf) end end)
    end })

    subscribe()
    render()
    refresh_all()
    -- the host places the buffer after this returns: start the timer once it is on screen
    vim.schedule(start_polling)
    return b
  end

  function view:bufnr()
    if st.bufnr and vim.api.nvim_buf_is_valid(st.bufnr) then return st.bufnr end
    return nil
  end

  function view:on_focus(_winid, b)
    if not vim.api.nvim_buf_is_valid(b) then return end
    follow_editor() -- the editor may have moved on while the drawer was hidden
    render()
    vim.schedule(start_polling)
  end

  ---dispose is the ONLY teardown, idempotent: the timer, the subscriptions and the buffer go.
  function view:dispose()
    st.disposed = true
    stop_polling()
    unsubscribe()
    if st.bufnr and vim.api.nvim_buf_is_valid(st.bufnr) then
      pcall(vim.api.nvim_del_augroup_by_name, "autodoc.drawer." .. st.bufnr)
      pcall(vim.api.nvim_buf_delete, st.bufnr, { force = true })
    end
    st.bufnr, st.rows = nil, {}
  end

  -- test handles onto this instance
  view._st = st
  view._profile = profile
  view._rows = function() return st.rows end
  view._dispatch = function(key, r) return actions[key](r) end
  view._poll = poll
  view._polling = function() return st.timer ~= nil end
  view._refresh = refresh_all
  view._request = request
  view._load_providers = load_providers
  view._follow = follow
  view._rel = function() return st.rel end
  view._sub_count = function() local n = 0 for _ in pairs(st.subs) do n = n + 1 end return n end

  return view
end

-- ─── host registry re-exports (ADR 0078 §3.3): auto-finder's facade addresses THIS module ───

M.register_host = host.register_host
M.unregister_host = host.unregister_host
M.has_host = host.has_host
M.open = host.open
M.focus = host.focus
M.toggle = host.toggle
M.owner = host.owner
M.mounted_view = host.view

M._HL = HL
M._NS = NS
M._log = log

return M
