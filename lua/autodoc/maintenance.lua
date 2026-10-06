---`:AutodocMaintenance` (`<leader>mX`), as AutoDB's `<leader>DX`:
---
---  restart   stop the shared daemon and start this plugin's build in its place
---  install   install this plugin's binary again (the release asset for its tag, else make build),
---            then restart the daemon on it
---  versions  the daemon's, this plugin's binary's, and the `autodoc` on PATH (the TUI's), with a
---            warning when PATH's differs: a TUI of another build restarts the daemon as ITS build
---
---The binary does the stopping and starting (`--print-endpoint --restart`); nothing here spawns a
---daemon or signals one.
---@module 'autodoc.maintenance'

local M = {}

M.ACTIONS = { "restart", "install", "versions" }

local function notify(msg, level) vim.notify(msg, level or vim.log.levels.INFO, { title = "autodoc" }) end
local function session() return require("autodoc.session") end
local function lifecycle() return require("autodoc.lifecycle") end
local function bin_opt() return require("autodoc").options().bin end

---path_binary is the `autodoc` on PATH when it is not the same file as bin, or nil.
---@param bin string|nil the binary this plugin uses
---@return string|nil
function M.path_binary(bin)
  local p = vim.fn.exepath(lifecycle().BINARY_NAME)
  if p == "" then return nil end
  local real = vim.uv.fs_realpath(p) or p
  if bin and (vim.uv.fs_realpath(bin) or bin) == real then return nil end
  return p
end

---versions gathers the three versions; `cb(info)` with
---`{ bin, bin_version, path_bin, path_version, daemon = hello|nil, daemon_err }`.
---@param cb fun(info: table)
function M.versions(cb)
  local bin, berr = lifecycle().resolve_binary(bin_opt())
  local info = { bin = bin, bin_err = berr, path_bin = M.path_binary(bin) }
  local pending = 3
  local function done()
    pending = pending - 1
    if pending == 0 then cb(info) end
  end
  if bin then lifecycle().version(bin, function(v) info.bin_version = v; done() end) else done() end
  if info.path_bin then lifecycle().version(info.path_bin, function(v) info.path_version = v; done() end) else done() end
  session().ensure(function(c, err)
    if c then info.daemon = c:hello() else info.daemon_err = err end
    done()
  end)
end

---describe is the versions as lines, the PATH warning among them.
---@param info table
---@return string[] lines, boolean warn
function M.describe(info)
  local lines, warn = {}, false
  if info.daemon then
    local h = info.daemon
    lines[#lines + 1] = string.format("daemon:  autodoc %s, protocol %s (serves %s to %s), pid %s",
      tostring(h.version), tostring(h.protocol), tostring(h.min_protocol), tostring(h.server_protocol), tostring(h.pid))
  else
    lines[#lines + 1] = "daemon:  not connected: " .. tostring(info.daemon_err)
    warn = true
  end
  lines[#lines + 1] = info.bin and string.format("plugin:  autodoc %s at %s", tostring(info.bin_version), info.bin)
    or ("plugin:  " .. tostring(info.bin_err))
  if info.path_bin then
    lines[#lines + 1] = string.format("PATH:    autodoc %s at %s", tostring(info.path_version), info.path_bin)
    if info.bin_version and info.path_version ~= info.bin_version then
      warn = true
      lines[#lines + 1] = string.format("         The TUI on PATH (%s) is not this plugin's build (%s): when the daemon refuses it, "
        .. "it restarts the daemon as itself. Make it this plugin's: ln -sf %s %s", tostring(info.path_version),
        tostring(info.bin_version), tostring(info.bin), info.path_bin)
    end
  end
  return lines, warn
end

---show_versions notifies the versions.
function M.show_versions()
  M.versions(function(info)
    local lines, warn = M.describe(info)
    notify(table.concat(lines, "\n"), warn and vim.log.levels.WARN or vim.log.levels.INFO)
  end)
end

---restart restarts the daemon as this plugin's build.
function M.restart()
  notify("autodoc: restarting the daemon…")
  session().restart(function(_, err)
    if err then notify("autodoc: restart failed: " .. tostring(err), vim.log.levels.ERROR) end
  end)
end

---install installs the binary again, then restarts the daemon on it. It runs in a coroutine of its
---own, so the download and the build never block the editor.
function M.install()
  local co = coroutine.create(function()
    local ok, res, msg = pcall(require("autodoc.install").run, {
      log = function(m) vim.schedule(function() notify("autodoc: " .. m) end) end,
    })
    vim.schedule(function()
      if not ok then return notify("autodoc: install failed: " .. tostring(res), vim.log.levels.ERROR) end
      if not res then return notify("autodoc: " .. msg, vim.log.levels.ERROR) end
      notify("autodoc: " .. msg)
      M.restart()
    end)
  end)
  local ok, err = coroutine.resume(co)
  if not ok then notify("autodoc: install failed: " .. tostring(err), vim.log.levels.ERROR) end
end

local LABELS = {
  restart = "Restart the daemon (as this plugin's build; every client reconnects)",
  install = "Install the binary again (the release for this version, else make build), then restart",
  versions = "Versions: the daemon, this plugin's binary, and the autodoc on PATH",
}

---run does action, or asks which.
---@param action string|nil
function M.run(action)
  if action and action ~= "" then
    if not M[action] or not LABELS[action] then
      return notify("autodoc: no maintenance action " .. action .. " (" .. table.concat(M.ACTIONS, ", ") .. ")", vim.log.levels.WARN)
    end
    if action == "versions" then return M.show_versions() end
    return M[action]()
  end
  vim.ui.select(M.ACTIONS, { prompt = "AutoDoc maintenance", format_item = function(a) return LABELS[a] end }, function(choice)
    if choice then M.run(choice) end
  end)
end

return M
