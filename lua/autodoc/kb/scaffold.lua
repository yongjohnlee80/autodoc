-- autodoc.kb.scaffold: create a KB's layout (ADR 1791209946 §1), idempotently.
--
--   * Never overwrites an existing file, with two exceptions: the managed KB_OPERATIONS.md and
--     _schema/frontmatter.yaml are replaced when the KB's copy carries an older autodoc_version
--     (a semantic-version compare). A copy with no readable version is kept.
--   * RULES.md is created once and never rewritten.
--   * Never creates `.todo-list/` (the todo store is not part of the KB, §1).
--   * Never writes into an existing, non-empty raw/ (it is the user's immutable material): raw/ABOUT.md
--     is created only with a new or empty raw/.
--   * A `_` folder's descriptor is `_ABOUT.md` (M.about_name): `_templates/ABOUT.md` would be the same
--     file as `_templates/about.md`, the `about` type's template, on a case-insensitive filesystem
--     (macOS). A KB an older scaffold wrote renames its two by hand (README, "The KB layout").

local util = require("autodoc.kb.util")
local schema = require("autodoc.kb.schema")
local version = require("autodoc.kb.version")

local M = {}

M.DIRS = { "raw", "adrs", "conventions", "playbooks", "reference", "synthesis", "reviews", "prs", "notes",
  "scripts", "archive", "_templates", "_schema" }
M.ROOT_FILES = { "RULES.md", "AGENTS.md", "KB_OPERATIONS.md", "CLAUDE.md", "GEMINI.md" }
M.MANAGED = { ["KB_OPERATIONS.md"] = true, ["_schema/frontmatter.yaml"] = true }

---about_name is the name of folder d's descriptor: `_ABOUT.md` in a folder whose name starts with
---`_`, else `ABOUT.md`. `_templates/` holds a template per type, `about.md` among them, and
---`ABOUT.md` beside it is one file where case is not significant; the `_` folders all take the
---prefix, so the rule is one rule.
---@param d string a folder of M.DIRS
---@return string
function M.about_name(d)
  return d:sub(1, 1) == "_" and "_ABOUT.md" or "ABOUT.md"
end

local function render(text, vars)
  text = text:gsub("{{autodoc_version}}", vars.autodoc_version)
  -- {{date}} is filled only in the frontmatter's created line: elsewhere (the _templates ABOUT,
  -- the generated templates) it is an Obsidian variable the user's editor fills.
  text = text:gsub("\ncreated: {{date}}\n", "\ncreated: " .. vars.date .. "\n", 1)
  return text
end

---The version a managed file declares: frontmatter `autodoc_version:` or a `# autodoc_version:`
---comment line.
function M.declared_version(text)
  if not text then return nil end
  local v = text:match("\nautodoc_version:%s*\"?([^\"\n]-)\"?%s*\n") or text:match("^#%s*autodoc_version:%s*([^\n]-)%s*\n")
  return v
end

---Every file the scaffold writes, with its rendered text, in a stable order.
---@return {rel: string, text: string, managed: boolean}[]
function M.files(vars)
  local out = {}
  local tdir = schema.templates_dir
  for _, name in ipairs(M.ROOT_FILES) do
    out[#out + 1] = { rel = name, text = render(assert(util.read_file(tdir .. "/" .. name)), vars), managed = M.MANAGED[name] or false }
  end
  out[#out + 1] = {
    rel = "_schema/frontmatter.yaml",
    text = render(assert(util.read_file(schema.schema_path)), vars),
    managed = true,
  }
  for _, d in ipairs(M.DIRS) do
    out[#out + 1] = { rel = d .. "/" .. M.about_name(d), text = render(assert(util.read_file(tdir .. "/about/" .. d .. ".md")), vars), managed = false }
  end
  local s = schema.shipped()
  for _, t in ipairs(s.type_order) do
    out[#out + 1] = { rel = "_templates/" .. t .. ".md", text = schema.template(s, t), managed = false }
  end
  return out
end

local function real_view(root)
  return {
    exists = function(rel) return util.exists(root .. "/" .. rel) end,
    read = function(rel) return util.read_file(root .. "/" .. rel) end,
    dir_empty = function(rel)
      local h = vim.uv.fs_scandir(root .. "/" .. rel)
      return not h or vim.uv.fs_scandir_next(h) == nil
    end,
  }
end

---Create the layout under root.
---@param root string
---@param opts table? {autodoc_version, date, dry, view, before_create = fn(rel), before_update = fn(rel)}
---@return table report {created, kept, updated, skipped, dirs, contents = {rel -> text}}
function M.scaffold(root, opts)
  opts = opts or {}
  root = util.normpath(root)
  local vars = {
    autodoc_version = opts.autodoc_version or version.autodoc,
    date = opts.date or os.date("%Y-%m-%d"),
  }
  local view = opts.view or real_view(root)
  local report = { created = {}, kept = {}, updated = {}, skipped = {}, dirs = {}, contents = {}, reasons = {} }

  local raw_writable = not view.exists("raw") or view.dir_empty("raw")

  if not opts.dry then
    for _, d in ipairs(M.DIRS) do
      if not util.isdir(root .. "/" .. d) then
        util.mkdirp(root .. "/" .. d)
        report.dirs[#report.dirs + 1] = d
      end
    end
  else
    for _, d in ipairs(M.DIRS) do
      if not view.exists(d) then report.dirs[#report.dirs + 1] = d end
    end
  end

  for _, f in ipairs(M.files(vars)) do
    local rel = f.rel
    if rel == "raw/ABOUT.md" and not raw_writable and not view.exists(rel) then
      report.skipped[#report.skipped + 1] = rel
      report.reasons[rel] = "raw/ exists and is not empty: never written"
    elseif not view.exists(rel) then
      if opts.before_create then opts.before_create(rel, f.text) end
      if not opts.dry then util.write_file(root .. "/" .. rel, f.text) end
      report.created[#report.created + 1] = rel
      report.contents[rel] = f.text
    elseif f.managed then
      local have = M.declared_version(view.read(rel))
      local cmp = util.semver_cmp(have, vars.autodoc_version)
      if cmp == -1 then
        if opts.before_update then opts.before_update(rel, f.text) end
        if not opts.dry then util.write_file(root .. "/" .. rel, f.text) end
        report.updated[#report.updated + 1] = rel
        report.contents[rel] = f.text
      else
        report.kept[#report.kept + 1] = rel
        report.reasons[rel] = cmp == nil and "no readable autodoc_version: kept" or "same or newer autodoc_version"
      end
    else
      report.kept[#report.kept + 1] = rel
    end
  end
  return report
end

return M
