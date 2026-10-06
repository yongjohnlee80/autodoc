---AutoDoc's public Lua API: what the drawer, the picker, auto-finder and other plugins call.
---Every function is asynchronous, `cb(result, err)` with err as `{code, message}`.
---@module 'autodoc.api'

local session = require("autodoc.session")

local M = {}

---search runs search.query on a workspace (the selected one when ws is nil).
---@param query string
---@param opts table? search.query's options: limit, stages, tags, paths, facets
---@param cb fun(result: table|nil, err: table|nil)
---@param ws string?
function M.search(query, opts, cb, ws)
  local function run(name)
    if not name then
      return cb(nil, { code = nil, message = "autodoc: no KB selected, and this project has no primary KB" })
    end
    local params = { name, query }
    if opts and next(opts) ~= nil then params[3] = opts end
    session.call("search.query", params, cb)
  end
  if ws then return run(ws) end
  session.resolve_selected(run)
end

---documents runs index.documents: the files with their frontmatter, most recently updated first.
---@param ws string
---@param opts table? index.documents' options
---@param cb fun(result: table|nil, err: table|nil)
function M.documents(ws, opts, cb)
  local params = { ws }
  if opts and next(opts) ~= nil then params[2] = opts end
  session.call("index.documents", params, cb)
end

---workspaces lists the daemon's workspaces.
---@param cb fun(list: table[]|nil, err: table|nil)
function M.workspaces(cb) session.workspaces(cb) end

M.select = session.select
M.selected = session.selected

---primary is the project's primary KB (auto-core.kb), or nil.
---@return { workspace: string|nil, root: string }|nil
function M.primary() return session.primary() end

---set_primary makes a workspace the project's primary KB. It is the caller's job to have asked
---the user first (the drawer's P asks): auto-core refuses without confirmed = true.
---@param ws { name: string, root: string }
---@return boolean ok, string|nil err
function M.set_primary(ws, confirmed)
  local ok, kb = pcall(require, "auto-core.kb")
  if not ok then return false, "auto-core.kb is not available" end
  return kb.set_primary(nil, { workspace = ws.name, root = ws.root }, { confirmed = confirmed == true })
end

---add_location adds a folder as a workspace (workspace.add); the drawer's A wraps it in dialogs.
---@param name string
---@param root string an absolute directory
---@param cb fun(ws: table|nil, err: table|nil)
function M.add_location(name, root, cb)
  session.call("workspace.add", { name, root }, cb)
end

---drawer_open / drawer_toggle / drawer_focus show the kb drawer in whichever host is active
---(auto-finder's kb section, else autodoc's own panel). `cb(ok, value)` as the host registry's.
function M.drawer_open(cb) return require("autodoc.views.host").open(cb) end
function M.drawer_toggle(cb) return require("autodoc.views.host").toggle(cb) end
function M.drawer_focus(cb) return require("autodoc.views.host").focus(cb) end

return M
