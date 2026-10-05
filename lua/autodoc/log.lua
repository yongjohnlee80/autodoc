---AutoDoc's log: auto-core's, under the component "autodoc", so `:AutoCoreLog` shows the plugin
---beside the rest of the family. A missing auto-core logs nothing rather than failing.
---@module 'autodoc.log'

local M = {}

M.COMPONENT = "autodoc"

local function core()
  local ok, log = pcall(require, "auto-core.log")
  if ok and type(log) == "table" then return log end
end

for _, level in ipairs({ "debug", "info", "warn", "error" }) do
  M[level] = function(...)
    local log = core()
    if log and type(log[level]) == "function" then
      pcall(log[level], M.COMPONENT, ...)
    end
  end
end

return M
