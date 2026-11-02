-- autodoc.health's workspace check (`:checkhealth autodoc`): a workspace whose root holds AutoDoc's
-- own folders, or lies inside one (workspace.list's autodoc_dirs), is a warning naming the folders
-- and the fix; none flagged is one ok. A recording stand-in for vim.health takes what it reports.
--   [flagged] two flagged workspaces of three: a warn each, naming the folders and the fix
--   [clean]   none flagged: one ok, no warn
--   [older]   a daemon whose list carries no autodoc_dirs: one ok, as clean
local t = require("helpers")

local function recorder()
  local r = { calls = {} }
  for _, k in ipairs({ "start", "ok", "warn", "error", "info" }) do
    r[k] = function(msg, advice) r.calls[#r.calls + 1] = { kind = k, msg = msg, advice = advice } end
  end
  function r.of(kind)
    return vim.tbl_filter(function(c) return c.kind == kind end, r.calls)
  end
  return r
end

local health = require("autodoc.health")

t.section("[flagged]", function()
  local h = recorder()
  health.check_workspaces(h, {
    { name = "home", root = "/home/me", autodoc_dirs = {
      { dir = "config", relation = "holds", path = "/home/me/.config/autodoc" },
      { dir = "state", relation = "holds", path = "/home/me/.local/state/autodoc" } } },
    { name = "notes", root = "/home/me/notes" },
    { name = "inner", root = "/home/me/.local/share/autodoc/kb", autodoc_dirs = {
      { dir = "data", relation = "inside", path = "/home/me/.local/share/autodoc" } } },
  })
  local warns = h.of("warn")
  t.eq(#warns, 2, "a warn per flagged workspace")
  t.eq(#h.of("error"), 0, "no error: a flagged root still works")
  local home, inner = warns[1] or {}, warns[2] or {}
  for _, s in ipairs({ "home", "/home/me", "config", "/home/me/.config/autodoc", "state", "/home/me/.local/state/autodoc" }) do
    t.ok(tostring(home.msg):find(s, 1, true) ~= nil, "home's warn names " .. s, home.msg)
  end
  for _, s in ipairs({ "inner", "inside", "data", "/home/me/.local/share/autodoc" }) do
    t.ok(tostring(inner.msg):find(s, 1, true) ~= nil, "inner's warn names " .. s, inner.msg)
  end
  local advice = table.concat(home.advice or {}, " ")
  t.ok(advice:find("move the root", 1, true) and advice:find("narrow it", 1, true), "the warn says the fix", advice)
  t.eq(#h.of("ok"), 0, "no ok beside the warns")
end)

t.section("[clean]", function()
  local h = recorder()
  health.check_workspaces(h, { { name = "notes", root = "/home/me/notes", autodoc_dirs = {} } })
  t.eq(#h.of("warn"), 0, "nothing flagged, no warn")
  t.eq(#h.of("ok"), 1, "one ok")
end)

t.section("[older]", function()
  local h = recorder()
  health.check_workspaces(h, { { name = "notes", root = "/home/me/notes" } })
  t.eq(#h.of("warn"), 0, "no autodoc_dirs, no warn")
  t.eq(#h.of("ok"), 1, "one ok")
end)

t.finish()
