-- autodoc.kb.migrate: move a KB to the v2 layout (ADR 1791209946 §8) through a checksummed
-- manifest, with a full pre-image undo.
--
--   :AutodocKbMigrate [root]            dry run: writes migration-manifest.json, migration.diff and
--                                       summary.txt to an output directory; changes nothing
--   :AutodocKbMigrate --apply [root]    applies the latest dry run's manifest (or --manifest=PATH)
--   :AutodocKbMigrate --undo [root]     restores the latest apply's pre-image
--   :AutodocKbMigrate --forget [root]   deletes the latest apply's pre-image
--
-- The plan (§8.2–8.6):
--   * every file's old path, new path and sha256, and the sha256 it has after the edits;
--   * every reference rewrite, resolved against its OLD location and re-relativized from its NEW one,
--     in the form it was written; fenced code is reported, not rewritten; bare-name wikilinks, URLs and
--     paths outside the KB are left alone; raw/ and archive-bound files are frozen;
--   * frontmatter normalization (type mapping, synonym keys), keyed by file;
--   * collisions, with a deterministic `--from-<old-parent-slug>` suffix; an unexpected existing
--     target aborts;
--   * todo stores outside the KB that reference it (auto-core.todo's known directories and overrides).
-- The todo store inside the KB stays in place: only its links are maintained.
--
-- Apply re-hashes everything, writes and verifies a pre-image under stdpath("state")/autodoc/
-- migrations/<id>/ BEFORE its first write, moves, rewrites, normalizes, scaffolds, validates, and
-- restores from the pre-image on any failure. When the KB is a git repo it commits the migration's
-- paths (no remote assumed; never pushes).

local util = require("autodoc.kb.util")
local refs = require("autodoc.kb.refs")
local schema = require("autodoc.kb.schema")
local frontmatter = require("autodoc.kb.frontmatter")
local scaffold = require("autodoc.kb.scaffold")
local version = require("autodoc.kb.version")

local M = {}

M.MANIFEST_VERSION = 1
M._issued = {} -- ids handed out by this session's dry runs

-- ─── the mapping (§8.2) ─────────────────────────────────────────────

local TOP_SAME = { adrs = true, conventions = true, playbooks = true }
local REF_GROUP = { reference = true, glossary = true, routes = true, sources = true }
local SYNTH_GROUP = { synthesis = true, ["bug-fixes"] = true }
local AGENT_SUB_SYNTH = { benchmarks = true, design = true, incidents = true }
local AGENT_SUB_NOTES = { tasks = true, scratch = true, incoming = true }
local ARCHIVE_FILES = { ["index.md"] = true, ["log.md"] = true, ["KB_RULES.md"] = true, ["hello.md"] = true,
  ["AGENTS.md"] = true }
local ARCHIVE_DIRS = { log = true, home = true }
-- the roots whose directories the migration empties and removes
M.SOURCE_ROOTS = { shared = true, agents = true, log = true, home = true }

local function split(rel)
  local p = {}
  for s in rel:gmatch("[^/]+") do p[#p + 1] = s end
  return p
end

local function join_from(p, i)
  return table.concat(p, "/", i)
end

---Destination of a KB-relative path, and the rule that sent it there. Unmapped paths stay.
function M.dest(rel)
  local p = split(rel)
  local n = #p
  if p[1] == "shared" and n >= 3 then
    local sub = p[2]
    if TOP_SAME[sub] then return sub .. "/" .. join_from(p, 3), "shared/" .. sub .. "->" .. sub end
    if REF_GROUP[sub] then return "reference/" .. join_from(p, 3), "shared/" .. sub .. "->reference" end
    if sub == "synthesis" and p[3] == "archive" and n >= 4 then
      return "archive/synthesis/" .. join_from(p, 4), "shared/synthesis/archive->archive/synthesis"
    end
    if SYNTH_GROUP[sub] then return "synthesis/" .. join_from(p, 3), "shared/" .. sub .. "->synthesis" end
    if sub == "prs" then return "prs/" .. join_from(p, 3), "shared/prs->prs" end
    if sub == "scripts" then return "scripts/" .. join_from(p, 3), "shared/scripts->scripts" end
    if sub == "agents" and n >= 4 then
      local d = M.dest("agents/" .. join_from(p, 3))
      return d, "shared/agents->as agents/"
    end
    return rel, "UNMAPPED shared/" .. sub
  end
  if p[1] == "agents" and n >= 3 then
    local name = p[2]
    if n == 3 then return "notes/" .. p[3], "agents/<n>/loose->notes" end
    local sub = p[3]
    if sub == "reviews" then return "reviews/" .. name .. "/" .. join_from(p, 4), "agents/<n>/reviews->reviews/<n>" end
    if AGENT_SUB_SYNTH[sub] then return "synthesis/" .. join_from(p, 4), "agents/<n>/" .. sub .. "->synthesis" end
    if AGENT_SUB_NOTES[sub] then return "notes/" .. join_from(p, 4), "agents/<n>/" .. sub .. "->notes" end
    return "notes/" .. join_from(p, 3), "agents/<n>/" .. sub .. "->notes (unlisted sub)"
  end
  if n == 1 and (ARCHIVE_FILES[p[1]] or p[1]:match("^AGENTS%.md%.bak%.")) then
    return "archive/" .. rel, "root->archive"
  end
  if ARCHIVE_DIRS[p[1]] and n >= 2 then return "archive/" .. rel, p[1] .. "/->archive" end
  -- the old templates predate the schema; the scaffold writes `_templates/<type>.md` from it after
  -- the move (§4.6), so the old ones are archived rather than left to shadow the generated ones
  if p[1] == "_templates" and n >= 2 then return "archive/" .. rel, "_templates->archive (regenerated from the schema)" end
  return rel, "stay"
end

-- references to these old targets keep pointing at the same path: the file there is replaced
-- (AGENTS.md is rewritten to §2; its old text is archived)
M.REPLACED_IN_PLACE = { ["AGENTS.md"] = true }

-- ─── frontmatter normalization (§8.5) ──────────────────────────────

M.TYPE_MAP = {
  ["review-response"] = { "review" }, ["review-fold"] = { "review" }, ["review-verdicts"] = { "review" },
  ["code-review"] = { "review" }, ["kb-review"] = { "review" },
  ["bug-fix"] = { "synthesis", "bug-fix" }, design = { "synthesis", "design" }, evidence = { "synthesis", "evidence" },
  incident = { "synthesis", "incident" }, benchmark = { "synthesis", "benchmark" }, analysis = { "synthesis", "analysis" },
  handoff = { "synthesis", "handoff" },
  source = { "reference", "source" }, glossary = { "reference", "glossary" }, route = { "reference", "route" },
  task = { "note", "task" }, scratch = { "note", "scratch" },
}

M.SYNONYMS = {
  ["superseded-by"] = "superseded_by",
  ["related-adrs"] = "related", ["related-conventions"] = "related", ["related-playbooks"] = "related",
  head_sha = "head", reviewed_commit = "head",
  base_sha = "base", base_commit = "base",
  pull_request = "pr",
  ["reviewed-by"] = "reviewers",
}

-- ─── helpers ────────────────────────────────────────────────────────

local function is_md(rel) return rel:match("%.md$") ~= nil end

local function slug(s)
  s = s:lower():gsub("[^%w]+", "-"):gsub("^%-+", ""):gsub("%-+$", "")
  return s == "" and "root" or s
end

local function with_suffix(rel, suffix)
  local dir, base = util.dirname(rel), util.basename(rel)
  local stem, ext = base:match("^(.+)(%.[^.]+)$")
  if not stem then stem, ext = base, "" end
  return util.join(dir, stem .. suffix .. ext)
end

local function sorted_keys(t)
  local k = {}
  for x in pairs(t) do k[#k + 1] = x end
  table.sort(k)
  return k
end

local function under(path, dir)
  return dir == "" or path:sub(1, #dir + 1) == dir .. "/"
end

local function state_root(opts)
  return (opts and opts.state_dir) or (vim.fn.stdpath("state") .. "/autodoc")
end

local function root_key(root)
  return util.sha256(root):sub(1, 12)
end

local function read_index(opts)
  return util.read_json(state_root(opts) .. "/migrations/index.json") or {}
end

local function write_index(opts, idx)
  util.write_file(state_root(opts) .. "/migrations/index.json", util.json_encode(idx, 2))
end

local function is_git(root)
  local code, out = util.run({ "git", "-C", root, "rev-parse", "--show-toplevel" })
  if code ~= 0 then return false end
  return true, vim.trim(out)
end

---The todo stores auto-core knows: known_dirs() entries and dir_overrides values.
function M.known_todo_stores()
  local out, seen = {}, {}
  local ok, todo = pcall(require, "auto-core.todo")
  if ok and type(todo) == "table" and type(todo.known_dirs) == "function" then
    local okk, list = pcall(todo.known_dirs)
    if okk and type(list) == "table" then
      for _, e in ipairs(list) do
        if type(e.todo_dir) == "string" and not seen[e.todo_dir] then
          seen[e.todo_dir] = true
          out[#out + 1] = e.todo_dir
        end
      end
    end
  end
  local oks, st = pcall(require, "auto-core.state")
  if oks and type(st) == "table" and type(st.namespace) == "function" then
    local okn, ns = pcall(st.namespace, "todo", { persist = "json" })
    if okn and ns then
      local overrides = ns:get("dir_overrides") or {}
      for _, d in pairs(overrides) do
        if type(d) == "string" and not seen[d] then
          seen[d] = true
          out[#out + 1] = d
        end
      end
    end
  end
  table.sort(out)
  return out, ok
end

-- ─── edits ──────────────────────────────────────────────────────────

---Apply a file's edits to its text: reference span rewrites first (on the original lines), then
---frontmatter normalization ops. Errors when an edit's precondition fails.
function M.apply_edits(text, rewrites, norm)
  local lines, trailing = util.split_lines(text)
  local by_line = {}
  for _, r in ipairs(rewrites or {}) do
    by_line[r.line] = by_line[r.line] or {}
    table.insert(by_line[r.line], r)
  end
  for ln, list in pairs(by_line) do
    table.sort(list, function(a, b) return a.col > b.col end)
    local s = lines[ln]
    if not s then error(("edit beyond the file's end (line %d)"):format(ln)) end
    for _, r in ipairs(list) do
      if s:sub(r.col, r.col + #r.old - 1) ~= r.old then
        error(("line %d col %d: expected %q, found %q"):format(ln, r.col, r.old, s:sub(r.col, r.col + #r.old - 1)))
      end
      s = s:sub(1, r.col - 1) .. r.new .. s:sub(r.col + #r.old)
    end
    lines[ln] = s
  end
  for _, op in ipairs(norm or {}) do
    local s = lines[op.line]
    if op.op == "rename_key" then
      local rest = s and s:match("^" .. vim.pesc(op.from) .. "(%s*:.*)$")
      if not rest then error(("line %d: expected key %q"):format(op.line, op.from)) end
      lines[op.line] = op.to .. rest
    elseif op.op == "set_type" then
      local v = s and s:match("^type%s*:%s*(.-)%s*$")
      if not v or frontmatter.unquote(v:gsub("%s+#.*$", "")) ~= op.from then
        error(("line %d: expected type %q"):format(op.line, op.from))
      end
      lines[op.line] = "type: " .. op.to .. (op.kind and ("\nkind: " .. op.kind) or "")
    end
  end
  return util.join_lines(lines, trailing)
end

local function schema_counts(root, paths)
  local s = schema.shipped()
  local c = {}
  for _, rel in ipairs(paths) do
    local text = util.read_file(root .. "/" .. rel)
    if text then
      for _, d in ipairs(schema.validate(s, text)) do
        local k = d.rule .. (d.field ~= "" and (" " .. d.field) or "")
        c[k] = (c[k] or 0) + 1
      end
    end
  end
  return c
end

function M.schema_counts_texts(texts)
  local s = schema.shipped()
  local c = {}
  for _, text in ipairs(texts) do
    for _, d in ipairs(schema.validate(s, text)) do
      local k = d.rule .. (d.field ~= "" and (" " .. d.field) or "")
      c[k] = (c[k] or 0) + 1
    end
  end
  return c
end

function M.schema_subject(rel)
  return is_md(rel) and rel:sub(1, 4) ~= "raw/" and rel:sub(1, 8) ~= "archive/" and rel:sub(1, 11) ~= ".todo-list/"
    and rel:sub(1, 11) ~= "_templates/" and rel:sub(1, 10) ~= ".obsidian/"
end

-- ─── planning ───────────────────────────────────────────────────────

---Build the plan for root. Pure: reads the KB (and external todo stores), writes nothing.
---@return table plan, string? err
function M.plan(opts)
  local root = util.normpath(opts.root)
  if not util.isdir(root) then return nil, "not a directory: " .. root end
  local now = opts.now or os.time()
  local plan = {
    version = M.MANIFEST_VERSION,
    id = opts.id or (os.date("!%Y%m%dT%H%M%SZ", now) .. "-" .. root_key(root)),
    root = root,
    date = opts.date or os.date("%Y-%m-%d", now),
    autodoc_version = opts.autodoc_version or version.autodoc,
    files = {},
    rewrites = {},
    normalize = {},
    reported = { fenced = {}, unresolved = {}, split_dirs = {}, bare_wikilinks = {}, normalize = {}, unmapped = {} },
    collisions = {},
    removed_dirs = {},
    created_dirs = {},
    created_files = {},
    scaffold = {},
    todo_stores = { checked = {}, joined = {}, note = "" },
    counts = {},
  }

  local files, dirs = util.walk(root, { [".git"] = true })
  local pre_view = refs.view(files, dirs)

  -- destinations
  local dest, rule = {}, {}
  for _, f in ipairs(files) do
    dest[f], rule[f] = M.dest(f)
    if rule[f]:match("^UNMAPPED") then table.insert(plan.reported.unmapped, f) end
  end

  -- scaffold targets in the moved tree are reserved: a moved file never lands on one
  local reserved = {}
  for _, sf in ipairs(scaffold.files({ autodoc_version = plan.autodoc_version, date = plan.date })) do
    reserved[util.fold(sf.rel)] = sf.rel
  end

  -- collisions (§8.6): group destinations case-folded
  local groups = {}
  for _, f in ipairs(files) do
    local k = util.fold(dest[f])
    groups[k] = groups[k] or {}
    table.insert(groups[k], f)
  end
  for _, f in ipairs(files) do
    local k = util.fold(dest[f])
    if reserved[k] and dest[f] ~= f then
      groups[k] = groups[k] or {}
      if not groups[k].reserved then groups[k].reserved = reserved[k] end
    end
  end
  for _, k in ipairs(sorted_keys(groups)) do
    local g = groups[k]
    if #g > 1 or (g.reserved and #g >= 1) then
      local entry = { dest = dest[g[1]], sources = {}, resolution = {} }
      for _, f in ipairs(g) do table.insert(entry.sources, f) end
      if g.reserved then entry.reserved = g.reserved end
      for _, f in ipairs(g) do
        if dest[f] ~= f then
          local nd = with_suffix(dest[f], "--from-" .. slug(util.dirname(f)))
          entry.resolution[f] = nd
          dest[f] = nd
          rule[f] = rule[f] .. " (collision suffix)"
        end
      end
      table.insert(plan.collisions, entry)
    end
  end
  -- after suffixing, every destination must be unique, case-folded, and clear of the reserved names
  local final_cf = {}
  for _, f in ipairs(files) do
    local k = util.fold(dest[f])
    if final_cf[k] then return nil, ("unresolvable collision: %s and %s both map to %s"):format(final_cf[k], f, dest[f]) end
    if reserved[k] and dest[f] ~= f then return nil, ("unresolvable collision with the scaffold's %s: %s"):format(reserved[k], f) end
    final_cf[k] = f
  end
  -- a destination that is another moving file's old path would chain moves: refuse
  local moving_src = {}
  for _, f in ipairs(files) do
    if dest[f] ~= f then moving_src[f] = true end
  end
  for _, f in ipairs(files) do
    if dest[f] ~= f and moving_src[dest[f]] then
      return nil, ("chained move: %s -> %s, which is itself moving"):format(f, dest[f])
    end
  end
  -- a destination file that is also a directory in the new tree
  local post_files = {}
  for _, f in ipairs(files) do post_files[#post_files + 1] = dest[f] end
  table.sort(post_files)
  do
    local pdirs = {}
    for _, f in ipairs(post_files) do
      local d = util.dirname(f)
      while d ~= "" do
        pdirs[d] = true
        d = util.dirname(d)
      end
    end
    for _, f in ipairs(post_files) do
      if pdirs[f] then return nil, "a destination is both a file and a directory: " .. f end
    end
  end

  local function final(rel)
    if M.REPLACED_IN_PLACE[rel] then return rel end
    return dest[rel] or rel
  end

  local function frozen(f)
    return f:sub(1, 4) == "raw/" or f:sub(1, 8) == "archive/" or dest[f]:sub(1, 8) == "archive/"
  end
  local function live_doc(f)
    return is_md(f) and not frozen(f) and f:sub(1, 10) ~= ".obsidian/" and f:sub(1, 5) ~= ".git/"
  end

  -- directory mapping: a directory maps when every file under it lands under one new directory.
  -- Its archive/ subfolder going to archive/ (shared/synthesis/archive, §8.2's "except archive/")
  -- doesn't split it.
  local dir_cache = {}
  local function map_dir(d)
    if dir_cache[d] ~= nil then return dir_cache[d] or nil end
    local probe = M.dest(d .. "/__probe__")
    local nd = probe:sub(1, -(#"/__probe__" + 1))
    local okd = true
    for _, f in ipairs(files) do
      local carve_out = under(f, d .. "/archive") and dest[f]:sub(1, 8) == "archive/"
      if under(f, d) and not carve_out and not under(dest[f], nd) then
        okd = false
        break
      end
    end
    dir_cache[d] = okd and nd or false
    return okd and nd or nil
  end

  -- the post-move view (scaffold files are added below, for validation only)
  local post_dirs = {}
  local post_view = refs.view(post_files, post_dirs)

  -- agent names for agents/<n>/ mentions
  local agent_names = {}
  for _, d in ipairs(dirs) do
    local n = d:match("^agents/([^/]+)$")
    if n then agent_names[#agent_names + 1] = n end
  end
  table.sort(agent_names)
  plan.agent_names = agent_names
  local px = refs.prefixes(root, agent_names)

  -- ── references ──
  local counts = {}
  local function count(r, action)
    local k = r.class .. "|" .. (r.sub or "?") .. "|" .. action
    counts[k] = (counts[k] or 0) + 1
  end

  ---Compute a reference's new text, from new source location nsrc. Returns new_text, target,
  ---new_target, status ("rewrite", "unchanged", "unresolved", "split-dir", "ignored").
  local function plan_ref(r, src, nsrc)
    if r.kind == "none" then return nil, nil, nil, "ignored" end
    if r.kind == "bare" then return nil, nil, nil, "bare" end
    if r.kind == "root" and (r.kbrel == nil or r.kbrel == "") then return nil, nil, nil, "ignored" end
    local target, how = refs.resolve(r, src, pre_view)
    r.how = how
    if not target then return nil, nil, nil, "unresolved" end
    local is_dir = target:sub(-1) == "/"
    local ntarget
    if is_dir then
      local d = target:sub(1, -2)
      ntarget = map_dir(d)
      if not ntarget then return nil, target, nil, "split-dir" end
      ntarget = ntarget .. "/"
    else
      ntarget = final(target)
    end
    local old = r.old
    local new
    if r.kind == "wiki" then
      -- unchanged text that still resolves to the moved target stays
      local still = refs.resolve_wiki(nsrc, old, post_view)
      if still == ntarget then return old, target, ntarget, "unchanged" end
      local strip_md = not refs.has_ext(old)
      local lead = old:sub(1, 1) == "/" and "/" or ""
      local function shape(p)
        if strip_md then p = p:gsub("%.md$", "") end
        return p
      end
      if how == "relative" then
        new = util.relpath(ntarget, util.dirname(nsrc))
        if old:sub(1, 2) == "./" and new:sub(1, 1) ~= "." then new = "./" .. new end
        new = shape(new)
      elseif how == "suffix" or how == "suffix-ambiguous" then
        local nseg = #split(old)
        local segs = split(ntarget)
        local cand = shape(table.concat(segs, "/", math.max(1, #segs - nseg + 1)))
        if refs.resolve_wiki(nsrc, cand, post_view) == ntarget then
          new = cand
        else
          new = shape(ntarget)
        end
      else
        new = lead .. shape(ntarget)
      end
    elseif r.kind == "rel" or (r.kind == "fmrel" and how == "rel") then
      local p, frag = refs.split_frag(old)
      local enc = p:find("%%20") ~= nil
      local still = refs.resolve_rel(nsrc, old, post_view)
      if still == ntarget then return old, target, ntarget, "unchanged" end
      local np = util.relpath(is_dir and ntarget:sub(1, -2) or ntarget, util.dirname(nsrc))
      if is_dir then np = np .. "/" end
      if not is_dir and not refs.has_ext(util.unquote(p)) then np = np:gsub("%.md$", "") end
      if (p:sub(1, 2) == "./") and np:sub(1, 1) ~= "." then np = "./" .. np end
      if enc then np = np:gsub(" ", "%%20") end
      new = np .. frag
    else -- root-relative (prefixed, bare shared/agents, frontmatter root relpaths)
      local rel = r.kind == "fmrel" and old or r.kbrel
      local p, frag = refs.split_frag(rel)
      local trailing_slash = p:sub(-1) == "/"
      local np = is_dir and ntarget:sub(1, -2) or ntarget
      if not is_dir and not refs.has_ext(util.unquote(p:gsub("/+$", ""))) then np = np:gsub("%.md$", "") end
      if trailing_slash or (is_dir and p:sub(-1) == "/") then np = np .. "/" end
      local prefix = r.kind == "fmrel" and "" or (r.prefix or "")
      new = (prefix ~= "" and (prefix .. "/") or "") .. np .. frag
    end
    if new == old then return old, target, ntarget, "unchanged" end
    return new, target, ntarget, "rewrite"
  end

  local file_edits = {} -- old path -> {rewrites, norm}
  local function edits_of(f)
    file_edits[f] = file_edits[f] or { rewrites = {}, norm = {} }
    return file_edits[f]
  end
  local refs_resolved = {} -- for validation: {file, line, col, new, kind, prefix, how, target, ntarget}
  local pre_broken = {}
  local frozen_count = 0

  local contents = {}
  for _, f in ipairs(files) do
    if is_md(f) and frozen(f) then frozen_count = frozen_count + 1 end
    if live_doc(f) then
      local text = assert(util.read_file(root .. "/" .. f))
      contents[f] = text
      local is_todo = f:sub(1, 11) == ".todo-list/"
      local nsrc = final(f)
      for _, r in ipairs(refs.scan(f, text, { px = px, todo = is_todo })) do
        local new, target, ntarget, status = plan_ref(r, f, nsrc)
        if status == "rewrite" and r.ctx == "fence" then status = "fenced" end
        count(r, status)
        local rec = {
          file = f, line = r.line, col = r.col, old = r.old, new = new, class = r.class, sub = r.sub,
          ctx = r.ctx, fm_key = r.fm_key, target = target, new_target = ntarget,
        }
        if status == "rewrite" then
          table.insert(plan.rewrites, rec)
          table.insert(edits_of(f).rewrites, { line = r.line, col = r.col, old = r.old, new = new })
        elseif status == "fenced" then
          table.insert(plan.reported.fenced, rec)
        elseif status == "unresolved" then
          table.insert(plan.reported.unresolved, rec)
          if r.ctx ~= "fence" then pre_broken[nsrc .. "\0" .. r.old] = true end
        elseif status == "split-dir" then
          table.insert(plan.reported.split_dirs, rec)
          if r.ctx ~= "fence" then pre_broken[nsrc .. "\0" .. r.old] = true end
        elseif status == "bare" then
          local cands = refs.resolve_wiki(f, r.old, pre_view)
          if not cands then
            -- reported for the record; Obsidian resolves bare names by its own rules
            plan.reported.bare_wikilinks[#plan.reported.bare_wikilinks + 1] = { file = f, line = r.line, name = r.old }
          end
        end
        if (status == "rewrite" or status == "unchanged") and r.ctx ~= "fence" then
          refs_resolved[#refs_resolved + 1] = {
            file = nsrc, line = r.line, col = r.col, text = new, kind = r.kind, prefix = r.prefix, how = r.how,
            new_target = ntarget, old_file = f,
          }
        end
      end
    end
  end
  plan.counts.references = counts
  plan.counts.frozen_docs = frozen_count

  -- ── frontmatter normalization (live documents outside the todo store) ──
  local norm_counts = {}
  local function ncount(k) norm_counts[k] = (norm_counts[k] or 0) + 1 end
  local s = schema.shipped()
  for _, f in ipairs(files) do
    if contents[f] and f:sub(1, 11) ~= ".todo-list/" and f:sub(1, 11) ~= "_templates/" then
      local fm = frontmatter.parse(util.split_lines(contents[f]))
      if fm then
        local tkey = fm.keys.type
        local t = tkey and type(tkey.value) == "string" and tkey.value or nil
        if not tkey then
          ncount("no type")
          table.insert(plan.reported.normalize, { file = f, issue = "no type" })
        elseif t and M.TYPE_MAP[t] then
          local to, kind = M.TYPE_MAP[t][1], M.TYPE_MAP[t][2]
          local op = { line = tkey.line, op = "set_type", from = t, to = to }
          if kind then
            if fm.keys.kind then
              if fm.keys.kind.value ~= kind then
                table.insert(plan.reported.normalize, { file = f, issue = "kind already set", kind = fm.keys.kind.value, wanted = kind })
              end
            else
              op.kind = kind
            end
          end
          table.insert(edits_of(f).norm, op)
          ncount("type " .. t .. " -> " .. to)
          t = to
        elseif t and not s.types[t] then
          ncount("type outside the schema")
          table.insert(plan.reported.normalize, { file = f, issue = "type outside the schema", type = t })
        end
        local claimed = {}
        for _, k in ipairs(fm.order) do
          local to = M.SYNONYMS[k]
          if k == "date" and not fm.keys.created then to = "created" end
          if to then
            if fm.keys[to] or claimed[to] then
              table.insert(plan.reported.normalize, { file = f, issue = "synonym kept: target key exists", key = k, target = to })
              ncount("synonym kept (conflict)")
            else
              claimed[to] = true
              table.insert(edits_of(f).norm, { line = fm.keys[k].line, op = "rename_key", from = k, to = to })
              ncount("key " .. k .. " -> " .. to)
            end
          end
        end
        -- enum values are reported, never rewritten
        local st = fm.keys.status and fm.keys.status.value
        if t and s.types[t] and type(st) == "string" then
          local _, by = schema.fields_for(s, t)
          local enum = by.status and by.status.enum
          if enum and not vim.tbl_contains(enum, st) then
            ncount("status outside the enum")
            table.insert(plan.reported.normalize, { file = f, issue = "status outside the enum", type = t, status = st })
          end
        end
      end
    end
  end
  plan.counts.normalize = norm_counts

  -- ── file entries and post-images ──
  local new_text = {}
  for _, f in ipairs(files) do
    local data = contents[f] or util.read_file(root .. "/" .. f)
    if data == nil then return nil, "cannot read " .. f end
    local sha = util.sha256(data)
    local e = file_edits[f]
    local nsha = sha
    if e and (#e.rewrites > 0 or #e.norm > 0) then
      local ok, out = pcall(M.apply_edits, data, e.rewrites, e.norm)
      if not ok then return nil, f .. ": " .. tostring(out) end
      new_text[f] = out
      nsha = util.sha256(out)
      if #e.norm > 0 then plan.normalize[f] = e.norm end
    end
    table.insert(plan.files, {
      old = f, new = dest[f], sha256 = sha, new_sha256 = nsha, rule = rule[f],
      edited = nsha ~= sha or nil, frozen = (is_md(f) and frozen(f)) or nil,
    })
    contents[f] = data
  end

  -- ── removed and created directories ──
  local post_dirset = {}
  for _, f in ipairs(post_files) do
    local d = util.dirname(f)
    while d ~= "" and not post_dirset[d] do
      post_dirset[d] = true
      d = util.dirname(d)
    end
  end
  local pre_dirset = {}
  for _, d in ipairs(dirs) do pre_dirset[d] = true end
  for _, d in ipairs(dirs) do
    local top = d:match("^[^/]+")
    if M.SOURCE_ROOTS[top] and not post_dirset[d] then
      local has_stay = false
      for _, f in ipairs(post_files) do
        if under(f, d) then
          has_stay = true
          break
        end
      end
      if not has_stay then table.insert(plan.removed_dirs, d) end
    end
  end
  local removed_set = {}
  for _, d in ipairs(plan.removed_dirs) do removed_set[d] = true end

  -- the scaffold, planned against the moved tree
  local post_exists = {}
  for _, f in ipairs(post_files) do post_exists[f] = true end
  for d in pairs(post_dirset) do post_exists[d] = true end
  for d in pairs(pre_dirset) do
    if not removed_set[d] then post_exists[d] = true end
  end
  local view = {
    exists = function(rel) return post_exists[rel] == true end,
    read = function(rel)
      for _, f in ipairs(files) do
        if dest[f] == rel then return new_text[f] or contents[f] end
      end
      return nil
    end,
    dir_empty = function(rel)
      for _, f in ipairs(post_files) do
        if under(f, rel) then return false end
      end
      return true
    end,
  }
  local srep = scaffold.scaffold(root, { dry = true, view = view, autodoc_version = plan.autodoc_version, date = plan.date })
  plan.scaffold = { created = srep.created, kept = srep.kept, updated = srep.updated, skipped = srep.skipped, reasons = srep.reasons }
  plan.created_files = {}
  for _, rel in ipairs(srep.created) do
    table.insert(plan.created_files, { path = rel, sha256 = util.sha256(srep.contents[rel]) })
  end
  for _, rel in ipairs(srep.updated) do
    -- a managed file the scaffold refreshes: a rewrite of an existing file
    for _, e in ipairs(plan.files) do
      if e.new == rel then
        e.new_sha256 = util.sha256(srep.contents[rel])
        e.edited = true
        e.scaffold_update = true
        new_text[e.old] = srep.contents[rel]
      end
    end
  end
  for d in pairs(post_dirset) do
    if not pre_dirset[d] then table.insert(plan.created_dirs, d) end
  end
  for _, d in ipairs(srep.dirs) do
    if not pre_dirset[d] or removed_set[d] then
      if not vim.tbl_contains(plan.created_dirs, d) then table.insert(plan.created_dirs, d) end
    end
  end
  table.sort(plan.created_dirs)

  -- ── todo stores outside the KB (§8.3a) ──
  local stores = opts.todo_stores
  local from_auto_core = false
  if stores == nil then stores, from_auto_core = M.known_todo_stores() end
  plan.todo_stores.source = opts.todo_stores and "opts.todo_stores" or (from_auto_core and "auto-core.todo.known_dirs() + dir_overrides" or "none (auto-core.todo unavailable)")
  plan.todo_stores.note = "a todo store auto-core does not know cannot be found; the stores checked are listed"
  for _, dir in ipairs(stores) do
    dir = util.normpath(dir)
    local entry = { dir = dir }
    table.insert(plan.todo_stores.checked, entry)
    if under(dir, root) or dir == root then
      entry.status = "inside the KB (scanned with it)"
    elseif not util.isdir(dir) then
      entry.status = "missing"
    else
      -- whose $KB_ROOT does this store use? A store at another KB's root uses that KB's.
      local parent = dir:match("^(.*)/[^/]+$") or ""
      local other_kb = parent ~= root and (util.isfile(parent .. "/AGENTS.md") or util.isfile(parent .. "/KB_RULES.md"))
      entry.kb_root_refs = other_kb and ("another KB's (" .. parent .. "): only absolute references count") or "this KB's"
      local sfiles = util.walk(dir, { [".git"] = true })
      local spx = refs.prefixes(root, agent_names)
      local joined = { dir = dir, files = {}, rewrites = {} }
      for _, rel in ipairs(sfiles) do
        if is_md(rel) then
          local abs = dir .. "/" .. rel
          local text = assert(util.read_file(abs))
          local rw = {}
          for _, r in ipairs(refs.scan(rel, text, { px = spx, todo = true })) do
            local counts_here = r.kind == "root" and r.kbrel and r.kbrel ~= ""
            if counts_here and other_kb and not (r.sub == "abs-kb" or r.sub == "tilde-kb") then counts_here = false end
            if counts_here then
              -- resolve against THIS KB, as root-relative
              local new, target, ntarget, status = plan_ref(r, "", "")
              if status == "rewrite" and r.ctx ~= "fence" then
                rw[#rw + 1] = { line = r.line, col = r.col, old = r.old, new = new, class = r.class, sub = r.sub,
                  target = target, new_target = ntarget }
              end
            end
          end
          if #rw > 0 then
            local out = M.apply_edits(text, rw, nil)
            table.insert(joined.files, { rel = rel, sha256 = util.sha256(text), new_sha256 = util.sha256(out) })
            for _, x in ipairs(rw) do
              x.file = rel
              table.insert(joined.rewrites, x)
            end
            new_text["\0ext\0" .. abs] = out
            contents["\0ext\0" .. abs] = text
          end
        end
      end
      entry.status = #joined.files > 0 and ("joined: " .. #joined.files .. " files, " .. #joined.rewrites .. " rewrites") or "no references to this KB"
      if #joined.files > 0 then table.insert(plan.todo_stores.joined, joined) end
    end
  end

  plan.git = { repo = is_git(root) }
  -- schema diagnostics the migrated tree would have (reported, never blocking)
  local texts = {}
  for _, e in ipairs(plan.files) do
    if M.schema_subject(e.new) then texts[#texts + 1] = new_text[e.old] or contents[e.old] end
  end
  plan.schema_diagnostics = M.schema_counts_texts(texts)
  plan._new_text = new_text
  plan._contents = contents
  plan._refs_resolved = refs_resolved
  plan._pre_broken = pre_broken
  plan._srep = srep
  return plan
end

-- ─── reports ────────────────────────────────────────────────────────

function M.summary(plan)
  local L = {}
  local function p(...) L[#L + 1] = table.concat({ ... }, "") end
  local moved, edited = 0, 0
  local rules = {}
  for _, e in ipairs(plan.files) do
    if e.old ~= e.new then moved = moved + 1 end
    if e.edited then edited = edited + 1 end
    rules[e.rule] = (rules[e.rule] or 0) + 1
  end
  p("AutoDoc KB migration plan ", plan.id)
  p("root: ", plan.root, plan.git.repo and "  (git repo)" or "  (not a git repo)")
  p("files: ", #plan.files, "  moved: ", moved, "  edited: ", edited, "  frozen docs (raw/, archive): ", plan.counts.frozen_docs)
  p("")
  p("== moves by rule ==")
  local rk = sorted_keys(rules)
  table.sort(rk, function(a, b) return rules[a] > rules[b] or (rules[a] == rules[b] and a < b) end)
  for _, k in ipairs(rk) do p(("  %6d  %s"):format(rules[k], k)) end
  p("")
  p("== references by class|sub|action ==")
  for _, k in ipairs(sorted_keys(plan.counts.references)) do p(("  %6d  %s"):format(plan.counts.references[k], k)) end
  p("")
  p("rewrites: ", #plan.rewrites, "  fenced (reported, not rewritten): ", #plan.reported.fenced,
    "  unresolved: ", #plan.reported.unresolved, "  split directories: ", #plan.reported.split_dirs,
    "  unresolved bare wikilinks: ", #plan.reported.bare_wikilinks)
  p("collisions: ", #plan.collisions)
  for _, c in ipairs(plan.collisions) do
    p("  ", c.dest, " <= ", table.concat(c.sources, " | "), c.reserved and (" (reserved: " .. c.reserved .. ")") or "")
    for _, src in ipairs(sorted_keys(c.resolution)) do p("     ", src, " -> ", c.resolution[src]) end
  end
  p("unmapped files (stay): ", #plan.reported.unmapped)
  p("")
  p("== frontmatter normalization ==")
  for _, k in ipairs(sorted_keys(plan.counts.normalize)) do p(("  %6d  %s"):format(plan.counts.normalize[k], k)) end
  p("")
  p("removed directories: ", #plan.removed_dirs, "  created directories: ", #plan.created_dirs)
  p("scaffold: created ", #plan.scaffold.created, ", kept ", #plan.scaffold.kept, ", updated ", #plan.scaffold.updated,
    ", skipped ", #plan.scaffold.skipped)
  for _, k in ipairs(plan.scaffold.kept) do p("  kept: ", k, plan.scaffold.reasons[k] and ("  (" .. plan.scaffold.reasons[k] .. ")") or "") end
  for _, k in ipairs(plan.scaffold.skipped) do p("  skipped: ", k, "  (", plan.scaffold.reasons[k] or "", ")") end
  p("")
  p("== todo stores (", plan.todo_stores.source, ") ==")
  for _, s in ipairs(plan.todo_stores.checked) do
    p("  ", s.dir, ": ", s.status or "?", s.kb_root_refs and ("  [$KB_ROOT = " .. s.kb_root_refs .. "]") or "")
  end
  p("  ", plan.todo_stores.note)
  if plan.schema_diagnostics then
    p("")
    p("== schema diagnostics after the migration (reported, never blocking) ==")
    for _, k in ipairs(sorted_keys(plan.schema_diagnostics)) do p(("  %6d  %s"):format(plan.schema_diagnostics[k], k)) end
  end
  return table.concat(L, "\n") .. "\n"
end

local function text_diff(a, b)
  local fn = (vim.text and vim.text.diff) or vim.diff
  return fn(a, b, { ctxlen = 3 }) or ""
end

function M.diff(plan)
  local out = {}
  for _, e in ipairs(plan.files) do
    if e.old ~= e.new or e.edited then
      out[#out + 1] = ("diff --kb a/%s b/%s"):format(e.old, e.new)
      if e.old ~= e.new then
        out[#out + 1] = "rename from " .. e.old
        out[#out + 1] = "rename to " .. e.new
      end
      if e.edited then
        out[#out + 1] = "--- a/" .. e.old
        out[#out + 1] = "+++ b/" .. e.new
        out[#out + 1] = text_diff(plan._contents[e.old], plan._new_text[e.old])
      end
    end
  end
  for _, c in ipairs(plan.created_files) do
    out[#out + 1] = ("diff --kb a/%s b/%s"):format(c.path, c.path)
    out[#out + 1] = "new file"
    out[#out + 1] = "--- /dev/null"
    out[#out + 1] = "+++ b/" .. c.path
    out[#out + 1] = text_diff("", plan._srep.contents[c.path])
  end
  for _, j in ipairs(plan.todo_stores.joined) do
    for _, f in ipairs(j.files) do
      local abs = j.dir .. "/" .. f.rel
      out[#out + 1] = ("diff --todo-store %s"):format(abs)
      out[#out + 1] = "--- a" .. abs
      out[#out + 1] = "+++ b" .. abs
      out[#out + 1] = text_diff(plan._contents["\0ext\0" .. abs], plan._new_text["\0ext\0" .. abs])
    end
  end
  for _, d in ipairs(plan.removed_dirs) do out[#out + 1] = "removed directory " .. d end
  return table.concat(out, "\n") .. "\n"
end

---The manifest written to disk: the plan without its in-memory parts.
local function manifest_of(plan)
  local m = {}
  for k, v in pairs(plan) do
    if k:sub(1, 1) ~= "_" then m[k] = v end
  end
  return m
end

-- ─── dry run ────────────────────────────────────────────────────────

function M.dry_run(opts)
  if not opts.id then
    -- a fresh id: the second's timestamp and the root, with a counter when a run in the same
    -- second already took it
    local root = util.normpath(opts.root)
    local base = os.date("!%Y%m%dT%H%M%SZ", opts.now or os.time()) .. "-" .. root_key(root)
    local id, n = base, 1
    while util.exists(state_root(opts) .. "/migrations/" .. id) or util.exists(state_root(opts) .. "/dry-runs/" .. id)
      or (opts.out_dir and util.exists(opts.out_dir .. "/" .. id)) or M._issued[id] do
      n = n + 1
      id = base .. "-" .. n
    end
    M._issued[id] = true
    opts = vim.tbl_extend("force", opts, { id = id })
  end
  local plan, err = M.plan(opts)
  if not plan then return nil, err end
  local out = opts.out_dir or (state_root(opts) .. "/dry-runs/" .. plan.id)
  out = util.normpath(out)
  if under(out, plan.root) or out == plan.root then return nil, "the output directory must be outside the KB" end
  util.mkdirp(out)
  local mpath = out .. "/migration-manifest.json"
  util.write_file(mpath, util.json_encode(manifest_of(plan)))
  util.write_file(out .. "/migration.diff", M.diff(plan))
  local summary = M.summary(plan)
  util.write_file(out .. "/summary.txt", summary)
  local idx = read_index(opts)
  local key = root_key(plan.root)
  idx[key] = idx[key] or { root = plan.root }
  idx[key].latest_dry_run = mpath
  write_index(opts, idx)
  return { plan = plan, manifest = mpath, out_dir = out, summary = summary }
end

-- ─── pre-image ──────────────────────────────────────────────────────

local function preimage_dir(opts, id)
  return state_root(opts) .. "/migrations/" .. id
end

local function save_preimage_index(pdir, pi)
  util.write_file(pdir .. "/preimage.json", util.json_encode(pi, 2))
end

---Write the pre-image and verify it, before anything in the KB changes.
local function write_preimage(m, pdir, hooks)
  if util.exists(pdir) then error("a pre-image already exists at " .. pdir) end
  util.mkdirp(pdir .. "/files")
  local pi = {
    id = m.id, root = m.root, status = "preparing", entries = {},
    removed_dirs = m.removed_dirs, created_files = m.created_files, created_dirs = m.created_dirs,
    git = { repo = m.git.repo },
  }
  for _, e in ipairs(m.files) do
    if e.old ~= e.new or e.edited then
      local copy = "files/kb/" .. e.old
      util.copy_file(m.root .. "/" .. e.old, pdir .. "/" .. copy)
      table.insert(pi.entries, { scope = "kb", rel = e.old, new = e.new, copy = copy, sha256 = e.sha256, new_sha256 = e.new_sha256 })
    end
  end
  for n, j in ipairs(m.todo_stores.joined) do
    for _, f in ipairs(j.files) do
      local copy = ("files/ext/%d/%s"):format(n, f.rel)
      util.copy_file(j.dir .. "/" .. f.rel, pdir .. "/" .. copy)
      table.insert(pi.entries, { scope = "ext", root = j.dir, rel = f.rel, new = f.rel, copy = copy, sha256 = f.sha256, new_sha256 = f.new_sha256 })
    end
  end
  if hooks and hooks.after_preimage_copies then hooks.after_preimage_copies(pdir, pi) end
  -- verify every copy against the dry run's sha256
  for _, en in ipairs(pi.entries) do
    local got = util.sha256_file(pdir .. "/" .. en.copy)
    if got ~= en.sha256 then
      error(("pre-image incomplete: %s copy has sha %s, the dry run's is %s"):format(en.rel, tostring(got), en.sha256))
    end
  end
  pi.status = "ready"
  save_preimage_index(pdir, pi)
  return pi
end

-- ─── restore (rollback and undo) ────────────────────────────────────

---Restore the KB from a pre-image. skip: set of abs paths not to touch (kept edits).
local function restore(pi, opts)
  opts = opts or {}
  local skip = opts.skip or {}
  local root = pi.root
  local problems = {}
  -- created files go
  for _, c in ipairs(pi.created_files or {}) do
    local abs = root .. "/" .. c.path
    if not skip[abs] and util.exists(abs) then util.remove(abs) end
  end
  -- moved files leave their new paths
  for _, en in ipairs(pi.entries) do
    if en.scope == "kb" and en.new ~= en.rel then
      local abs = root .. "/" .. en.new
      if not skip[abs] and util.exists(abs) then util.remove(abs) end
    end
  end
  -- removed directories come back before files are restored into them
  for _, d in ipairs(pi.removed_dirs or {}) do util.mkdirp(root .. "/" .. d) end
  -- every pre-image copy goes back to its old path
  for _, en in ipairs(pi.entries) do
    local base = en.scope == "kb" and root or en.root
    local newabs = base .. "/" .. en.new
    if not skip[newabs] then
      util.copy_file(opts.pdir .. "/" .. en.copy, base .. "/" .. en.rel)
    end
  end
  -- directories the migration created go, deepest first, when empty
  local cd = vim.deepcopy(pi.created_dirs or {})
  table.sort(cd, function(a, b) return #a > #b end)
  for _, d in ipairs(cd) do
    local abs = root .. "/" .. d
    local h = vim.uv.fs_scandir(abs)
    if h and vim.uv.fs_scandir_next(h) == nil then vim.uv.fs_rmdir(abs) end
  end
  -- verify
  for _, en in ipairs(pi.entries) do
    local base = en.scope == "kb" and root or en.root
    if not skip[base .. "/" .. en.new] then
      local got = util.sha256_file(base .. "/" .. en.rel)
      if got ~= en.sha256 then problems[#problems + 1] = ("%s: sha %s, expected %s"):format(en.rel, tostring(got), en.sha256) end
    end
  end
  for _, d in ipairs(pi.removed_dirs or {}) do
    if not util.isdir(root .. "/" .. d) then problems[#problems + 1] = "directory not recreated: " .. d end
  end
  return problems
end

-- ─── validation (§8.7) ──────────────────────────────────────────────

local function validate(m, plan, root)
  local fails = {}
  local files, dirs = util.walk(root, { [".git"] = true })
  local have = {}
  for _, f in ipairs(files) do have[f] = true end
  local want = {}
  for _, e in ipairs(m.files) do want[e.new] = e.new_sha256 end
  for _, c in ipairs(m.created_files) do want[c.path] = c.sha256 end
  -- the same file set: count and sha256 set, modulo the listed edits
  for p, sha in pairs(want) do
    if not have[p] then
      fails[#fails + 1] = "missing after apply: " .. p
    else
      local got = util.sha256_file(root .. "/" .. p)
      if got ~= sha then fails[#fails + 1] = ("content differs from the plan: %s"):format(p) end
    end
  end
  for _, f in ipairs(files) do
    if not want[f] then fails[#fails + 1] = "unexpected file after apply: " .. f end
  end
  -- every reference that resolved before resolves to the same file after: read each one where the
  -- edits put it in the written file, and resolve what is there
  local view = refs.view(files, dirs)
  local rw_at = {}
  for _, r in ipairs(m.rewrites) do
    local k = r.file .. "\0" .. r.line
    rw_at[k] = rw_at[k] or {}
    table.insert(rw_at[k], r)
  end
  local lines_of = {}
  local function post_lines(p)
    if lines_of[p] == nil then
      local t = util.read_file(root .. "/" .. p)
      lines_of[p] = t and util.split_lines(t) or false
    end
    return lines_of[p] or nil
  end
  local bad_refs = 0
  for _, r in ipairs(plan._refs_resolved) do
    local line, col = r.line, r.col
    for _, op in ipairs(m.normalize[r.old_file] or {}) do
      if op.op == "set_type" and op.kind and op.line < r.line then line = line + 1 end
      if op.op == "rename_key" and op.line == r.line then col = col + #op.to - #op.from end
    end
    for _, o in ipairs(rw_at[r.old_file .. "\0" .. r.line] or {}) do
      if o.col < r.col then col = col + #o.new - #o.old end
    end
    local pl = post_lines(r.file)
    local seg = pl and pl[line] and pl[line]:sub(col, col + #r.text - 1) or nil
    local got = seg and refs.resolve({ kind = r.kind, prefix = r.prefix, how = r.how, old = seg }, r.file, view, seg)
    if seg ~= r.text or got ~= r.new_target then
      bad_refs = bad_refs + 1
      if bad_refs <= 20 then
        fails[#fails + 1] = ("reference no longer resolves to its target: %s:%d found %q, want %q -> %s"):format(
          r.file, line, tostring(seg), r.text, r.new_target)
      end
    end
  end
  if bad_refs > 20 then fails[#fails + 1] = ("… %d references no longer resolve"):format(bad_refs) end
  -- no new broken internal reference
  local created = {}
  for _, c in ipairs(m.created_files) do created[c.path] = true end
  local frozen_new = {}
  for _, e in ipairs(m.files) do
    if e.frozen or e.new:sub(1, 4) == "raw/" or e.new:sub(1, 8) == "archive/" then frozen_new[e.new] = true end
  end
  local px = refs.prefixes(root, m.agent_names or {})
  local new_broken = 0
  for _, f in ipairs(files) do
    if is_md(f) and not created[f] and not frozen_new[f] and f:sub(1, 10) ~= ".obsidian/" then
      local text = util.read_file(root .. "/" .. f)
      for _, r in ipairs(refs.scan(f, text, { px = px, todo = f:sub(1, 11) == ".todo-list/" })) do
        if r.ctx ~= "fence" and r.kind ~= "none" and r.kind ~= "bare" and not (r.kind == "root" and (r.kbrel or "") == "") then
          local got = refs.resolve(r, f, view)
          if not got and not plan._pre_broken[f .. "\0" .. r.old] then
            new_broken = new_broken + 1
            if new_broken <= 20 then
              fails[#fails + 1] = ("new broken reference: %s:%d %q"):format(f, r.line, r.old)
            end
          end
        end
      end
    end
  end
  if new_broken > 20 then fails[#fails + 1] = ("… %d new broken references in all"):format(new_broken) end
  -- schema diagnostics, counted
  local subjects = {}
  for _, f in ipairs(files) do
    if M.schema_subject(f) then subjects[#subjects + 1] = f end
  end
  return fails, schema_counts(root, subjects)
end

-- ─── git ────────────────────────────────────────────────────────────

local function git_paths(m)
  local set = {}
  for _, e in ipairs(m.files) do
    if e.old ~= e.new or e.edited then
      set[e.old] = true
      set[e.new] = true
    end
  end
  for _, c in ipairs(m.created_files) do set[c.path] = true end
  local list = sorted_keys(set)
  return list
end

local function git_stage_and_commit(root, paths, msg)
  -- ignored files stay out of the commit (they were never tracked)
  local code, ign = util.run({ "git", "-C", root, "check-ignore", "--stdin" }, { stdin = table.concat(paths, "\n") .. "\n" })
  local ignored = {}
  if code == 0 then
    for l in ign:gmatch("[^\n]+") do ignored[l] = true end
  end
  local tracked = {}
  local _, ls = util.run({ "git", "-C", root, "ls-files", "-z" })
  for p in ls:gmatch("[^%z]+") do tracked[p] = true end
  local keep = {}
  for _, p in ipairs(paths) do
    -- a pathspec must name a tracked path or an existing file, or git add refuses them all
    if not ignored[p] and (tracked[p] or util.exists(root .. "/" .. p)) then keep[#keep + 1] = p end
  end
  if #keep > 0 then
    local c, _, err = util.run({ "git", "-C", root, "add", "-A", "--pathspec-from-file=-", "--pathspec-file-nul" },
      { stdin = table.concat(keep, "\0") })
    if c ~= 0 then return nil, "git add: " .. err end
  end
  local c, _, err = util.run({ "git", "-C", root, "commit", "-q", "-m", msg })
  if c ~= 0 then return nil, "git commit: " .. err end
  local _, sha = util.run({ "git", "-C", root, "rev-parse", "HEAD" })
  return vim.trim(sha)
end

-- ─── apply ──────────────────────────────────────────────────────────

local function load_manifest(path)
  local m, err = util.read_json(path)
  if not m then return nil, "cannot read the manifest " .. path .. ": " .. tostring(err) end
  if m.version ~= M.MANIFEST_VERSION then return nil, "unsupported manifest version " .. tostring(m.version) end
  return m
end

---Apply a dry run's manifest.
---@param opts table {root, manifest, state_dir, todo_stores, hooks, commit}
function M.apply(opts)
  local root = util.normpath(opts.root)
  local mpath = opts.manifest
  if not mpath then
    local idx = read_index(opts)[root_key(root)]
    mpath = idx and idx.latest_dry_run
    if not mpath then return nil, "no dry run for " .. root .. ": run :AutodocKbMigrate first" end
  end
  local m, err = load_manifest(mpath)
  if not m then return nil, err end
  if m.root ~= root then return nil, ("the manifest is for %s, not %s"):format(m.root, root) end
  local hooks = opts.hooks or {}

  -- re-plan from the same sources: the plan must be byte-for-byte the manifest's
  local plan, perr = M.plan({ root = root, id = m.id, date = m.date, autodoc_version = m.autodoc_version,
    todo_stores = opts.todo_stores, now = opts.now })
  -- 1. every source must be unchanged since the dry run
  local files = util.walk(root, { [".git"] = true })
  local changed = {}
  local listed = {}
  for _, e in ipairs(m.files) do
    listed[e.old] = true
    local got = util.sha256_file(root .. "/" .. e.old)
    if got ~= e.sha256 then changed[#changed + 1] = e.old .. (got and " (changed)" or " (missing)") end
  end
  for _, f in ipairs(files) do
    if not listed[f] then changed[#changed + 1] = f .. " (new since the dry run)" end
  end
  for _, j in ipairs(m.todo_stores.joined) do
    for _, f in ipairs(j.files) do
      if util.sha256_file(j.dir .. "/" .. f.rel) ~= f.sha256 then changed[#changed + 1] = j.dir .. "/" .. f.rel .. " (changed)" end
    end
  end
  if #changed > 0 then
    return nil, "sources changed since the dry run; run it again. " .. table.concat(changed, ", ", 1, math.min(#changed, 20))
  end
  if not plan then return nil, "re-planning failed: " .. tostring(perr) end
  -- the re-plan must agree with the manifest (same code, same sources)
  local mm = util.json_encode(manifest_of(plan))
  local saved = util.json_encode(m)
  if mm ~= saved then return nil, "the manifest does not match a re-plan of unchanged sources (edited manifest or different AutoDoc)" end

  -- 2. preflight targets: nothing may be overwritten
  for _, e in ipairs(m.files) do
    if e.new ~= e.old and util.exists(root .. "/" .. e.new) then return nil, "unexpected existing target: " .. e.new end
  end
  local leaving = {}
  for _, e in ipairs(m.files) do
    if e.new ~= e.old then leaving[e.old] = true end
  end
  for _, c in ipairs(m.created_files) do
    if util.exists(root .. "/" .. c.path) and not leaving[c.path] then return nil, "unexpected existing target: " .. c.path end
  end
  -- git: refuse with staged changes, which a migration commit would sweep in
  if m.git.repo then
    local c = util.run({ "git", "-C", root, "diff", "--cached", "--quiet" })
    if c ~= 0 then return nil, "the KB's git index has staged changes; commit or unstage them first" end
  end

  -- 3. the pre-image, complete and verified before the first write
  local pdir = preimage_dir(opts, m.id)
  local existed = util.exists(pdir)
  local okp, pi = pcall(write_preimage, m, pdir, hooks)
  if not okp then
    -- nothing in the KB changed: a partial pre-image is only clutter
    if not existed then util.rmtree(pdir) end
    return nil, "pre-image: " .. tostring(pi) .. " (nothing in the KB was changed)"
  end
  util.copy_file(mpath, pdir .. "/manifest.json")
  local idx = read_index(opts)
  local key = root_key(root)
  idx[key] = idx[key] or { root = root }
  idx[key].latest_apply = m.id
  write_index(opts, idx)
  if hooks.before_first_write then hooks.before_first_write(pdir, pi) end

  local function step(name)
    if hooks.fail_at == name then error("injected failure at " .. name) end
  end

  local okw, werr = pcall(function()
    -- 4. moves
    for _, e in ipairs(m.files) do
      if e.new ~= e.old then
        -- a move never replaces a file, whatever the plan says (rename(2) would)
        if vim.uv.fs_lstat(root .. "/" .. e.new) then error("refusing to overwrite " .. e.new) end
        util.rename(root .. "/" .. e.old, root .. "/" .. e.new)
      end
    end
    step("moved")
    -- 5. rewrites and normalization, from the manifest's own edit list
    local rw_by_file = {}
    for _, r in ipairs(m.rewrites) do
      rw_by_file[r.file] = rw_by_file[r.file] or {}
      table.insert(rw_by_file[r.file], r)
    end
    for _, e in ipairs(m.files) do
      if e.edited and not e.scaffold_update then
        local p = root .. "/" .. e.new
        local text = assert(util.read_file(p))
        local out = M.apply_edits(text, rw_by_file[e.old], m.normalize[e.old])
        if hooks.mutate_output then out = hooks.mutate_output(e, out) end
        util.write_file(p, out)
      end
    end
    for _, j in ipairs(m.todo_stores.joined) do
      local by = {}
      for _, r in ipairs(j.rewrites) do
        by[r.file] = by[r.file] or {}
        table.insert(by[r.file], r)
      end
      for _, f in ipairs(j.files) do
        local p = j.dir .. "/" .. f.rel
        local out = M.apply_edits(assert(util.read_file(p)), by[f.rel], nil)
        if util.sha256(out) ~= f.new_sha256 then error("todo store edit differs from the plan: " .. p) end
        util.write_file(p, out)
      end
    end
    step("rewritten")
    -- 6. removed directories (deepest first; each must be empty)
    local rd = vim.deepcopy(m.removed_dirs)
    table.sort(rd, function(a, b) return #a > #b end)
    for _, d in ipairs(rd) do
      local ok, e2 = vim.uv.fs_rmdir(root .. "/" .. d)
      if not ok then error("cannot remove directory " .. d .. ": " .. tostring(e2)) end
    end
    step("removed")
    -- 7. scaffold (§8.9)
    local planned = {}
    for _, c in ipairs(m.created_files) do planned[c.path] = true end
    scaffold.scaffold(root, {
      autodoc_version = m.autodoc_version, date = m.date,
      before_create = function(rel)
        if not planned[rel] then error("the scaffold would create an unplanned file: " .. rel) end
      end,
    })
    step("scaffolded")
  end)

  local fails, diags = {}, {}
  if okw then
    fails, diags = validate(m, plan, root)
    if hooks.after_validate then hooks.after_validate(fails) end
  else
    fails = { tostring(werr) }
  end

  if #fails == 0 and m.git.repo and opts.commit ~= false then
    local sha, gerr = git_stage_and_commit(root, git_paths(m),
      "kb: migrate to the v2 layout (ADR 1791209946 §8)\n\nAutoDoc migration " .. m.id .. ".\n")
    if not sha then
      fails = { gerr }
      util.run({ "git", "-C", root, "reset", "-q" })
    else
      pi.git.commit = sha
    end
  end

  if #fails > 0 then
    local problems = restore(pi, { pdir = pdir })
    pi.status = "rolled-back"
    pi.rollback_problems = problems
    save_preimage_index(pdir, pi)
    return nil, "apply failed and was rolled back: " .. table.concat(fails, "; ", 1, math.min(#fails, 10))
      .. (#problems > 0 and (" — ROLLBACK PROBLEMS: " .. table.concat(problems, "; ")) or ""), { fails = fails, rollback_problems = problems }
  end

  -- 8. record what the apply left, for --undo's edit check
  pi.post = {}
  for _, en in ipairs(pi.entries) do
    local base = en.scope == "kb" and root or en.root
    local abs = base .. "/" .. en.new
    pi.post[abs] = util.sha256_file(abs)
  end
  for _, c in ipairs(m.created_files) do pi.post[root .. "/" .. c.path] = c.sha256 end
  pi.status = "applied"
  pi.schema_diagnostics = diags
  save_preimage_index(pdir, pi)
  return { id = m.id, preimage = pdir, schema_diagnostics = diags, commit = pi.git.commit }
end

-- ─── undo / forget ──────────────────────────────────────────────────

local function find_preimage(opts)
  local root = util.normpath(opts.root)
  local id = opts.id
  if not id then
    local idx = read_index(opts)[root_key(root)]
    id = idx and idx.latest_apply
  end
  if not id then return nil, "no applied migration recorded for " .. root end
  local pdir = preimage_dir(opts, id)
  local pi = util.read_json(pdir .. "/preimage.json")
  if not pi then return nil, "no pre-image at " .. pdir end
  return pi, pdir
end

---Restore the pre-image of the latest apply (or opts.id).
---@param opts table {root, id, keep_edited, confirm = fn(abs_path) -> bool, commit}
function M.undo(opts)
  local pi, pdir = find_preimage(opts)
  if not pi then return nil, pdir end
  if pi.status ~= "applied" then return nil, "the migration's state is " .. tostring(pi.status) .. ", not applied" end
  local edited = {}
  for _, abs in ipairs(sorted_keys(pi.post or {})) do
    if util.sha256_file(abs) ~= pi.post[abs] then edited[#edited + 1] = abs end
  end
  local skip = {}
  if #edited > 0 then
    if opts.keep_edited then
      for _, a in ipairs(edited) do skip[a] = true end
    elseif opts.confirm then
      for _, a in ipairs(edited) do
        if not opts.confirm(a) then
          return nil, "undo refused: " .. a .. " was edited since the apply (not confirmed)", { edited = edited }
        end
      end
    else
      return nil, "undo refused: files edited since the apply: " .. table.concat(edited, ", ")
        .. " (confirm each, or pass --keep-edited to skip them)", { edited = edited }
    end
  end
  local problems = restore(pi, { pdir = pdir, skip = skip })
  local commit
  if pi.git and pi.git.repo and opts.commit ~= false then
    local m = util.read_json(pdir .. "/manifest.json")
    if m then
      local sha, gerr = git_stage_and_commit(pi.root, git_paths(m), "kb: undo AutoDoc migration " .. pi.id .. "\n")
      if not sha then problems[#problems + 1] = gerr else commit = sha end
    end
  end
  pi.status = "undone"
  pi.undo = { kept_edited = edited and opts.keep_edited and edited or nil, problems = problems, commit = commit }
  save_preimage_index(pdir, pi)
  return { id = pi.id, problems = problems, kept = opts.keep_edited and edited or {}, commit = commit }
end

function M.forget(opts)
  local pi, pdir = find_preimage(opts)
  if not pi then return nil, pdir end
  util.rmtree(pdir)
  local idx = read_index(opts)
  local key = root_key(util.normpath(opts.root))
  if idx[key] and idx[key].latest_apply == pi.id then idx[key].latest_apply = nil end
  write_index(opts, idx)
  return { id = pi.id, removed = pdir }
end

-- ─── entry points ───────────────────────────────────────────────────

---Run a mode: opts.mode is "dry" (default), "apply", "undo" or "forget".
function M.run(opts)
  opts = opts or {}
  if not opts.root or opts.root == "" then
    local okc, kb = pcall(require, "auto-core.kb")
    if okc and type(kb) == "table" and type(kb.primary) == "function" then
      local okp, r = pcall(kb.primary)
      if okp and type(r) == "table" then r = r.root end
      if okp and type(r) == "string" then opts.root = r end
    end
    opts.root = opts.root or vim.env.AUTO_AGENTS_KB_ROOT
    if not opts.root or opts.root == "" then return nil, "no KB root: pass one" end
  end
  opts.root = util.normpath(vim.fn.fnamemodify(vim.fn.expand(opts.root), ":p"):gsub("/$", ""))
  local mode = opts.mode or "dry"
  if mode == "dry" then return M.dry_run(opts) end
  if mode == "apply" then return M.apply(opts) end
  if mode == "undo" then return M.undo(opts) end
  if mode == "forget" then return M.forget(opts) end
  return nil, "unknown mode " .. tostring(mode)
end

---Parse `:AutodocKbMigrate` arguments.
function M.parse_args(fargs)
  local opts = { mode = "dry" }
  for _, a in ipairs(fargs or {}) do
    if a == "--apply" then opts.mode = "apply"
    elseif a == "--undo" then opts.mode = "undo"
    elseif a == "--forget" then opts.mode = "forget"
    elseif a == "--keep-edited" then opts.keep_edited = true
    elseif a == "--no-commit" then opts.commit = false
    elseif a:match("^%-%-out=") then opts.out_dir = a:sub(7)
    elseif a:match("^%-%-manifest=") then opts.manifest = a:sub(12)
    elseif a:match("^%-%-id=") then opts.id = a:sub(6)
    elseif a:sub(1, 2) == "--" then error("unknown flag " .. a)
    else opts.root = a end
  end
  return opts
end

---The `:AutodocKbMigrate` command body.
function M.command(fargs)
  local ok, opts = pcall(M.parse_args, fargs)
  if not ok then
    vim.notify("AutodocKbMigrate: " .. tostring(opts), vim.log.levels.ERROR)
    return
  end
  if opts.mode == "undo" and not opts.keep_edited then
    opts.confirm = function(abs)
      return vim.fn.confirm("Edited since the migration: " .. abs .. "\nOverwrite it with its pre-migration copy?", "&Yes\n&No", 2) == 1
    end
  end
  local res, err = M.run(opts)
  if not res then
    vim.notify("AutodocKbMigrate: " .. tostring(err), vim.log.levels.ERROR)
    return
  end
  if res.summary then
    vim.notify(res.summary .. "\nmanifest: " .. res.manifest .. "\ndiff: " .. res.out_dir .. "/migration.diff")
  else
    vim.notify("AutodocKbMigrate " .. opts.mode .. ": " .. vim.inspect(res))
  end
  return res
end

---Define the user command (the main plugin calls this from its setup).
function M.register()
  vim.api.nvim_create_user_command("AutodocKbMigrate", function(a) M.command(a.fargs) end, {
    nargs = "*",
    complete = function() return { "--apply", "--undo", "--forget", "--keep-edited", "--no-commit", "--out=", "--manifest=", "--id=" } end,
    desc = "AutoDoc: migrate a KB to the v2 layout (dry run, --apply, --undo, --forget)",
  })
end

return M
