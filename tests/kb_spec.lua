-- AutoDoc KB tooling: scaffold, templates and :AutodocKbMigrate (ADR 1791209946 §1–§4, §8, §9).
--
-- Run (tests/run-all.sh does this inside an XDG sandbox):
--   AUTO_CORE=<auto-core.nvim checkout> nvim --headless -u NONE -l tests/kb_spec.lua
--
-- Every §9 mutant has a cell here. The fixture KB is built in a temp dir and exercises every
-- reference class: path-qualified wikilinks (root, ../../ relative, suffix), relative Markdown links,
-- bare paths in prose and inline code, $KB_ROOT and absolute KB paths, frontmatter paths under
-- arbitrary keys, todo YAML adr/review and a second --- block, fenced code, archive-bound files,
-- raw/, a collision, an external todo store, and a non-git and a git variant.

local here = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
vim.opt.runtimepath:append(here)
local auto_core = os.getenv("AUTO_CORE") or "/home/johno/Source/Projects/nvim-plugins/auto-core.nvim/main"
vim.opt.runtimepath:append(auto_core)

local util = require("autodoc.kb.util")
local scaffold = require("autodoc.kb.scaffold")
local schema = require("autodoc.kb.schema")
local migrate = require("autodoc.kb.migrate")
local refs = require("autodoc.kb.refs")

local pass, fail = 0, 0
local function ok(name, cond, detail)
  if cond then
    pass = pass + 1
    print("  PASS  " .. name)
  else
    fail = fail + 1
    print("  FAIL  " .. name .. (detail ~= nil and ("  — " .. tostring(detail)) or ""))
  end
end

local function section(s) print("\n" .. s) end

local tmp = vim.fn.tempname() .. "-kbspec"
util.mkdirp(tmp)
local state_dir = tmp .. "/state"

local function write(root, rel, text)
  util.write_file(root .. "/" .. rel, text)
end

local function read(root, rel)
  return util.read_file(root .. "/" .. rel)
end

---rel -> sha256 for every file, and the directory list
local function snapshot(root)
  local files, dirs = util.walk(root, { [".git"] = true })
  local m = {}
  for _, f in ipairs(files) do m[f] = util.sha256_file(root .. "/" .. f) end
  return { files = m, dirs = dirs }
end

local function same_snapshot(a, b)
  for k, v in pairs(a.files) do
    if b.files[k] ~= v then return false, "file " .. k .. (b.files[k] and " differs" or " missing") end
  end
  for k in pairs(b.files) do
    if not a.files[k] then return false, "extra file " .. k end
  end
  if table.concat(a.dirs, "\n") ~= table.concat(b.dirs, "\n") then
    local sa, sb = {}, {}
    for _, d in ipairs(a.dirs) do sa[d] = true end
    for _, d in ipairs(b.dirs) do sb[d] = true end
    for d in pairs(sa) do if not sb[d] then return false, "directory missing: " .. d end end
    for d in pairs(sb) do if not sa[d] then return false, "extra directory: " .. d end end
  end
  return true
end

-- ─── the fixture ────────────────────────────────────────────────────

local function build_fixture(root, ext)
  util.mkdirp(root)
  write(root, "AGENTS.md", "# Old agent contract\n\nSee [[shared/conventions/naming]] and log.md.\n")
  write(root, "CLAUDE.md", "# CLAUDE\n\nCanonical instructions live in [AGENTS.md](./AGENTS.md).\n")
  write(root, "RULES.md", "# My rules\n\n1. Johno's own rule, never rewritten.\n")
  write(root, "README.md", "# KB\n\nStart at [the first ADR](shared/adrs/0001-first.md).\n")
  write(root, "index.md", "# Index\n\n- [[shared/adrs/0001-first]]\n")
  write(root, "MIGRATIONS.md", "---\ntype: kb\nstatus: active\ndate: 2026-05-01\ntags: [kb]\nabstract: \"Migrations.\"\n---\n\n# Migrations\n")
  write(root, "log.md", "## [2026-05-01] op | wrote shared/adrs/0001-first.md\n")
  write(root, "KB_RULES.md", "# KB rules\n\nR1: append to log.md.\n")
  write(root, "raw/source.md", "Raw notes citing [[shared/adrs/0001-first]] and shared/adrs/0002-second.md.\n")
  write(root, "shared/adrs/0001-first.md", table.concat({
    "---",
    "type: adr",
    'number: "0001"',
    "status: accepted",
    "created: 2026-05-01",
    "tags: [kb]",
    'abstract: "The first decision."',
    "superseded-by: shared/adrs/0002-second.md",
    "builds-on: ../conventions/naming.md",
    "---",
    "",
    "# ADR 0001",
    "",
    "Follows [[shared/conventions/naming]] and [[../../agents/jarvis/reviews/r1|the review]].",
    "Suffix form: [[adrs/0002-second]]. Bare name: [[naming]].",
    "See [naming](../conventions/naming.md) and the playbook `shared/playbooks/release.md`.",
    "Lesson: shared/synthesis/lesson.md, and $KB_ROOT/shared/adrs/0002-second.md.",
    "Absolute: " .. root .. "/shared/conventions/naming.md.",
    "Folder: shared/glossary/ and agents/jarvis/ (split).",
    "",
    "```sh",
    "cat shared/adrs/0002-second.md",
    "```",
    "",
  }, "\n"))
  write(root, "shared/adrs/0002-second.md", "---\ntype: adr\nnumber: \"0002\"\nstatus: proposed\ncreated: 2026-05-02\ntags: [kb]\nabstract: \"Second.\"\n---\n\n# ADR 0002\n\nLessons live in shared/synthesis/.\n")
  write(root, "shared/conventions/naming.md", "---\ntype: convention\nstatus: active\ncreated: 2026-05-01\ntags: [kb]\nabstract: \"Naming.\"\n---\n\n# Naming\n")
  write(root, "shared/playbooks/release.md", "---\ntype: playbook\nstatus: active\ncreated: 2026-05-01\ntags: [kb]\nabstract: \"Release.\"\n---\n\n# Release\n")
  write(root, "shared/synthesis/lesson.md", "---\ntype: synthesis\nstatus: final\ncreated: 2026-05-01\ntags: [kb]\nabstract: \"Lesson.\"\n---\n\nUp: [first](../adrs/0001-first.md#decision).\n")
  write(root, "shared/synthesis/archive/old.md", "Old synthesis citing [[shared/adrs/0001-first]].\n")
  write(root, "shared/glossary/term.md", "---\ntype: glossary\nstatus: active\ncreated: 2026-05-01\ntags: [kb]\nabstract: \"A glossary term.\"\n---\n\nglossary term\n")
  write(root, "shared/reference/term.md", "---\ntype: reference\nstatus: active\ncreated: 2026-05-01\ntags: [kb]\nabstract: \"A reference term.\"\n---\n\nreference term\n")
  write(root, "shared/bug-fixes/bug.md", "---\ntype: bug-fix\nstatus: final\ncreated: 2026-05-01\ntags: [kb]\nabstract: \"A bug.\"\nrelated-adrs: [shared/adrs/0001-first.md]\n---\n\n# Bug\n")
  write(root, "shared/scripts/run.sh", "#!/bin/sh\necho run\n")
  vim.uv.fs_chmod(root .. "/shared/scripts/run.sh", 493)
  util.mkdirp(root .. "/shared/agents/lector")
  write(root, "agents/jarvis/reviews/r1.md", table.concat({
    "---",
    "type: review-response",
    "status: completed",
    "created: 2026-05-03",
    "tags: [review]",
    'abstract: "A review."',
    "reviewer: jarvis",
    "subject: adr 0001",
    "head_sha: abc123",
    'sources: ["agents/jarvis/tasks/t1.md"]',
    "anykey: ../../../shared/adrs/0002-second.md",
    "---",
    "",
    "Reviewed [[shared/adrs/0001-first]].",
    "",
  }, "\n"))
  write(root, "agents/jarvis/tasks/t1.md", "---\ntype: task\nstatus: active\ncreated: 2026-05-03\ntags: [task]\nabstract: \"A task.\"\n---\n\nTask.\n")
  write(root, "agents/jarvis/loose.md", "Loose note: [first](../../shared/adrs/0001-first.md).\n")
  write(root, "log/2026-W01.md", "week one, wrote shared/adrs/0001-first.md\n")
  write(root, ".todo-list/open/task-a.md", table.concat({
    "---",
    'id: "task-a"',
    "version: 1",
    "status: open",
    'title: "Task A"',
    "adr:",
    "  - $KB_ROOT/shared/adrs/0001-first.md",
    "review:",
    "  - shared/conventions/naming.md",
    "---",
    "",
    "Body.",
    "",
    "---",
    "legacy: ../../shared/playbooks/release.md",
    "---",
    "",
  }, "\n"))
  if ext then
    write(ext, "open/task-b.md", table.concat({
      "---",
      'id: "task-b"',
      "status: open",
      "adr:",
      "  - " .. root .. "/shared/adrs/0001-first.md",
      "review:",
      "  - $KB_ROOT/shared/conventions/naming.md",
      "---",
      "",
    }, "\n"))
  end
end

-- ─── scaffold ───────────────────────────────────────────────────────

section("[S] scaffold and templates")
do
  local root = tmp .. "/fresh"
  local r = scaffold.scaffold(root, { autodoc_version = "0.2.0", date = "2026-10-06" })
  ok("S1: a fresh scaffold creates every layout folder with its ABOUT.md", (function()
    for _, d in ipairs(scaffold.DIRS) do
      if not util.isfile(root .. "/" .. d .. "/ABOUT.md") then return false end
    end
    return true
  end)())
  ok("S2: the scaffold never creates .todo-list/", not util.exists(root .. "/.todo-list"))
  ok("S3: root files and the managed files are created",
    util.isfile(root .. "/RULES.md") and util.isfile(root .. "/AGENTS.md") and util.isfile(root .. "/KB_OPERATIONS.md")
    and util.isfile(root .. "/CLAUDE.md") and util.isfile(root .. "/GEMINI.md") and util.isfile(root .. "/_schema/frontmatter.yaml"))
  ok("S4: KB_OPERATIONS.md carries the version it was written with",
    scaffold.declared_version(read(root, "KB_OPERATIONS.md")) == "0.2.0")
  ok("S5: the schema carries the version it was written with",
    scaffold.declared_version(read(root, "_schema/frontmatter.yaml")) == "0.2.0")
  local s = schema.shipped()
  local bad = {}
  for _, t in ipairs(s.type_order) do
    local text = read(root, "_templates/" .. t .. ".md")
    if not text then
      bad[#bad + 1] = t .. " (missing)"
    else
      local filled = text:gsub("{{date}}", "2026-10-06"):gsub("{{title}}", "T")
      if #schema.validate(s, filled) > 0 then bad[#bad + 1] = t end
    end
  end
  ok("S6: one template per schema type, each valid against the schema once filled", #bad == 0 and #s.type_order == 11, table.concat(bad, ","))
  local invalid = {}
  for _, f in ipairs(util.walk(root)) do
    if f:match("%.md$") and not f:match("^_templates/") and #schema.validate(s, read(root, f)) > 0 then invalid[#invalid + 1] = f end
  end
  ok("S7: every scaffolded document is valid against the schema", #invalid == 0, table.concat(invalid, ","))
  local r2 = scaffold.scaffold(root, { autodoc_version = "0.2.0", date = "2026-10-07" })
  ok("S8: a second scaffold is idempotent (nothing created or updated)", #r2.created == 0 and #r2.updated == 0, vim.inspect(r2.created))
  ok("S9: report lists created and kept", #r.created == 30 and #r2.kept == 30, #r.created .. "/" .. #r2.kept)

  -- RULES.md is never rewritten
  write(root, "RULES.md", "# Mine\n\nautodoc_version: 0.0.1\n")
  local before = read(root, "RULES.md")
  scaffold.scaffold(root, { autodoc_version = "9.0.0" })
  ok("S10: RULES.md is never rewritten, even by a newer AutoDoc", read(root, "RULES.md") == before)
  -- AGENTS.md (the KB's own contract) is not managed either
  write(root, "AGENTS.md", "# custom\n")
  scaffold.scaffold(root, { autodoc_version = "9.0.0" })
  ok("S11: AGENTS.md is never overwritten", read(root, "AGENTS.md") == "# custom\n")

  -- managed files refresh only when the KB's copy is older
  local function managed_case(have, ship)
    local d = tmp .. "/managed-" .. have:gsub("%W", "_") .. "-" .. ship:gsub("%W", "_")
    scaffold.scaffold(d, { autodoc_version = have, date = "2026-10-06" })
    local ops = read(d, "KB_OPERATIONS.md")
    local sch = read(d, "_schema/frontmatter.yaml")
    local rep = scaffold.scaffold(d, { autodoc_version = ship, date = "2026-10-06" })
    return read(d, "KB_OPERATIONS.md") ~= ops, read(d, "_schema/frontmatter.yaml") ~= sch, rep
  end
  local o1, s1, rep1 = managed_case("0.1.9", "0.1.10")
  ok("S12: managed files are refreshed when the KB's copy is older (0.1.9 < 0.1.10, a semver compare)", o1 and s1 and #rep1.updated == 2, vim.inspect(rep1.updated))
  local o2, s2 = managed_case("0.2.0", "0.2.0")
  ok("S13: managed files are kept at the same version", not o2 and not s2)
  local o3, s3 = managed_case("0.3.0", "0.2.0")
  ok("S14: managed files are kept when the KB's copy is newer", not o3 and not s3)
  local d4 = tmp .. "/managed-noversion"
  util.mkdirp(d4)
  write(d4, "KB_OPERATIONS.md", "# hand written\n")
  scaffold.scaffold(d4, { autodoc_version = "0.2.0" })
  ok("S15: a managed file with no readable version is kept", read(d4, "KB_OPERATIONS.md") == "# hand written\n")
  -- raw/ is the user's: no ABOUT.md written into an existing non-empty raw/
  local d5 = tmp .. "/rawkb"
  write(d5, "raw/x.txt", "x")
  local rep5 = scaffold.scaffold(d5, {})
  ok("S16: the scaffold writes nothing into an existing non-empty raw/", not util.exists(d5 .. "/raw/ABOUT.md") and vim.tbl_contains(rep5.skipped, "raw/ABOUT.md"))
end

-- ─── reference scanning ─────────────────────────────────────────────

section("[R] reference scanner")
do
  local px = refs.prefixes("/kb", { "jarvis" })
  local function scan(text, todo) return refs.scan("shared/adrs/x.md", text, { px = px, todo = todo }) end
  local r = scan("a [[shared/adrs/y|alias]] b")
  ok("R1: a path-qualified wikilink is found, its span the path only", r[1] and r[1].sub == "path:shared/" and r[1].old == "shared/adrs/y")
  r = scan("see [t](../c/n.md#h \"title\")")
  ok("R2: a relative Markdown link is found with its target", r[1] and r[1].kind == "rel" and r[1].old == "../c/n.md#h")
  r = scan("`shared/playbooks/p.md`, and shared/adrs/z.md.")
  ok("R3: bare paths in inline code and prose, trailing punctuation dropped",
    #r == 2 and r[1].ctx == "code" and r[2].ctx == "prose" and r[2].old == "shared/adrs/z.md", vim.inspect(r))
  r = scan("```\nshared/adrs/z.md\n```\n")
  ok("R4: fenced code is classified fence", r[1] and r[1].ctx == "fence")
  r = scan("https://github.com/x/shared/adrs/z.md and /kb2/shared/x and /kb/shared/adrs/q.md")
  ok("R5: URLs and look-alike roots are not KB paths; the real root is", #r == 1 and r[1].sub == "abs-kb" and r[1].kbrel == "shared/adrs/q.md", vim.inspect(r))
  r = scan("---\nsources: ../../shared/adrs/q.md\n---\n")
  ok("R6: a ../../ relative path in frontmatter is found", r[1] and r[1].kind == "fmrel" and r[1].fm_key == "sources", vim.inspect(r))
  r = scan("---\nid: x\n---\n\nbody\n\n---\nlegacy: ../a/b.md\n---\n", true)
  ok("R7: a todo body's second --- block counts as frontmatter", r[1] and r[1].kind == "fmrel" and r[1].fm_key == "legacy", vim.inspect(r))
  r = scan("agents/jarvis/reviews/a.md and agents/nobody/x.md")
  ok("R8: agents/<n>/ matches known agents only", #r == 1 and r[1].sub == "agents/<n>/")
end

-- ─── the migration ──────────────────────────────────────────────────

local function dry(root, ext, extra)
  local o = { root = root, mode = "dry", state_dir = state_dir, out_dir = tmp .. "/out-" .. vim.fn.fnamemodify(root, ":t"),
    todo_stores = { root .. "/.todo-list", ext }, date = "2026-10-06" }
  for k, v in pairs(extra or {}) do o[k] = v end
  return migrate.run(o)
end

local function apply(root, ext, extra)
  local o = { root = root, mode = "apply", state_dir = state_dir, todo_stores = { root .. "/.todo-list", ext } }
  for k, v in pairs(extra or {}) do o[k] = v end
  return migrate.run(o)
end

local function undo(root, extra)
  local o = { root = root, mode = "undo", state_dir = state_dir }
  for k, v in pairs(extra or {}) do o[k] = v end
  return migrate.run(o)
end

section("[M] dry run")
local A = tmp .. "/kb-a"
local EXT_A = tmp .. "/ext-a"
build_fixture(A, EXT_A)
local snapA = snapshot(A)
local snapExtA = snapshot(EXT_A)
local res, err = dry(A, EXT_A)
ok("M1: the dry run succeeds", res ~= nil, err)
local plan = res and res.plan or {}
local function file_entry(old)
  for _, e in ipairs(plan.files or {}) do
    if e.old == old then return e end
  end
end
local function rewrite_of(file, old)
  for _, r in ipairs(plan.rewrites or {}) do
    if r.file == file and r.old == old then return r end
  end
end
if res then
  ok("M2: the dry run changes nothing in the KB or the external store", same_snapshot(snapA, snapshot(A)) and same_snapshot(snapExtA, snapshot(EXT_A)))
  ok("M3: it writes the manifest, the diff and the summary outside the KB",
    util.isfile(res.manifest) and util.isfile(res.out_dir .. "/migration.diff") and util.isfile(res.out_dir .. "/summary.txt"))
  local m = util.read_json(res.manifest)
  ok("M4: the manifest lists every file with old path, new path and sha256", m and #m.files == vim.tbl_count(snapA.files)
    and m.files[1].old and m.files[1].new and #m.files[1].sha256 == 64)
  local wl = rewrite_of("shared/adrs/0001-first.md", "../../agents/jarvis/reviews/r1")
  ok("M5: a ../../ wikilink is re-relativized from the NEW location", wl and wl.new == "../reviews/jarvis/r1", wl and wl.new)
  local wr = rewrite_of("shared/adrs/0001-first.md", "shared/conventions/naming")
  ok("M6: a root path-qualified wikilink keeps its form (no .md)", wr and wr.new == "conventions/naming", wr and wr.new)
  ok("M7: a suffix wikilink that still resolves after the move is left alone", rewrite_of("shared/adrs/0001-first.md", "adrs/0002-second") == nil)
  local ml = rewrite_of("agents/jarvis/loose.md", "../../shared/adrs/0001-first.md")
  ok("M8: a relative Markdown link is re-relativized from the new location", ml and ml.new == "../adrs/0001-first.md", ml and ml.new)
  ok("M9: a relative link whose source and target moved together is unchanged",
    rewrite_of("shared/synthesis/lesson.md", "../adrs/0001-first.md#decision") == nil)
  local bc = rewrite_of("shared/adrs/0001-first.md", "shared/playbooks/release.md")
  ok("M10: a bare path in inline code is rewritten", bc and bc.new == "playbooks/release.md" and bc.ctx == "code")
  local kb = rewrite_of("shared/adrs/0001-first.md", "$KB_ROOT/shared/adrs/0002-second.md")
  ok("M11: a $KB_ROOT path keeps its prefix", kb and kb.new == "$KB_ROOT/adrs/0002-second.md")
  local ab = rewrite_of("shared/adrs/0001-first.md", A .. "/shared/conventions/naming.md")
  ok("M12: an absolute KB path is rewritten", ab and ab.new == A .. "/conventions/naming.md")
  local fk = rewrite_of("agents/jarvis/reviews/r1.md", "../../../shared/adrs/0002-second.md")
  ok("M13: a frontmatter path under an arbitrary key is re-relativized", fk and fk.new == "../../adrs/0002-second.md" and fk.fm_key == "anykey", fk and fk.new)
  local ty = rewrite_of(".todo-list/open/task-a.md", "$KB_ROOT/shared/adrs/0001-first.md")
  local tr = rewrite_of(".todo-list/open/task-a.md", "shared/conventions/naming.md")
  local t2 = rewrite_of(".todo-list/open/task-a.md", "../../shared/playbooks/release.md")
  ok("M14: the todo store's adr/review YAML and a second --- block are rewritten (link maintenance only)",
    ty and ty.new == "$KB_ROOT/adrs/0001-first.md" and tr and tr.new == "conventions/naming.md" and t2 and t2.new == "../../playbooks/release.md")
  ok("M15: the todo store stays in place", file_entry(".todo-list/open/task-a.md").new == ".todo-list/open/task-a.md")
  local fenced = false
  for _, f in ipairs(plan.reported.fenced) do
    if f.file == "shared/adrs/0001-first.md" and f.old == "shared/adrs/0002-second.md" then fenced = true end
  end
  local fence_rw = false
  for _, r in ipairs(plan.rewrites) do
    if r.ctx == "fence" then fence_rw = true end
  end
  ok("M16: fenced code is reported, not rewritten", fenced and not fence_rw)
  local bare_kept = true
  for _, r in ipairs(plan.rewrites) do
    if r.old == "naming" then bare_kept = false end
  end
  ok("M17: a bare-name wikilink is left alone", bare_kept)
  local split = false
  for _, r in ipairs(plan.reported.split_dirs) do
    if r.old == "agents/jarvis/" then split = true end
  end
  ok("M18: a folder mention whose files split across folders is reported, not guessed", split)
  ok("M19: a mention of a merged folder maps (shared/glossary/ -> reference/)",
    (rewrite_of("shared/adrs/0001-first.md", "shared/glossary/") or {}).new == "reference/")
  ok("M19b: shared/synthesis/ maps to synthesis/ despite its archive/ carve-out",
    (rewrite_of("shared/adrs/0002-second.md", "shared/synthesis/") or {}).new == "synthesis/")
  ok("M20: references into AGENTS.md keep pointing at AGENTS.md (it is replaced in place)",
    rewrite_of("CLAUDE.md", "./AGENTS.md") == nil and file_entry("AGENTS.md").new == "archive/AGENTS.md")
  ok("M21: archive-bound files are frozen (moved, never rewritten)",
    file_entry("index.md").new == "archive/index.md" and not file_entry("index.md").edited
    and file_entry("shared/synthesis/archive/old.md").new == "archive/synthesis/old.md" and not file_entry("shared/synthesis/archive/old.md").edited)
  ok("M22: raw/ is not touched by the plan", file_entry("raw/source.md").new == "raw/source.md" and not file_entry("raw/source.md").edited)
  local coll = plan.collisions[1]
  ok("M23: a collision gets a deterministic path-derived suffix for every mover",
    #plan.collisions == 1 and file_entry("shared/glossary/term.md").new == "reference/term--from-shared-glossary.md"
    and file_entry("shared/reference/term.md").new == "reference/term--from-shared-reference.md", vim.inspect(coll))
  local n = plan.normalize["shared/bug-fixes/bug.md"] or {}
  ok("M24: frontmatter normalization: bug-fix -> synthesis with kind, related-adrs -> related",
    #n == 2 and n[1].op == "set_type" and n[1].to == "synthesis" and n[1].kind == "bug-fix" and n[2].to == "related", vim.inspect(n))
  local n2 = plan.normalize["agents/jarvis/reviews/r1.md"] or {}
  ok("M25: review-response -> review, head_sha -> head", #n2 == 2 and n2[1].to == "review" and n2[2].to == "head", vim.inspect(n2))
  ok("M26: the empty shared/agents/* folders are removed, and emptied source folders too",
    vim.tbl_contains(plan.removed_dirs, "shared/agents/lector") and vim.tbl_contains(plan.removed_dirs, "agents"))
  ok("M27: the scaffold plan creates ABOUT.md files and a new AGENTS.md, keeps RULES.md and CLAUDE.md",
    vim.tbl_contains(plan.scaffold.created, "adrs/ABOUT.md") and vim.tbl_contains(plan.scaffold.created, "AGENTS.md")
    and vim.tbl_contains(plan.scaffold.kept, "RULES.md") and vim.tbl_contains(plan.scaffold.kept, "CLAUDE.md"))
  ok("M28: raw/ABOUT.md is not planned into an existing raw/", vim.tbl_contains(plan.scaffold.skipped, "raw/ABOUT.md"))
  local joined = plan.todo_stores.joined[1]
  ok("M29: an external todo store that references the KB joins the manifest",
    joined and joined.dir == EXT_A and #joined.files == 1 and #joined.rewrites == 2, vim.inspect(plan.todo_stores.checked))
  ok("M30: the diff carries renames, edits and created files",
    (function()
      local d = read(res.out_dir, "migration.diff")
      return d:find("rename to adrs/0001%-first.md") and d:find("%+Follows %[%[conventions/naming%]%]") and d:find("new file")
    end)())
end

section("[A] apply guards")
do
  -- a source changed after the dry run: apply aborts, nothing changes
  local B = tmp .. "/kb-b"
  build_fixture(B, nil)
  local r1 = dry(B, nil)
  write(B, "shared/adrs/0002-second.md", read(B, "shared/adrs/0002-second.md") .. "edited after the dry run\n")
  local snapB = snapshot(B)
  local r2, e2 = apply(B, nil)
  ok("A1: apply aborts when a source changed after the dry run", r1 ~= nil and r2 == nil and tostring(e2):find("changed since the dry run") ~= nil, e2)
  ok("A2: …and nothing in the KB changed, and no pre-image was written",
    same_snapshot(snapB, snapshot(B)) and not util.exists(state_dir .. "/migrations/" .. r1.plan.id))
  -- a file that appeared since the dry run (an unexpected target): aborts
  local r3 = dry(B, nil)
  write(B, "adrs/0001-first.md", "squatter\n")
  local snapB2 = snapshot(B)
  local r4, e4 = apply(B, nil)
  ok("A3: an unexpected existing target aborts the apply, overwriting nothing",
    r3 ~= nil and r4 == nil and read(B, "adrs/0001-first.md") == "squatter\n" and same_snapshot(snapB2, snapshot(B)), e4)
  util.remove(B .. "/adrs/0001-first.md")
  vim.uv.fs_rmdir(B .. "/adrs")

  -- an incomplete pre-image aborts with nothing changed
  local r5 = dry(B, nil)
  local snapB3 = snapshot(B)
  local r6, e6 = apply(B, nil, { hooks = { after_preimage_copies = function(pdir, pi)
    util.write_file(pdir .. "/" .. pi.entries[1].copy, "torn copy")
  end } })
  ok("A4: a pre-image copy that fails verification aborts the apply with nothing changed",
    r5 ~= nil and r6 == nil and tostring(e6):find("pre%-image incomplete") ~= nil and same_snapshot(snapB3, snapshot(B)), e6)

  -- an injected failure after the first writes rolls back to the exact pre-image
  local r7 = dry(B, nil)
  local snapB4 = snapshot(B)
  local r8, e8 = apply(B, nil, { hooks = { fail_at = "rewritten" } })
  ok("A5: a failure mid-apply rolls the KB back byte for byte", r7 ~= nil and r8 == nil and tostring(e8):find("rolled back") ~= nil
    and same_snapshot(snapB4, snapshot(B)), (select(2, same_snapshot(snapB4, snapshot(B)))) or e8)

  -- the post-apply validator catches a broken reference (and the apply rolls back)
  local r9 = dry(B, nil)
  local r10, e10 = apply(B, nil, { hooks = { mutate_output = function(e, out)
    if e.old == "shared/adrs/0001-first.md" then return (out:gsub("%[%[conventions/naming%]%]", "[[conventions/nameing]]")) end
    return out
  end } })
  ok("A6: the validator catches a reference that no longer resolves, and the apply rolls back",
    r9 ~= nil and r10 == nil and tostring(e10):find("no longer resolves") ~= nil and same_snapshot(snapB4, snapshot(B)), e10)
  local r11 = dry(B, nil)
  local r12, e12 = apply(B, nil, { hooks = { mutate_output = function(e, out)
    if e.old == "agents/jarvis/loose.md" then return out .. "\nNew: [[adrs/does-not-exist]]\n" end
    return out
  end } })
  ok("A7: the validator catches a new broken internal reference", r11 ~= nil and r12 == nil and tostring(e12):find("new broken reference") ~= nil, e12)
end

section("[P] pre-image, apply and undo (no git)")
local pre_ok, pre_detail = false, "hook never ran"
local r_apply, e_apply = apply(A, EXT_A, { hooks = { before_first_write = function(pdir, pi)
  -- the KB must be untouched, and the pre-image complete and verified
  local same, why = same_snapshot(snapA, snapshot(A))
  if not same then
    pre_detail = "the KB changed before the pre-image was complete: " .. why
    return
  end
  if pi.status ~= "ready" then
    pre_detail = "status " .. tostring(pi.status)
    return
  end
  local need = {}
  for _, e in ipairs(plan.files) do
    if e.old ~= e.new or e.edited then need["kb:" .. e.old] = e.sha256 end
  end
  for _, j in ipairs(plan.todo_stores.joined) do
    for _, f in ipairs(j.files) do need["ext:" .. j.dir .. "/" .. f.rel] = f.sha256 end
  end
  for _, en in ipairs(pi.entries) do
    local k = en.scope == "kb" and ("kb:" .. en.rel) or ("ext:" .. en.root .. "/" .. en.rel)
    if need[k] and util.sha256_file(pdir .. "/" .. en.copy) == need[k] then need[k] = nil end
  end
  if next(need) then
    pre_detail = "missing from the pre-image: " .. next(need)
    return
  end
  local disk = util.read_json(pdir .. "/preimage.json")
  if not disk or disk.status ~= "ready" then
    pre_detail = "the pre-image index is not on disk as ready"
    return
  end
  pre_ok = true
end } })
ok("P1: the apply succeeds", r_apply ~= nil, e_apply)
ok("P2: the pre-image is complete and verified before the first write (external store included)", pre_ok, pre_detail)
if r_apply then
  ok("P3: raw/ is untouched (same bytes, nothing added)", util.sha256_file(A .. "/raw/source.md") == snapA.files["raw/source.md"]
    and #util.walk(A .. "/raw") == 1)
  ok("P4: archived files are frozen byte for byte",
    util.sha256_file(A .. "/archive/index.md") == snapA.files["index.md"]
    and util.sha256_file(A .. "/archive/synthesis/old.md") == snapA.files["shared/synthesis/archive/old.md"]
    and util.sha256_file(A .. "/archive/AGENTS.md") == snapA.files["AGENTS.md"])
  ok("P5: RULES.md is never rewritten by the migration", util.sha256_file(A .. "/RULES.md") == snapA.files["RULES.md"])
  ok("P6: the colliding files both survive, with their own bytes (no overwrite)",
    util.sha256_file(A .. "/reference/term--from-shared-glossary.md") ~= nil
    and read(A, "reference/term--from-shared-glossary.md"):find("glossary term")
    and read(A, "reference/term--from-shared-reference.md"):find("reference term"))
  ok("P7: the path-qualified ../../ wikilink reads from its new location",
    read(A, "adrs/0001-first.md"):find("[[../reviews/jarvis/r1|the review]]", 1, true) ~= nil)
  ok("P8: …and resolves there", util.isfile(util.normpath(A .. "/adrs/../reviews/jarvis/r1.md")))
  ok("P9b: a file that stays was normalized in place (date -> created)", read(A, "MIGRATIONS.md"):find("\ncreated: 2026%-05%-01\n") ~= nil)
  ok("P9: frontmatter was normalized", read(A, "synthesis/bug.md"):find("\ntype: synthesis\nkind: bug%-fix\n") ~= nil
    and read(A, "synthesis/bug.md"):find("\nrelated: %[adrs/0001%-first.md%]") ~= nil)
  ok("P10: the external todo store's reference was rewritten",
    read(EXT_A, "open/task-b.md"):find(A .. "/adrs/0001-first.md", 1, true) ~= nil
    and read(EXT_A, "open/task-b.md"):find("$KB_ROOT/conventions/naming.md", 1, true) ~= nil)
  ok("P11: a new AGENTS.md (the §2 contract) and ABOUT.md files exist; .todo-list/ was not scaffolded anew",
    read(A, "AGENTS.md"):find("Agent contract") ~= nil and util.isfile(A .. "/notes/ABOUT.md")
    and #util.walk(A .. "/.todo-list") == 1)
  ok("P12: the script kept its executable mode", (vim.uv.fs_stat(A .. "/scripts/run.sh").mode % 512) == 493)
  ok("P13: the source folders are gone", not util.exists(A .. "/shared") and not util.exists(A .. "/agents") and not util.exists(A .. "/log"))

  -- undo refuses over a later edit
  local edited_path = A .. "/adrs/0002-second.md"
  local edited_text = read(A, "adrs/0002-second.md") .. "\nedited after the apply\n"
  util.write_file(edited_path, edited_text)
  local snap_post = snapshot(A)
  local u1, ue1 = undo(A)
  ok("U1: undo refuses when a migrated file was edited since the apply", u1 == nil and tostring(ue1):find("refused") ~= nil
    and tostring(ue1):find("0002-second", 1, true) ~= nil, ue1)
  ok("U2: …and changes nothing", same_snapshot(snap_post, snapshot(A)))
  local u2, ue2 = undo(A, { confirm = function() return false end })
  ok("U3: an unconfirmed edit refuses too", u2 == nil and same_snapshot(snap_post, snapshot(A)), ue2)
  -- restore the apply's bytes, then undo for real
  local pi = util.read_json(state_dir .. "/migrations/" .. plan.id .. "/preimage.json")
  util.write_file(edited_path, (edited_text:gsub("\nedited after the apply\n$", "")))
  ok("U4: the pre-image records what the apply left", pi and pi.post and pi.post[edited_path] == util.sha256_file(edited_path))
  local u3, ue3 = undo(A)
  ok("U5: undo succeeds with no edits", u3 ~= nil and #u3.problems == 0, ue3 or vim.inspect(u3 and u3.problems))
  local same, why = same_snapshot(snapA, snapshot(A))
  ok("U6: undo restores the KB byte for byte (files and directories), with no git", same, why)
  ok("U7: …a rewritten file", util.sha256_file(A .. "/shared/adrs/0001-first.md") == snapA.files["shared/adrs/0001-first.md"])
  ok("U8: …a rewritten file that never moved", util.sha256_file(A .. "/README.md") == snapA.files["README.md"])
  ok("U9: …a normalized frontmatter", util.sha256_file(A .. "/shared/bug-fixes/bug.md") == snapA.files["shared/bug-fixes/bug.md"])
  ok("U9b: …a normalized frontmatter that never moved", util.sha256_file(A .. "/MIGRATIONS.md") == snapA.files["MIGRATIONS.md"])
  ok("U10: …a removed directory", util.isdir(A .. "/shared/agents/lector"))
  ok("U11: …a created ABOUT.md is gone", not util.exists(A .. "/adrs/ABOUT.md") and not util.exists(A .. "/adrs"))
  ok("U12: the external todo store's file is restored", same_snapshot(snapExtA, snapshot(EXT_A)))
  local u4 = undo(A)
  ok("U13: a second undo is refused (state is undone)", u4 == nil)
end

section("[K] --keep-edited and --forget")
do
  local C = tmp .. "/kb-c"
  build_fixture(C, nil)
  local snapC = snapshot(C)
  local d = dry(C, nil)
  local a, ae = apply(C, nil)
  ok("K1: apply on a second fixture", d ~= nil and a ~= nil, ae)
  util.write_file(C .. "/adrs/ABOUT.md", "my own about\n")
  local u, ue = undo(C, { keep_edited = true })
  ok("K2: --keep-edited skips the edited file and restores the rest", u ~= nil and #u.kept == 1
    and read(C, "adrs/ABOUT.md") == "my own about\n" and util.sha256_file(C .. "/shared/adrs/0001-first.md") == snapC.files["shared/adrs/0001-first.md"], ue)
  local f, fe = migrate.run({ root = C, mode = "forget", state_dir = state_dir })
  ok("K3: --forget deletes the pre-image", f ~= nil and not util.exists(f.removed), fe)
end

section("[G] git variant")
do
  local G = tmp .. "/kb-g"
  build_fixture(G, nil)
  local env_ok = true
  local function git(...)
    local c, out, e = util.run(vim.list_extend({ "git", "-C", G }, { ... }))
    if c ~= 0 then env_ok = false end
    return out, e
  end
  vim.env.GIT_AUTHOR_NAME, vim.env.GIT_AUTHOR_EMAIL = "kb-spec", "kb-spec@example.invalid"
  vim.env.GIT_COMMITTER_NAME, vim.env.GIT_COMMITTER_EMAIL = "kb-spec", "kb-spec@example.invalid"
  git("init", "-q")
  git("config", "commit.gpgsign", "false")
  git("add", "-A")
  git("commit", "-q", "-m", "fixture")
  local head0 = vim.trim(git("rev-parse", "HEAD"))
  local snapG = snapshot(G)
  local d = dry(G, nil)
  ok("G1: the dry run sees a git repo", d ~= nil and d.plan.git.repo == true)
  local a, ae = apply(G, nil)
  ok("G2: apply commits the migration", a ~= nil and a.commit ~= nil and a.commit ~= head0, ae)
  local status = git("status", "--porcelain")
  ok("G3: the tree is clean after the commit", vim.trim(status) == "", status)
  local log = git("log", "--follow", "--format=%s", "--", "adrs/0002-second.md")
  ok("G4: history follows a moved file", log:find("fixture") ~= nil, log)
  local remotes = git("remote")
  ok("G5: no remote assumed, nothing pushed", vim.trim(remotes) == "")
  local u, ue = undo(G)
  ok("G6: undo restores the tree byte for byte and commits", u ~= nil and same_snapshot(snapG, snapshot(G)) and u.commit ~= nil, ue)
  ok("G7: git ran in the sandbox", env_ok)
end

section("[T] todo stores from auto-core")
do
  -- auto-core's persisted todo state lives under the sandboxed stdpath("state")
  local st = vim.fn.stdpath("state") .. "/auto-core/todo.json"
  util.write_file(st, vim.json.encode({
    known_dirs = { ["/x/a/.todo-list"] = { todo_dir = "/x/a/.todo-list", workspace_roots = { "/x/a" } } },
    dir_overrides = { ["/x/b"] = "/x/b-store" },
  }))
  local list, from_core = migrate.known_todo_stores()
  ok("T1: the dry run collects auto-core.todo.known_dirs() and its overrides",
    from_core and vim.tbl_contains(list, "/x/a/.todo-list") and vim.tbl_contains(list, "/x/b-store"), vim.inspect(list))
end

print(string.format("\n%d passed, %d failed", pass, fail))
util.rmtree(tmp)
if fail > 0 then
  print("KB-SPEC-COMPLETE FAIL")
  io.stdout:flush()
  os.exit(1)
end
print("KB-SPEC-COMPLETE OK")
io.stdout:flush()
os.exit(0)
