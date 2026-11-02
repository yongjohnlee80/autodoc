-- autodoc.registry: AutoDoc's Neovim verbs (kb.*, and the preview's preview.*) register on auto-core's
-- command registry wherever the auto-core beside it keeps it: auto-core.commands from v0.6.0, the
-- file mailbox's auto-core.mailbox.commands before it. With neither they register nothing, and the
-- user is told once per owner, naming the verbs, never silently. Stub registries stand in for each
-- auto-core:
--   [new]     auto-core.commands alone (v0.6.0): every verb lands there
--   [old]     auto-core.mailbox.commands alone (an older auto-core): every verb lands there
--   [both]    both load (an auto-core between the two): the new one wins, the old is untouched
--   [neither] no registry: nothing registered, one warning per owner naming its verbs, not twice
local t = require("helpers")

local KB = { "kb.info", "kb.search", "kb.workspaces" }
local PREVIEW = { "preview.attach", "preview.browser", "preview.view" }

local function stub()
  local r = { names = {} }
  function r.register(name, spec)
    r.names[#r.names + 1] = name
    r[name] = spec
    return true
  end
  function r.get(name) return r[name] end
  function r.unregister(name) r[name] = nil end
  return r
end

local MODS = { "auto-core.commands", "auto-core.mailbox.commands" }

-- world loads the stub registries given (a module name -> stub) and makes the others unloadable
local function world(stubs)
  for _, m in ipairs(MODS) do
    package.loaded[m] = stubs[m]
    package.preload[m] = (not stubs[m]) and function() error(m .. ": not in this auto-core") end or nil
  end
  require("autodoc.registry")._reset_for_tests()
end

local told = {}
local real_notify = vim.notify
vim.notify = function(msg, level) told[#told + 1] = { msg = msg, level = level } end

local verbs = require("autodoc.verbs")
local preview = require("autodoc.preview.commands")

local function sorted(xs) local c = vim.deepcopy(xs); table.sort(c); return c end

local function register_both()
  local n = verbs.register()
  local names = preview.register_verbs()
  preview.unregister_verbs()
  return n, names
end

t.section("[new] auto-core.commands alone", function()
  local new = stub()
  world({ ["auto-core.commands"] = new })
  told = {}
  local n, names = register_both()
  t.eq(n, 3, "the three kb verbs are registered")
  t.eq(names, PREVIEW, "and the three preview verbs")
  t.eq(sorted(new.names), sorted(vim.list_extend(vim.deepcopy(KB), PREVIEW)), "all on auto-core.commands")
  t.ok(new["kb.info"] and new["kb.info"].owner == "autodoc", "owned by autodoc")
  t.eq(#told, 0, "nothing said")
end)

t.section("[old] auto-core.mailbox.commands alone", function()
  local old = stub()
  world({ ["auto-core.mailbox.commands"] = old })
  told = {}
  local n, names = register_both()
  t.eq(n, 3, "the three kb verbs are registered")
  t.eq(names, PREVIEW, "and the three preview verbs")
  t.eq(sorted(old.names), sorted(vim.list_extend(vim.deepcopy(KB), PREVIEW)), "all on the mailbox's registry")
  t.eq(#told, 0, "nothing said")
end)

t.section("[both] the new registry wins", function()
  local new, old = stub(), stub()
  world({ ["auto-core.commands"] = new, ["auto-core.mailbox.commands"] = old })
  told = {}
  register_both()
  t.eq(#new.names, 6, "every verb on auto-core.commands")
  t.eq(#old.names, 0, "none on the old one")
end)

t.section("[neither] nothing registered, said once per owner", function()
  world({})
  told = {}
  local n, names = register_both()
  t.eq(n, 0, "no kb verb registered")
  t.eq(names, {}, "no preview verb registered")
  t.eq(#told, 2, "one warning for autodoc, one for its preview")
  local kb_msg = (told[1] or {}).msg or ""
  local pv_msg = (told[2] or {}).msg or ""
  t.ok((told[1] or {}).level == vim.log.levels.WARN and (told[2] or {}).level == vim.log.levels.WARN, "as warnings")
  t.ok(kb_msg:find("kb.info, kb.search, kb.workspaces", 1, true) ~= nil and kb_msg:find("auto-core.commands", 1, true) ~= nil,
    "autodoc's names its verbs and where it looked", kb_msg)
  t.ok(pv_msg:find("preview.attach, preview.browser, preview.view", 1, true) ~= nil, "the preview's names its verbs", pv_msg)
  register_both()
  t.eq(#told, 2, "a second registration says nothing more")
end)

vim.notify = real_notify
for _, m in ipairs(MODS) do package.loaded[m], package.preload[m] = nil, nil end
t.finish()
