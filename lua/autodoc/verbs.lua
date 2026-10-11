---The kb.* verbs AutoDoc registers with auto-core's command registry, so other plugins, and
---agents without a shell, reach the KB through the family's one command surface (`commands_list`
---shows them). Agents with a shell use `autodoc --call` directly, which needs no Neovim.
---
---  kb.info        the project's primary KB, the daemon's endpoint, version and protocols
---  kb.workspaces  the daemon's workspaces
---  kb.search      {query, workspace?, opts?}: search.query on a workspace (the selected KB by default)
---
---kb.set_primary is NOT a verb: the primary KB changes only with the user's confirmation, in the
---drawer. A registry handler answers synchronously, so the two verbs that ask the daemon wait for
---its answer, bounded; a daemon that does not answer in time is an error, never a hang.
---@module 'autodoc.verbs'

local contracts = require("autodoc.contracts")
local session = require("autodoc.session")

local M = {}

M.OWNER = "autodoc"

-- How long a verb waits for the daemon.
M.WAIT_MS = 10000

---await runs fn(done) and waits for done(result, err), up to WAIT_MS.
local function await(fn)
  local result, err, finished = nil, nil, false
  fn(function(r, e) result, err, finished = r, e, true end)
  vim.wait(M.WAIT_MS, function() return finished end, 10)
  if not finished then
    return nil, { message = "autodoc: the daemon did not answer within " .. M.WAIT_MS .. " ms" }
  end
  return result, err
end

-- The registry's type for each contract param type a kb.* verb passes on.
local REGISTRY_TYPES = { string = "string", map = "table", int = "number", bool = "boolean" }

---args_of is a registry schema from a daemon verb's params (autodoc.contracts): each param by name,
---"?" when it is optional or one of the names the registry verb fills in itself.
---@param verb string
---@param defaulted table<string, boolean>
---@return table<string, string>
local function args_of(verb, defaulted)
  local schema = {}
  for _, p in ipairs(contracts.verbs[verb].params) do
    schema[p.name] = (REGISTRY_TYPES[p.type] or "any") .. ((p.optional or defaulted[p.name]) and "?" or "")
  end
  return schema
end

local function fail(err)
  return { ok = false, code = "autodoc_error", error = type(err) == "table" and err.message or tostring(err) }
end

M.SPECS = {
  ["kb.info"] = {
    description = "The project's primary KB, and the AutoDoc daemon's endpoint, version and protocols.",
    handler = function()
      local ok, kb = pcall(require, "auto-core.kb")
      local info = { primary = ok and kb.primary() or nil, connected = session.is_ready() }
      local c = session.client()
      if c and c:is_ready() then
        local h = c:hello() or {}
        info.addr, info.version, info.protocol = c:addr(), h.version, h.protocol
        info.server_protocol, info.min_protocol = h.server_protocol, h.min_protocol
      end
      info.selected = session.selected()
      return info
    end,
  },
  ["kb.workspaces"] = {
    description = "The AutoDoc daemon's workspaces: name, root, state.",
    handler = function()
      local list, err = await(function(done) session.workspaces(done) end)
      if err then return fail(err) end
      return list
    end,
  },
  ["kb.search"] = {
    description = "search.query on a workspace (default: the selected KB, else the project's primary). Args: {query, workspace?, opts?}.",
    -- search.query's params; the workspace defaults to the selected KB
    schema = args_of("search.query", { workspace = true }),
    handler = function(args)
      local ws = args.workspace or await(function(done) session.resolve_selected(done) end)
      if not ws then return fail("no KB selected, and this project has no primary KB") end
      local params = { ws, args.query }
      if type(args.opts) == "table" and next(args.opts) ~= nil then params[3] = args.opts end
      local res, err = await(function(done) session.call("search.query", params, done) end)
      if err then return fail(err) end
      return res
    end,
  },
}

---register adds the verbs to auto-core's registry; a missing auto-core registers nothing.
---@return integer registered
function M.register()
  local ok, commands = pcall(require, "auto-core.mailbox.commands")
  if not ok then return 0 end
  local n = 0
  for name, spec in pairs(M.SPECS) do
    local rok = commands.register(name, { owner = M.OWNER, handler = spec.handler, schema = spec.schema,
      description = spec.description })
    if rok then n = n + 1 end
  end
  return n
end

return M
