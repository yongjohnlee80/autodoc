---The one AutoDoc session in this Neovim: the client, its epoch, the workspaces it serves, the
---KB selected for searching, and the save hook.
---
---Every caller goes through `ensure`, which connects once however many ask at the same time:
---ask the binary for the endpoint, dial it, and when nothing answers, ask again with `--ensure` so
---the binary starts the daemon. A dropped connection ends the epoch; `guarded` drops any reply
---from an epoch that is over, so a view never paints an answer from a daemon that is gone.
---@module 'autodoc.session'

local client = require("autodoc.client")
local lifecycle = require("autodoc.lifecycle")
local log = require("autodoc.log")

local M = {}

M.TOPIC_CONNECTED = "autodoc.session:connected"
M.TOPIC_DISCONNECTED = "autodoc.session:disconnected"
M.TOPIC_WORKSPACES = "autodoc.session:workspaces"
M.TOPIC_SELECTED = "autodoc.session:selected"

local _opts = {} -- setup's: bin, config, offer_restart, offer_link
local _client, _epoch = nil, 0
local _waiting = nil -- callbacks queued behind a connect in flight
local _workspaces = nil -- the last workspace.list, by name
local _kb_refreshed = {} -- KB roots whose managed files this session already brought up to date
local _selected = nil -- the workspace selected for searching, by name
local _mismatch = nil -- the last probe that refused this plugin's protocol
local _offered = {} -- the daemon instances a restart was offered for: once each
local _restarted = false -- this session restarted the daemon: a mismatch after that is not offered again

local _registered = false
local function ensure_topics()
  if _registered then return end
  _registered = true
  local ok, events = pcall(require, "auto-core.events")
  if not ok then return end
  pcall(events.register_topics, "autodoc", {
    [M.TOPIC_CONNECTED] = { doc = "A session with the AutoDoc daemon is ready.",
      payload = "{ instance = string, addr = string, version = string, protocol = integer }", publishers = { "autodoc" } },
    [M.TOPIC_DISCONNECTED] = { doc = "The AutoDoc session ended; every request in flight has settled.",
      payload = "{ reason = string }", publishers = { "autodoc" } },
    [M.TOPIC_WORKSPACES] = { doc = "The AutoDoc workspaces were listed again (added, renamed or removed).",
      payload = "{ names = string[] }", publishers = { "autodoc" } },
    [M.TOPIC_SELECTED] = { doc = "The KB selected for searching changed.",
      payload = "{ workspace = string? }", publishers = { "autodoc" } },
  })
end

local function publish(topic, payload)
  ensure_topics()
  local ok, events = pcall(require, "auto-core.events")
  if ok then pcall(events.publish, topic, payload) end
end

---configure keeps setup's options (bin, config) for every connect after.
function M.configure(opts) _opts = vim.deepcopy(opts or {}) end

function M.epoch() return _epoch end
function M.is_ready() return _client ~= nil and _client:is_ready() end
function M.client() return _client end
function M.mismatch() return _mismatch end

local function settle(c, err)
  local queue = _waiting or {}
  _waiting = nil
  for _, cb in ipairs(queue) do pcall(cb, c, err) end
end

local function on_lost(reason, lost)
  -- a session that ended after another took its place ends nothing of the current one
  if lost ~= nil and lost ~= _client then return end
  _epoch = _epoch + 1
  _client = nil
  log.info("session ended: " .. tostring(reason))
  publish(M.TOPIC_DISCONNECTED, { reason = tostring(reason) })
end

local function dial(addr, cb)
  local this = {}
  client.connect({ addr = addr, on_lost = vim.schedule_wrap(function(reason) on_lost(reason, this.c) end) },
    vim.schedule_wrap(function(c, err, info)
      this.c = c
      cb(c, err, info)
    end))
end

---ensure calls back with a ready client, connecting first when there is none. Concurrent callers
---share one connect.
---@param cb fun(c: AutodocClient|nil, err: string|nil)
function M.ensure(cb)
  if M.is_ready() then return cb(_client, nil) end
  if _waiting then
    table.insert(_waiting, cb)
    return
  end
  _waiting = { cb }
  local bin, berr = lifecycle.resolve_binary(_opts.bin)
  if not bin then return settle(nil, berr) end
  local function connected(c, err, info)
    if not c then
      _mismatch = info
      if info then vim.schedule(function() M.offer_restart(info) end) end
      return settle(nil, err)
    end
    _mismatch = nil
    _client = c
    _epoch = _epoch + 1
    local h = c:hello() or {}
    log.info(string.format("connected to autodoc %s at %s (protocol %s)", tostring(h.version), c:addr(), tostring(h.protocol)))
    publish(M.TOPIC_CONNECTED, { instance = h.instance, addr = c:addr(), version = h.version, protocol = h.protocol })
    settle(c, nil)
  end
  lifecycle.endpoint(bin, { config = _opts.config }, function(addr, eerr)
    if not addr then return settle(nil, eerr) end
    dial(addr, function(c, err, info)
      if c or info then return connected(c, err, info) end
      -- nothing answers there: let the binary start the daemon, and dial what it prints
      lifecycle.endpoint(bin, { config = _opts.config, ensure = true }, function(addr2, eerr2)
        if not addr2 then return settle(nil, eerr2) end
        dial(addr2, connected)
      end)
    end)
  end)
end

---call is ensure, then the call; one failure path whether the connect or the call failed.
---@param method string
---@param params table
---@param cb fun(result: any, err: table|nil)
function M.call(method, params, cb)
  M.ensure(function(c, err)
    if not c then return cb(nil, { code = nil, message = err or "autodoc: not connected" }) end
    c:call(method, params, vim.schedule_wrap(cb))
  end)
end

---guarded wraps a callback so it runs only while the epoch it was made in lasts.
---@param cb fun(...)
---@return fun(...)
function M.guarded(cb)
  local at = _epoch
  return function(...)
    if _epoch ~= at then return end
    cb(...)
  end
end

---request is call for views: ensure, then the call, its reply guarded by the epoch the call was
---ISSUED in. Wrapping a callback in `guarded` before `ensure` has connected would capture the
---epoch before the connect and drop the first reply of every new session; this takes the epoch
---after. The reply is dropped (cb never fires) when the session ended while it was in flight;
---a failure to connect still calls back, with the lifecycle's message.
---@param method string
---@param params table
---@param cb fun(result: any, err: table|nil)
function M.request(method, params, cb)
  M.ensure(function(c, err)
    if not c then return cb(nil, { code = nil, message = err or "autodoc: not connected" }) end
    c:call(method, params, vim.schedule_wrap(M.guarded(cb)))
  end)
end

---refresh_kbs asks auto-core to bring each listed KB's managed files (KB_OPERATIONS.md, the schema) up
---to the newest copies AutoDoc provided, once per root per session (auto-core.kb.sync_managed: replaced
---only when the KB's copy is older, nothing created). A workspace is a KB when its root has AGENTS.md.
---@param list table[]
function M.refresh_kbs(list)
  local managed = require("autodoc.kb.managed")
  for _, w in ipairs(list or {}) do
    local root = w.root
    if type(root) == "string" and not _kb_refreshed[root] and vim.fn.filereadable(root .. "/AGENTS.md") == 1 then
      _kb_refreshed[root] = true
      local rok, ok, err, rep = pcall(managed.sync, root)
      if not rok or not ok then
        log.warn("syncing the managed KB files in " .. root .. " failed: " .. tostring(rok and err or ok))
      elseif rep and #rep.updated > 0 then
        log.info("updated " .. table.concat(rep.updated, ", ") .. " in " .. root .. " to this AutoDoc's copy")
      end
    end
  end
end

---remember keeps a workspace.list answer for the save hook and the views, and says so. Each listed KB's
---managed files are brought up to date on the way (refresh_kbs), and a primary KB with no workspace
---named is offered one (opts.offer_link).
---@param list table[]
function M.remember_workspaces(list)
  M.refresh_kbs(list)
  -- a primary KB auto-core knows by its root alone is offered a workspace (autodoc.kb.link)
  if _opts.offer_link then
    vim.schedule(function() require("autodoc.kb.link").check(list) end)
  end
  _workspaces = {}
  local names = {}
  for _, w in ipairs(list or {}) do
    _workspaces[w.name] = w
    names[#names + 1] = w.name
  end
  publish(M.TOPIC_WORKSPACES, { names = names })
end

---workspace is the last listed workspace named name, or nil.
---@param name string
---@return table|nil
function M.workspace(name)
  return _workspaces and _workspaces[name] or nil
end

---cached_workspaces is the names of the last listed workspaces, sorted (for completion).
---@return string[]
function M.cached_workspaces()
  local out = vim.tbl_keys(_workspaces or {})
  table.sort(out)
  return out
end

---workspaces lists the daemon's workspaces, and keeps them for the save hook.
---@param cb fun(list: table[]|nil, err: table|nil)
function M.workspaces(cb)
  M.call("workspace.list", {}, function(list, err)
    if err then return cb(nil, err) end
    M.remember_workspaces(list)
    cb(list, nil)
  end)
end

---primary is the project's primary KB record (auto-core.kb), or nil.
---@return { workspace: string|nil, root: string }|nil
function M.primary()
  local ok, kb = pcall(require, "auto-core.kb")
  return ok and kb.primary() or nil
end

---is_primary is whether workspace w is the project's primary KB p: by name when the record names
---one, else by root, since auto-core's first-run import records the root alone. The one resolver
---the drawer's star and the search's default both use.
---@param w { name: string, root: string? }
---@param p { workspace: string|nil, root: string|nil }|nil
---@return boolean
function M.is_primary(w, p)
  if not p or not w then return false end
  if p.workspace then return p.workspace == w.name end
  return w.root ~= nil and p.root ~= nil and vim.fs.normalize(w.root) == vim.fs.normalize(p.root)
end

---primary_workspace is the name of the listed workspace that is the project's primary KB, or nil
---(no primary, or the workspaces not listed yet).
---@return string|nil
function M.primary_workspace()
  local p = M.primary()
  if not p then return nil end
  if p.workspace then return p.workspace end
  for name, w in pairs(_workspaces or {}) do
    if M.is_primary(w, p) then return name end
  end
  return nil
end

---selected is the workspace selected for searching: the one chosen, else the project's primary.
---@return string|nil
function M.selected()
  return _selected or M.primary_workspace()
end

---resolve_selected is selected, listing the workspaces first when the primary is known by its root
---alone and they were not listed yet. `cb(name)` with nil when nothing is selected or primary.
---@param cb fun(name: string|nil)
function M.resolve_selected(cb)
  local name = M.selected()
  if name or _workspaces or not M.primary() then return cb(name) end
  M.workspaces(function() cb(M.selected()) end)
end

---select chooses the workspace to search, and gives it the embedding queue's next batch, as the
---TUI does on a selection.
---@param name string|nil
function M.select(name)
  _selected = name
  publish(M.TOPIC_SELECTED, { workspace = name })
  if name then M.call("workspace.focus", { name }, function() end) end
end

---workspace_of is the served workspace whose root holds path, and path relative to it.
---@param path string an absolute file path
---@return string|nil name, string|nil rel
function M.workspace_of(path)
  if not _workspaces then return nil end
  local best, rel
  for name, w in pairs(_workspaces) do
    local root = w.root and vim.fs.normalize(w.root) or nil
    if root and (path == root or vim.startswith(path, root .. "/")) and (not best or #root > #_workspaces[best].root) then
      best, rel = name, path:sub(#root + 2)
    end
  end
  return best, rel
end

---notify is a message from the session, never a dialog.
local function notify(msg, level) vim.notify(msg, level or vim.log.levels.INFO, { title = "autodoc" }) end

---restart stops the daemon serving the store and starts this plugin's build in its place, through
---the binary (`--print-endpoint --restart`), then connects. Other clients reconnect on their own.
---`cb(client, err)` once.
---@param cb fun(c: AutodocClient|nil, err: string|nil)
function M.restart(cb)
  local bin, berr = lifecycle.resolve_binary(_opts.bin)
  if not bin then return cb(nil, berr) end
  if _client then
    local c = _client
    on_lost("restart", c)
    c:close()
  end
  _restarted = true
  lifecycle.endpoint(bin, { config = _opts.config, restart = true }, function(addr, err)
    if not addr then return cb(nil, err) end
    M.ensure(function(c, eerr)
      if c then
        _restarted = false -- it answers at this plugin's protocol: a later mismatch is a new one
        local h = c:hello() or {}
        notify(string.format("autodoc: restarted the daemon: autodoc %s, protocol %s", tostring(h.version), tostring(h.protocol)))
      end
      cb(c, eerr)
    end)
  end)
end

---older_daemon is whether a refusing probe came from a daemon older than this plugin: the only
---mismatch a restart as this plugin's build mends. A newer daemon refuses an older plugin, which
---an update mends; restarting it would start the older build over it.
---@param info table the probe's answer
---@return boolean
function M.older_daemon(info)
  local own = type(info) == "table" and tonumber(info.protocol) or nil
  return own ~= nil and client.PROTOCOL > own
end

---offer_restart asks, once per daemon instance, whether to restart an older daemon as this
---plugin's build (ADR 1791209945 §3.1); other clients share it, hence the question. Not after
---this session restarted it already: a daemon that still refuses then is the binary's fault, and
---offering again would loop.
---@param info table the probe's answer
function M.offer_restart(info)
  if _opts.offer_restart == false or not M.older_daemon(info) then return end
  local key = tostring(info.instance or "") .. "|" .. tostring(info.version) .. "|" .. tostring(info.pid)
  if _offered[key] then return end
  _offered[key] = true
  if _restarted then
    return notify(string.format("autodoc: the daemon this plugin started (autodoc %s, protocol %s) still speaks an older protocol "
      .. "than this plugin (%d): its binary is older. Update it (:Lazy build autodoc), or set opts.bin to a current one.",
      tostring(info.version), tostring(info.protocol), client.PROTOCOL), vim.log.levels.ERROR)
  end
  local bin = lifecycle.resolve_binary(_opts.bin)
  local function ask(build)
    local q = string.format("The AutoDoc daemon (autodoc %s, protocol %s) is older than this plugin (protocol %d). "
      .. "Restart it as %s? It is shared: every client reconnects.", tostring(info.version), tostring(info.protocol),
      client.PROTOCOL, build and ("autodoc " .. build) or "this plugin's build")
    vim.ui.select({ "Restart the daemon", "Not now" }, { prompt = q }, function(_, idx)
      if idx ~= 1 then
        return notify("autodoc: not restarted; <leader>mX (:AutodocMaintenance) restarts it later", vim.log.levels.WARN)
      end
      M.restart(function(_, err)
        if err then notify("autodoc: restart failed: " .. tostring(err), vim.log.levels.ERROR) end
      end)
    end)
  end
  if not bin then return ask(nil) end
  lifecycle.version(bin, function(v) ask(v) end)
end

---on_write nudges the daemon to index a saved file at once (index.reindex queues it): the drawer,
---the picker and agents see the change without waiting on the daemon's watcher. Eventual, not
---immediate: the change becomes searchable within moments.
---@param path string
function M.on_write(path)
  if not M.is_ready() then return end
  local name, rel = M.workspace_of(vim.fs.normalize(path))
  if name and rel and rel ~= "" then
    _client:call("index.reindex", { name, rel }, function() end)
  end
end

-- Test seam: the loss handler, called as a client's on_lost is.
M._on_lost_for_tests = function(reason, lost) on_lost(reason, lost) end

---reset_for_tests clears all state. Production code never calls this.
function M.reset_for_tests()
  if _client then pcall(function() _client:close() end) end
  _client, _epoch, _waiting, _workspaces, _selected, _mismatch = nil, 0, nil, nil, nil, nil
  _kb_refreshed = {}
  _offered, _restarted = {}, false
end

return M
