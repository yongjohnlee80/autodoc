---autodoc.preview.commands — the preview's mailbox verbs, user commands
---and (opt-in) default keys.
---
---Mailbox verbs, registered in auto-core's command registry under owner
---"autodoc" (no aliases for md-harpoon's `harpoon_*` names):
---
---  preview.attach  { slot, path }  render the file at `path` into `slot`
---  preview.view    { slot }        focus / reopen `slot`
---  preview.browser { slot? }       open `slot` (or the current buffer) in the browser
---@module 'autodoc.preview.commands'

local layout = require("autodoc.preview.layout")

local M = {}

M.OWNER = "autodoc"

local SLOT_ERR = "args.slot must be one of '1','2','3','a','s','d'"

local function err(code, message)
  return { ok = false, code = code, error = message }
end

local function preview() return require("autodoc.preview") end

M.VERBS = {
  ["preview.attach"] = {
    description = "Render the Markdown file at `path` into preview slot `slot` ('1','2','3','a','s','d').",
    schema = { slot = "string", path = "string" },
    handler = function(args)
      if not layout.valid(args.slot) then return err("invalid_slot", SLOT_ERR) end
      if args.path == "" then return err("invalid_args", "args.path must be a non-empty string") end
      local path = vim.fn.fnamemodify(vim.fn.expand(args.path), ":p")
      if vim.fn.filereadable(path) ~= 1 then return err("not_readable", "file not readable: " .. path) end
      local ok, e = pcall(preview().render_path, args.slot, path)
      if not ok then return err("render_failed", tostring(e)) end
      return { ok = true, slot = args.slot, path = path }
    end,
  },
  ["preview.view"] = {
    description = "Focus preview slot `slot`, reopening its pinned document (or rendering the current buffer when empty).",
    schema = { slot = "string" },
    handler = function(args)
      if not layout.valid(args.slot) then return err("invalid_slot", SLOT_ERR) end
      local ok, e = pcall(preview().focus, args.slot)
      if not ok then return err("focus_failed", tostring(e)) end
      return { ok = true, slot = args.slot }
    end,
  },
  ["preview.browser"] = {
    description = "Open preview slot `slot`'s document (or, without a slot, the focused slot / current buffer) in the browser through `autodoc --export html`.",
    schema = { slot = "string?" },
    handler = function(args)
      if args.slot ~= nil and not layout.valid(args.slot) then return err("invalid_slot", SLOT_ERR) end
      local ok, e = pcall(preview().browser, args.slot)
      if not ok then return err("browser_failed", tostring(e)) end
      return { ok = true, slot = args.slot }
    end,
  },
}

local registered = {}

---Register the verbs. No-op without auto-core's mailbox.
---@return string[] registered names
function M.register_verbs()
  local ok, core = pcall(require, "auto-core")
  local reg = ok and core.mailbox and core.mailbox.commands
  if not reg or type(reg.register) ~= "function" then return {} end
  local names = {}
  for name, spec in pairs(M.VERBS) do
    local rok, rerr = reg.register(name, {
      owner = M.OWNER, description = spec.description, schema = spec.schema, handler = spec.handler,
    })
    if rok then
      registered[name] = true
      names[#names + 1] = name
    else
      vim.notify(("autodoc.preview: cannot register %s: %s"):format(name, tostring(rerr)), vim.log.levels.WARN)
    end
  end
  table.sort(names)
  return names
end

function M.unregister_verbs()
  local ok, core = pcall(require, "auto-core")
  local reg = ok and core.mailbox and core.mailbox.commands
  for name in pairs(registered) do
    if reg then pcall(reg.unregister, name) end
  end
  registered = {}
end

-- ── user commands ───────────────────────────────────────────────

M.USER_COMMANDS = {
  "AutodocPreviewFocus", "AutodocPreviewRender", "AutodocPreviewRenderPath",
  "AutodocPreviewFind", "AutodocPreviewCloseAll", "AutodocPreviewBrowser",
}

local function complete_slots(lead)
  return vim.tbl_filter(function(s) return s:sub(1, #lead) == lead end, layout.SLOTS)
end

local function slot_arg(arg)
  if layout.valid(arg) then return arg end
  vim.notify(("autodoc.preview: slot must be one of %s (got %q)")
    :format(table.concat(layout.SLOTS, ", "), tostring(arg or "")), vim.log.levels.WARN)
  return nil
end

---Define the :AutodocPreview* user commands (idempotent).
function M.user_commands()
  local cmd = vim.api.nvim_create_user_command
  cmd("AutodocPreviewFocus", function(o)
    local slot = slot_arg(o.args)
    if slot then preview().focus(slot) end
  end, { nargs = 1, complete = complete_slots, desc = "autodoc preview: focus / open slot" })
  cmd("AutodocPreviewRender", function(o)
    local slot = slot_arg(o.args)
    if slot then preview().render_current(slot) end
  end, { nargs = 1, complete = complete_slots, desc = "autodoc preview: render the current buffer into slot" })
  cmd("AutodocPreviewRenderPath", function(o)
    local slot = slot_arg(o.fargs[1])
    local path = table.concat(vim.list_slice(o.fargs, 2), " ")
    if not slot then return end
    if path == "" then
      vim.notify("autodoc.preview: path required", vim.log.levels.WARN)
      return
    end
    preview().render_path(slot, path)
  end, {
    nargs = "+",
    complete = function(lead, line)
      if #vim.split(line, "%s+") <= 2 then return complete_slots(lead) end
      return vim.fn.getcompletion(lead, "file")
    end,
    desc = "autodoc preview: render <slot> <path>",
  })
  cmd("AutodocPreviewFind", function() preview().find() end,
    { nargs = 0, desc = "autodoc preview: find a Markdown file under cwd, then pick a slot" })
  cmd("AutodocPreviewCloseAll", function() preview().close_all() end,
    { nargs = 0, desc = "autodoc preview: close every slot float" })
  cmd("AutodocPreviewBrowser", function(o)
    local arg = vim.trim(o.args or "")
    if arg ~= "" and not slot_arg(arg) then return end
    preview().browser(arg ~= "" and arg or nil)
  end, { nargs = "?", complete = complete_slots, desc = "autodoc preview: open in the browser ([slot] or current)" })
end

function M.remove_user_commands()
  for _, name in ipairs(M.USER_COMMANDS) do pcall(vim.api.nvim_del_user_command, name) end
end

-- ── default keys (opt-in) ───────────────────────────────────────

local SHIFTED = { ["1"] = "!", ["2"] = "@", ["3"] = "#", a = "A", s = "S", d = "D" }

---The default `<leader>m*` keys as { lhs, fn, desc } (md-harpoon's set).
---@return { [1]: string, [2]: function, desc: string }[]
function M.default_keys()
  local keys = {}
  for _, slot in ipairs(layout.SLOTS) do
    keys[#keys + 1] = { "<leader>m" .. slot, function() preview().focus(slot) end,
      desc = "autodoc preview: " .. layout.LABELS[slot] }
  end
  for _, slot in ipairs(layout.SLOTS) do
    keys[#keys + 1] = { "<leader>m" .. SHIFTED[slot], function() preview().render_current(slot) end,
      desc = "autodoc preview: render → " .. slot }
  end
  keys[#keys + 1] = { "<leader>mf", function() preview().find() end, desc = "autodoc preview: find → pick panel" }
  keys[#keys + 1] = { "<leader>mc", function() preview().close_all() end, desc = "autodoc preview: close all floats" }
  return keys
end

local mapped = {}

function M.map_keys()
  for _, k in ipairs(M.default_keys()) do
    vim.keymap.set("n", k[1], k[2], { desc = k.desc })
    mapped[#mapped + 1] = k[1]
  end
end

function M.unmap_keys()
  for _, lhs in ipairs(mapped) do pcall(vim.keymap.del, "n", lhs) end
  mapped = {}
end

return M
