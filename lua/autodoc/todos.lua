---AutoDoc's todo stores, for Neovim: the daemon's todos.* verbs (protocol 20) as Lua functions.
---
---The daemon owns the todo format, its rules and every write (ADR 1791532020). This module only
---calls it: each function takes the verb's params in order, then `cb(result, err)`, err as
---`{code, message}`. The first param of most is where the store is: a workspace name ("kb"), or
---`{ root = "/abs/project" }` for a project that is no AutoDoc workspace.
---
---  require("autodoc.todos").list("kb", {}, function(listing, err) … end)
---  require("autodoc.todos").update("kb", id, rev, { priority = "high" }, cb)
---
---`autodoc --contracts 'todos.*'` describes every verb: its params, its answer and its errors. Until
---the cutover claims a store, its writes answer -32077 (todos.unclaimed).
---@module 'autodoc.todos'

local contract = require("autodoc.contract")
local session = require("autodoc.session")

local M = {}

-- The verbs, each with its params in order: a trailing "?" marks an optional one. They come from
-- the contracts (autodoc.contracts, generated from core/todo's module): a new verb needs no row here.
M.VERBS = contract.domain("todos")

---call runs todos.<verb> with params as a list, as the wire carries them.
---@param verb string the verb's own name: "list", not "todos.list"
---@param params any[]
---@param cb fun(result: any, err: { code: integer|nil, message: string }|nil)
function M.call(verb, params, cb)
  session.call("todos." .. verb, params, cb)
end

-- through M.call as it is at the call, so a caller that wraps it sees every verb's
contract.bind(M, "autodoc.todos", M.VERBS, function(verb, params, cb) return M.call(verb, params, cb) end)

return M
