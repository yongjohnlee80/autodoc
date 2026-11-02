---autodoc.registry: auto-core's command registry, wherever the auto-core installed beside AutoDoc
---keeps it. From auto-core v0.6.0 it is `auto-core.commands` (the file mailbox and its
---`auto-core.mailbox.commands` are gone); an auto-core before it has only the mailbox's. AutoDoc
---can't pin auto-core's version, so it asks for the new one first, then the old. With neither, its
---verbs aren't registered, and the user is told once, naming them: never silently.
---@module 'autodoc.registry'

local M = {}

-- Where the registry is, newest first.
M.MODULES = { "auto-core.commands", "auto-core.mailbox.commands" }

---get is the registry: the first of M.MODULES that loads and can register, or nil.
---@return table|nil
function M.get()
  for _, name in ipairs(M.MODULES) do
    local ok, reg = pcall(require, name)
    if ok and type(reg) == "table" and type(reg.register) == "function" then return reg end
  end
  return nil
end

local told = {}

---missing tells the user, once per owner, that owner's verbs (names) aren't registered because no
---registry loads.
---@param owner string
---@param names string[]
function M.missing(owner, names)
  if told[owner] then return end
  told[owner] = true
  local sorted = vim.deepcopy(names)
  table.sort(sorted)
  vim.notify(("%s: auto-core's command registry isn't loaded (auto-core.commands, or auto-core.mailbox.commands before auto-core v0.6.0), so these verbs aren't registered: %s")
    :format(owner, table.concat(sorted, ", ")), vim.log.levels.WARN)
end

---Test seam: forget who was told.
function M._reset_for_tests() told = {} end

return M
