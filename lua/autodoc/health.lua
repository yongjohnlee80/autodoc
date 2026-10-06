---`:checkhealth autodoc`: auto-core, the binary, the endpoint and a live session.
---@module 'autodoc.health'

local M = {}

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
