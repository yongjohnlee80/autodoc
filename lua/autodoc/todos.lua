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

local session = require("autodoc.session")

local M = {}

-- The verbs, each with its params in order: a trailing "?" marks an optional one. They are the
-- contracts' (core/todo module.go); a new verb is a row here.
M.VERBS = {
  resolve = { "at" },
  open = { "at", "opts?" },
  scaffold = { "at" },
  register = { "root" },
  set_dir = { "at", "dir" },
  stores = {},
  move_store = { "at", "to" },
  list = { "at", "opts?" },
  get = { "at", "id" },
  add = { "at", "spec" },
  update = { "at", "id", "rev", "patch" },
  status = { "at", "id", "rev", "status" },
  assign = { "at", "id", "rev", "assignee", "reason?" },
  remove = { "at", "id", "rev" },
  remove_malformed = { "at", "path" },
  refresh = { "at" },
  changes = { "at", "consumer", "limit?" },
  ack = { "at", "consumer", "seq" },
  claim = { "at", "opts?" },
  release = { "at" },
  vars = { "at" },
  set_var = { "name", "value" },
}

---required is how many of a verb's params a call must give.
---@param params string[]
---@return integer
local function required(params)
  for i, p in ipairs(params) do
    if p:sub(-1) == "?" then return i - 1 end
  end
  return #params
end

---call runs todos.<verb> with params as a list, as the wire carries them.
---@param verb string the verb's own name: "list", not "todos.list"
---@param params any[]
---@param cb fun(result: any, err: { code: integer|nil, message: string }|nil)
function M.call(verb, params, cb)
  session.call("todos." .. verb, params, cb)
end

for verb, params in pairs(M.VERBS) do
  local lo, hi = required(params), #params
  M[verb] = function(...)
    local args = { ... }
    local n = select("#", ...)
    local cb = args[n]
    if type(cb) ~= "function" then
      error(string.format("autodoc.todos.%s: the last argument is the callback", verb), 2)
    end
    n = n - 1
    -- trailing nils are params left out
    while n > 0 and args[n] == nil do n = n - 1 end
    if n < lo or n > hi then
      error(string.format("autodoc.todos.%s takes %d to %d params (%s), then a callback; got %d",
        verb, lo, hi, table.concat(params, ", "), n), 2)
    end
    local list = {}
    for i = 1, n do list[i] = args[i] end
    M.call(verb, list, cb)
  end
end

return M
