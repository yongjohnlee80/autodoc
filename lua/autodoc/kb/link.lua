---Link the project's primary KB to an AutoDoc workspace, when auto-core knows only its root.
---
---auto-core.kb's first-run import records the primary as `{ root }`, with no workspace, and
---auto-agents exports `$AUTODOC_WORKSPACE` to a spawn only when the record names one. AutoDoc's
---daemon knows its workspaces by name. Until the two meet, an agent resolves the workspace itself, or
---nothing in the KB is indexed at all. This offers to close the gap:
---
---  * a workspace already serves that root: link it (set_primary with its name);
---  * none does: add the root as a workspace under a name the user confirms, then link it.
---
---The question is the confirmation auto-core's set_primary requires. It is asked once per root per
---session, when the workspaces are first listed (`check`), and again on demand (`:AutodocLinkKb`,
---`run`). "Don't ask again" is a preference kept in stdpath("state") per root; `run` asks anyway.
---@module 'autodoc.kb.link'

local M = {}

M.DEFAULT_NAME = "AutoVimKB"
M.STATE_FILE = "autodoc/link-dismissed.json"

local _asked = {} -- roots asked this session

local function notify(msg, level) vim.notify(msg, level or vim.log.levels.INFO, { title = "autodoc" }) end
local function norm(p) return p and vim.fs.normalize(vim.fn.fnamemodify(p, ":p")):gsub("/$", "") or nil end

local function state_path() return vim.fn.stdpath("state") .. "/" .. M.STATE_FILE end

---dismissed is the roots the user said not to ask about again.
---@return table<string, boolean>
function M.dismissed()
  local f = io.open(state_path(), "r")
  if not f then return {} end
  local text = f:read("*a")
  f:close()
  local ok, t = pcall(vim.json.decode, text)
  return ok and type(t) == "table" and type(t.roots) == "table" and t.roots or {}
end

local function dismiss(root)
  local roots = M.dismissed()
  roots[root] = true
  local path = state_path()
  vim.fn.mkdir(vim.fs.dirname(path), "p")
  local tmp = path .. ".tmp"
  local f = io.open(tmp, "w")
  if not f then return notify("autodoc: could not save the preference in " .. path, vim.log.levels.WARN) end
  f:write(vim.json.encode({ roots = roots }))
  f:close()
  os.rename(tmp, path)
end

---default_name is the name offered for a new workspace: AutoVimKB for AutoVim's global KB, the
---project's folder name for a project-local `<project>/.auto-agents/kb`, else the folder's own name.
---@param root string
---@return string
function M.default_name(root)
  root = norm(root)
  if root == norm(vim.fn.stdpath("config") .. "/.auto-agents-config/kb") then return M.DEFAULT_NAME end
  local project = root:match("^(.*)/%.auto%-agents/kb$")
  if project then return vim.fn.fnamemodify(project, ":t") end
  return vim.fn.fnamemodify(root, ":t")
end

---gap is the primary KB record that names no workspace, or nil when there is nothing to link.
---@param primary { workspace: string|nil, root: string|nil }|nil
---@return string|nil root
function M.gap(primary)
  if type(primary) ~= "table" or primary.workspace or type(primary.root) ~= "string" or primary.root == "" then return nil end
  return norm(primary.root)
end

---serving is the listed workspace whose root is root, or nil.
---@param list table[]
---@param root string
---@return table|nil
function M.serving(list, root)
  for _, w in ipairs(list or {}) do
    if type(w.root) == "string" and norm(w.root) == root then return w end
  end
  return nil
end

local function link(name, root, after)
  local ok, err = require("autodoc.api").set_primary({ name = name, root = root }, true)
  if not ok then
    notify("autodoc: linking " .. name .. " as the primary KB failed: " .. tostring(err), vim.log.levels.ERROR)
  else
    notify("autodoc: " .. name .. " is this project's primary KB (" .. root .. "); agents spawned from now get $AUTODOC_WORKSPACE")
  end
  if after then after(ok) end
end

local function ask_name(root, list, after)
  local taken = {}
  for _, w in ipairs(list or {}) do taken[w.name] = true end
  vim.ui.input({ prompt = "Workspace name for " .. root .. ": ", default = M.default_name(root) }, function(name)
    name = name and vim.trim(name) or nil
    if not name or name == "" then return after and after(false) end
    if name:find("/", 1, true) then
      notify("autodoc: a workspace name has no /", vim.log.levels.WARN)
      return after and after(false)
    end
    if taken[name] then
      notify("autodoc: a workspace named " .. name .. " already serves another folder", vim.log.levels.WARN)
      return after and after(false)
    end
    require("autodoc.api").add_location(name, root, function(_, err)
      if err then
        notify("autodoc: adding " .. root .. " as " .. name .. " failed: " .. tostring(err.message), vim.log.levels.ERROR)
        return after and after(false)
      end
      require("autodoc.session").workspaces(function() end)
      link(name, root, after)
    end)
  end)
end

---offer asks about one root, given the listed workspaces. `after(linked)` once it is settled.
---@param root string normalized
---@param list table[]
---@param after fun(linked: boolean)?
function M.offer(root, list, after)
  local w = M.serving(list, root)
  local choices = w
    and { "Link workspace " .. w.name .. " as this project's primary KB", "Not now", "Don't ask again" }
    or { "Add it as an AutoDoc workspace and link it", "Not now", "Don't ask again" }
  local prompt = w
    and ("This project's primary KB (" .. root .. ") names no AutoDoc workspace, and " .. w.name
      .. " serves that folder. Link them, so agents get $AUTODOC_WORKSPACE?")
    or ("This project's primary KB (" .. root .. ") is not an AutoDoc workspace, so it is not indexed or searchable. Add it?")
  vim.ui.select(choices, { prompt = prompt }, function(_, idx)
    if idx == 1 then
      if w then return link(w.name, root, after) end
      return ask_name(root, list, after)
    end
    if idx == 3 then
      dismiss(root)
      notify("autodoc: not asked again for " .. root .. "; :AutodocLinkKb links it later")
    end
    if after then after(false) end
  end)
end

---check is the automatic offer, on a workspace listing: once per root per session, never for a
---dismissed root, and only when the primary names no workspace.
---@param list table[]
function M.check(list)
  local root = M.gap(require("autodoc.session").primary())
  if not root or _asked[root] or M.dismissed()[root] then return end
  _asked[root] = true
  M.offer(root, list)
end

---run is :AutodocLinkKb: the offer on demand, dismissed or not.
function M.run()
  local session = require("autodoc.session")
  local p = session.primary()
  if not p then return notify("autodoc: this project has no primary KB (auto-core.kb)", vim.log.levels.WARN) end
  local root = M.gap(p)
  if not root then return notify("autodoc: the primary KB is linked already: workspace " .. tostring(p.workspace)) end
  session.workspaces(function(list, err)
    if err then return notify("autodoc: " .. tostring(err.message), vim.log.levels.WARN) end
    _asked[root] = true
    M.offer(root, list)
  end)
end

---reset_for_tests forgets which roots were asked this session. Production code never calls this.
function M.reset_for_tests() _asked = {} end

return M
