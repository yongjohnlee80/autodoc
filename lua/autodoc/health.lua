---`:checkhealth autodoc`: auto-core, the binary, the endpoint and a live session.
---@module 'autodoc.health'

local M = {}

---check_workspaces reports each workspace whose root holds AutoDoc's own folders, or lies inside one
---(workspace.list's autodoc_dirs), as a warning naming them and the fix; none, one ok. The root
---still works: a script granted its files could reach them but for the protected-path list.
---@param h table vim.health, or a stand-in
---@param list table[] workspace.list's answer
function M.check_workspaces(h, list)
  local flagged = 0
  for _, w in ipairs(list or {}) do
    local dirs = {}
    for _, d in ipairs(type(w.autodoc_dirs) == "table" and w.autodoc_dirs or {}) do
      dirs[#dirs + 1] = string.format("%s %s %s", tostring(d.relation), tostring(d.dir), tostring(d.path))
    end
    if #dirs > 0 then
      flagged = flagged + 1
      h.warn(string.format("workspace %s (%s): its root %s AutoDoc's own folders; a script granted its files "
        .. "could reach them but for the protected-path list", tostring(w.name), tostring(w.root), table.concat(dirs, ", ")),
        { "move the root, or narrow it" })
    end
  end
  if flagged == 0 then
    h.ok("no workspace's root holds or lies inside AutoDoc's own folders")
  end
end

function M.check()
  local h = vim.health
  h.start("autodoc")
  if pcall(require, "auto-core.rpc") then
    h.ok("auto-core.nvim is available")
  else
    h.error("auto-core.nvim is missing: AutoDoc's Lua needs it")
    return
  end
  local lifecycle = require("autodoc.lifecycle")
  local session = require("autodoc.session")
  local bin, err, label = lifecycle.resolve_binary(require("autodoc").options().bin)
  if not bin then
    h.error(err)
    return
  end
  h.ok(string.format("binary: %s (%s)", bin, label))
  local function version_of(path)
    local res = vim.system({ path, "--version" }, { text = true }):wait(5000)
    return res and res.code == 0 and vim.trim(res.stdout or "") or nil
  end
  local own = version_of(bin)
  if own then h.info(own) end
  -- one binary per machine: the TUI on PATH restarts the shared daemon as ITS build
  local on_path = require("autodoc.maintenance").path_binary(bin)
  if on_path then
    local theirs = version_of(on_path)
    if own and theirs and theirs ~= own then
      h.warn(string.format("the autodoc on PATH (%s, %s) is not this plugin's (%s): a TUI of another build "
        .. "restarts the shared daemon as itself", on_path, theirs, own), { string.format("ln -sf %s %s", bin, on_path) })
    else
      h.ok(string.format("the autodoc on PATH (%s) is this plugin's build", on_path))
    end
  end
  local done, cerr, client = false, nil, nil
  session.ensure(function(c, e) client, cerr, done = c, e, true end)
  vim.wait(15000, function() return done end, 50)
  if not done then
    h.error("no session within 15 s")
  elseif not client then
    h.error(cerr or "no session")
  else
    local hello = client:hello() or {}
    h.ok(string.format("connected: autodoc %s at %s, protocol %s (the daemon serves %s to %s)",
      tostring(hello.version), client:addr(), tostring(hello.protocol), tostring(hello.min_protocol), tostring(hello.server_protocol)))
    local listed, list, lerr = false, nil, nil
    client:call("workspace.list", {}, function(v, e) list, lerr, listed = v, e, true end)
    vim.wait(5000, function() return listed end, 50)
    if listed and type(list) == "table" then
      M.check_workspaces(h, list)
    else
      h.warn("workspace.list: " .. tostring(lerr or "no answer within 5 s"))
    end
  end
  local kb = package.loaded["auto-core.kb"] or select(2, pcall(require, "auto-core.kb"))
  local primary = type(kb) == "table" and kb.primary and kb.primary() or nil
  if primary then
    h.ok(string.format("primary KB: %s (%s)", tostring(primary.workspace), primary.root))
  else
    h.info("this project has no primary KB yet: choose one in the kb drawer (P)")
  end
end

return M
