---AutoDoc's RPC client: auto-core's transport, AutoDoc's rules.
---
---`auto-core.rpc` is transport only. Everything that makes a connection AutoDoc's lives here:
---
---  * **the handshake** — a probe first (a hello that declares nothing, which every AutoDoc
---    answers and none refuses), then the declared hello at this plugin's protocol. A daemon
---    serves a range of protocols (`min_protocol` to its own); a plugin outside it is told which
---    side is behind, because "protocol mismatch" alone sends people to the wrong fix.
---  * **what an error means** — the raw msgpack error slot projected to `{code, message}`.
---  * **the verbs this session may call** — `sys.capabilities.verbs`, read once after the hello.
---
---Nothing here blocks the editor: every step is a callback.
---@module 'autodoc.client'

local rpc = require("auto-core.rpc")

local M = {}

-- The protocol this plugin is written for. The daemon serves it while it is within the daemon's
-- range; a verb added after it answers as an unknown method, so a newer daemon is fine.
M.PROTOCOL = 16

-- The name the daemon logs this client by. Not the TUI's: the TUI is held to the daemon's own
-- protocol, this plugin to the range.
M.NAME = "autodoc.nvim"

---@class AutodocClient
local Client = {}
Client.__index = Client

---project_error turns a raw msgpack error slot into `{code, message}`.
---@param err any
---@return { code: integer|nil, message: string }
function M.project_error(err)
  if type(err) == "table" then
    if err.message ~= nil or err.code ~= nil then
      return { code = tonumber(err.code), message = tostring(err.message or "error") }
    end
    if type(err[2]) == "string" then
      return { code = tonumber(err[1]), message = err[2] }
    end
  end
  if type(err) == "string" then
    return { code = nil, message = err }
  end
  return { code = nil, message = vim.inspect(err) }
end

---mismatch says which side is behind, from a probe's answer.
---@param probe table
---@return string|nil
function M.mismatch(probe)
  local own = tonumber(probe.protocol)
  local min = tonumber(probe.min_protocol) or own
  if not own then return nil end
  if M.PROTOCOL > own then
    return string.format("autodoc: the daemon serves protocols %d to %d, this plugin speaks %d — "
      .. "the DAEMON is older. Restart it (it is shared: other clients reconnect).", min, own, M.PROTOCOL)
  end
  if M.PROTOCOL < min then
    return string.format("autodoc: the daemon serves protocols %d to %d, this plugin speaks %d — "
      .. "the PLUGIN is older. Update autodoc.", min, own, M.PROTOCOL)
  end
  return nil
end

---connect dials addr, says hello and reads the session's verbs; `cb(client, err, info)` fires
---once. info carries the probe's answer on a mismatch, so a caller can offer a restart.
---@param opts { addr: string, on_lost: fun(reason: string)?, limits: table? }
---@param cb fun(client: AutodocClient|nil, err: string|nil, info: table|nil)
function M.connect(opts, cb)
  local self = setmetatable({ _addr = opts.addr, _ready = false, _verbs = {} }, Client)
  local conn, cerr = rpc.connect({
    addr = opts.addr,
    mode = "pipe",
    limits = opts.limits,
    on_epoch_lost = function(reason)
      self._ready = false
      if opts.on_lost then opts.on_lost(reason) end
    end,
  })
  if not conn then return cb(nil, "autodoc: " .. tostring(cerr)) end
  self._conn = conn
  conn:request("sys.hello", {}, {}, function(probe)
    if probe.status ~= "ok" or type(probe.value) ~= "table" or probe.value.server ~= "autodoc" then
      conn:close()
      return cb(nil, string.format("autodoc: %s does not answer as AutoDoc (%s)", opts.addr, tostring(probe.status)))
    end
    local err = M.mismatch(probe.value)
    if err then
      conn:close()
      return cb(nil, err, probe.value)
    end
    self:_declare(cb)
  end)
end

---_declare says the declared hello, then reads the verbs this session may call.
function Client:_declare(cb)
  self._conn:request("sys.hello", { { protocol = M.PROTOCOL, name = M.NAME } }, {}, function(outcome)
    if outcome.status ~= "ok" or type(outcome.value) ~= "table" then
      self._conn:close()
      local why = outcome.status == "error" and M.project_error(outcome.error).message or tostring(outcome.status)
      return cb(nil, "autodoc: handshake refused: " .. why)
    end
    self._hello = outcome.value
    self._ready = true
    self:call("sys.capabilities", {}, function(caps, err)
      if type(caps) == "table" and type(caps.verbs) == "table" then
        for _, v in ipairs(caps.verbs) do self._verbs[v] = true end
        self._verbs_known = true
      else
        -- the session works without them: `can` then lets a call through, and the daemon
        -- answers a verb it lacks as an unknown method, rather than this client hiding it
        require("autodoc.log").warn("sys.capabilities failed: " .. (err and err.message or "no verbs in the answer"))
      end
      self._capabilities = caps
      cb(self, nil)
    end)
  end)
end

---call issues a request; `cb(result, err)` with err as `{code, message}`, never a wire value.
---@param method string
---@param params table
---@param cb fun(result: any|nil, err: table|nil)
---@param opts { deadline: integer? }?
function Client:call(method, params, cb, opts)
  if not self:is_ready() then
    return cb(nil, { code = nil, message = "autodoc: not connected" })
  end
  local id, err = self._conn:request(method, params or {}, opts or {}, function(outcome)
    if outcome.status == "ok" then return cb(outcome.value, nil) end
    if outcome.status == "error" then return cb(nil, M.project_error(outcome.error)) end
    cb(nil, { code = nil, message = "autodoc: " .. tostring(outcome.status) })
  end)
  if not id then
    cb(nil, { code = nil, message = "autodoc: request refused (" .. tostring(err) .. ")" })
  end
  return id
end

---can reports whether this session may call method (sys.capabilities' verbs); true for any verb when
---the capabilities could not be read, since the daemon answers a verb it lacks as an unknown method.
function Client:can(method) return not self._verbs_known or self._verbs[method] == true end
function Client:hello() return self._hello end
function Client:capabilities() return self._capabilities end
function Client:instance() return self._hello and self._hello.instance end
function Client:addr() return self._addr end
function Client:is_ready() return self._ready and self._conn ~= nil and not self._conn:is_closed() end

---close ends the session. Idempotent.
function Client:close()
  self._ready = false
  if self._conn then self._conn:close() end
end

return M
