-- tests/preview_spec.lua — headless suite for autodoc.preview (ADR 1791210485).
--
-- Run:   nvim --headless -u NONE -l tests/preview_spec.lua
-- or:    tests/run-all.sh            (every suite, XDG sandboxed, summary-gated)
--
-- Loads auto-core from the sibling worktree (…/auto-core.nvim/<this branch>,
-- then …/main, then an installed copy) and never touches a running nvim. The
-- `autodoc` binary is a stub script on PATH that records its arguments and
-- writes the --output file; the system opener is a recorder.
--
-- Prints "<P> passed, <F> failed" at the natural end of the chunk and exits
-- non-zero on any failure; a missing summary line is an abort.

local plugin_root = vim.fn.fnamemodify(
  vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p"), ":h:h")
local siblings = vim.fn.fnamemodify(plugin_root, ":h:h")
local branch = vim.fn.fnamemodify(plugin_root, ":t")

vim.opt.rtp:prepend(plugin_root)
local core_found
for _, p in ipairs({
  siblings .. "/auto-core.nvim/" .. branch,
  siblings .. "/auto-core.nvim/main",
  vim.fn.expand("~/.local/share/nvim/lazy/auto-core.nvim"),
}) do
  if vim.fn.isdirectory(p .. "/lua/auto-core") == 1 then
    vim.opt.rtp:prepend(p)
    core_found = p
    break
  end
end
if not core_found then
  print("FATAL: auto-core.nvim not found next to " .. plugin_root)
  print("0 passed, 1 failed")
  os.exit(1)
end

vim.o.columns = 200
vim.o.lines = 60
vim.o.swapfile = false
vim.o.hidden = true
-- Before any setup(): a key mapped with the default leader would be invisible
-- to a later maparg("<leader>…") under a different leader.
vim.g.mapleader = " "

-- Sandbox: state, config and temp files under one throwaway root (the runner
-- also exports XDG_* to its own temp dir; this keeps a bare run safe too).
local SANDBOX = vim.fn.tempname() .. "-autodoc-preview-spec"
vim.fn.mkdir(SANDBOX, "p")
vim.env.XDG_STATE_HOME = SANDBOX .. "/state"
vim.env.XDG_CONFIG_HOME = SANDBOX .. "/config"
vim.env.XDG_DATA_HOME = SANDBOX .. "/data"
vim.env.TMPDIR = SANDBOX .. "/tmp"
vim.fn.mkdir(vim.env.TMPDIR, "p")
require("auto-core.state").configure({ persist_dir = SANDBOX .. "/persist" })

local pass_count, fail_count = 0, 0
local function ok(name, cond, detail)
  if cond then
    pass_count = pass_count + 1
    print("  PASS  " .. name)
  else
    fail_count = fail_count + 1
    print("  FAIL  " .. name .. (detail ~= nil and ("  — " .. tostring(detail)) or ""))
  end
end
local function section(title, fn)
  print("\n" .. title)
  local good, err = xpcall(fn, debug.traceback)
  if not good then ok(title .. ": section ran to the end", false, err) end
end

local function write(path, lines)
  vim.fn.mkdir(vim.fs.dirname(path), "p")
  vim.fn.writefile(lines, path)
end

local function marks(buf, ns)
  return vim.api.nvim_buf_get_extmarks(buf, ns, 0, -1, { details = true })
end
local function marks_on(buf, ns, row)
  local out = {}
  for _, m in ipairs(marks(buf, ns)) do
    if m[2] == row then out[#out + 1] = m end
  end
  return out
end
local function find_mark(buf, ns, row, pred)
  for _, m in ipairs(marks_on(buf, ns, row)) do
    if pred(m[4], m[3]) then return m end
  end
end
local function vt(d) return d.virt_text and d.virt_text[1] and d.virt_text[1][1] or nil end
local function row_of(lines, needle)
  for i, l in ipairs(lines) do
    if l:find(needle, 1, true) then return i - 1 end
  end
end
local function captures(buf, row, col)
  local names = {}
  for _, c in ipairs(vim.treesitter.get_captures_at_pos(buf, row, col)) do names[#names + 1] = c.capture end
  return table.concat(names, ",")
end

local core = require("auto-core")

local notes = {}
local orig_notify = vim.notify
vim.notify = function(msg, level)
  notes[#notes + 1] = { msg = msg, level = level }
end

local opened = {}
local function opener(t) opened[#opened + 1] = t end

-- ── [1] module load registers nothing; setup does; teardown undoes ─────────
section("[1] subscriptions: none at load, all in setup(), gone after teardown()", function()
  local before_sw = core.events.count_subscribers("worktree:switched")
  local before_fm = core.events.count_subscribers("core.file:modified")
  local P = require("autodoc.preview")
  ok("require returns the module", type(P) == "table")
  for _, fn in ipairs({ "setup", "teardown", "focus", "render_current", "render_path", "close_all", "find", "browser", "commands" }) do
    ok("exports " .. fn, type(P[fn]) == "function")
  end
  ok("module load subscribes to nothing (worktree:switched)",
    core.events.count_subscribers("worktree:switched") == before_sw)
  ok("module load subscribes to nothing (core.file:modified)",
    core.events.count_subscribers("core.file:modified") == before_fm)
  ok("module load registers no verb", core.mailbox.commands.get("preview.attach") == nil)
  P.setup({ opener = opener })
  ok("setup subscribes to worktree:switched",
    core.events.count_subscribers("worktree:switched") == before_sw + 1)
  ok("setup subscribes to core.file:modified",
    core.events.count_subscribers("core.file:modified") == before_fm + 1)
  P.setup({ opener = opener })
  ok("a second setup replaces, not stacks",
    core.events.count_subscribers("worktree:switched") == before_sw + 1)
  P.teardown()
  ok("teardown unsubscribes worktree:switched",
    core.events.count_subscribers("worktree:switched") == before_sw)
  ok("teardown unsubscribes core.file:modified",
    core.events.count_subscribers("core.file:modified") == before_fm)
  ok("teardown unregisters the verbs", core.mailbox.commands.get("preview.attach") == nil)
end)

local P = require("autodoc.preview")
local render = require("autodoc.preview.render")
P.setup({ opener = opener })
local NS = P.ns

local DOCDIR = SANDBOX .. "/docs"
local DOC = DOCDIR .. "/guide.md"
local DOC_LINES = {
  "---",
  "type: adr",
  "status: accepted",
  "updated: 2026-10-06",
  "tags: [a, b]",
  "---",
  "# Title with *emphasis* and `code`",
  "",
  "Intro with [a link](https://example.com/page) and [other](other.md#second-part).",
  "",
  "- first item",
  "  - nested item",
  "- [ ] open task",
  "- [x] done task",
  "1. numbered",
  "",
  "> [!WARNING]",
  "> Mind the gap.",
  "",
  "> a plain quote",
  "",
  "```lua",
  "local answer = 42",
  "```",
  "",
  "```mermaid",
  "graph TD",
  "  A --> B",
  "```",
  "",
  "| Name | Value |",
  "|------|:-----:|",
  "| [x](https://x.example) | **bold** |",
  "",
  "***",
  "",
  "![a diagram](img/diagram.png)",
  "",
  "## Second part",
}
write(DOC, DOC_LINES)
write(DOCDIR .. "/other.md", { "# Other", "", "## Second part", "", "text" })

-- ── [2] golden screen cells: each element in a float ──────────────────────
section("[2] golden cells: every element rendered into a float", function()
  vim.cmd("edit " .. vim.fn.fnameescape(DOC))
  local src = vim.api.nvim_get_current_buf()
  P.render_current("1")
  local s = P._slot("1")
  ok("slot 1 has a float", s and s.win and vim.api.nvim_win_is_valid(s.win))
  local buf, win = s.buf, s.win
  local lines = vim.api.nvim_buf_get_lines(buf, 0, -1, false)
  ok("float window conceallevel = 2", vim.wo[win].conceallevel == 2)
  ok("float buffer is a scratch copy, not the source", buf ~= src and vim.bo[buf].buftype == "nofile")
  ok("float buffer is not modifiable", vim.bo[buf].modifiable == false)
  ok("treesitter markdown highlighting is active on the float",
    vim.treesitter.highlighter.active[buf] ~= nil)
  vim.treesitter.get_parser(buf, "markdown"):parse(true)

  -- frontmatter
  ok("frontmatter folds to one header line (type · status · updated)",
    lines[1] == "≡ adr · accepted · updated 2026-10-06", lines[1])
  ok("frontmatter header is highlighted",
    find_mark(buf, NS, 0, function(d) return d.hl_group == "AutodocPreviewFrontmatter" end) ~= nil)
  ok("frontmatter body lines are not shown", row_of(lines, "tags: [a, b]") == nil)

  -- heading
  local h = row_of(lines, "# Title")
  ok("heading: '# ' marker concealed",
    find_mark(buf, NS, h, function(d, col) return col == 0 and d.conceal == "" and d.end_col == 2 end) ~= nil)
  ok("heading: H1 highlight on the text",
    find_mark(buf, NS, h, function(d) return d.hl_group == "AutodocPreviewH1" end) ~= nil)
  ok("heading: treesitter heading capture", captures(buf, h, 3):find("markup.heading.1", 1, true) ~= nil,
    captures(buf, h, 3))
  local em = lines[h + 1]:find("emphasis", 1, true) - 1
  ok("emphasis: treesitter italic capture", captures(buf, h, em):find("markup.italic", 1, true) ~= nil,
    captures(buf, h, em))
  local cd = lines[h + 1]:find("code", 1, true) - 1
  ok("inline code: treesitter raw capture", captures(buf, h, cd):find("markup.raw", 1, true) ~= nil,
    captures(buf, h, cd))
  local delim = lines[h + 1]:find("`", 1, true) - 1
  ok("inline code: delimiter concealed by treesitter",
    captures(buf, h, delim):find("conceal", 1, true) ~= nil, captures(buf, h, delim))

  -- lists + tasks
  local li = row_of(lines, "- first item")
  ok("list: bullet concealed as •",
    find_mark(buf, NS, li, function(d) return d.conceal == "•" end) ~= nil)
  ok("list: nested bullet concealed as ◦",
    find_mark(buf, NS, li + 1, function(d) return d.conceal == "◦" end) ~= nil)
  ok("task: open box ☐",
    find_mark(buf, NS, row_of(lines, "open task"), function(d) return d.conceal == "☐" end) ~= nil)
  ok("task: done box ☑",
    find_mark(buf, NS, row_of(lines, "done task"), function(d)
      return d.conceal == "☑" and d.hl_group == "AutodocPreviewTaskDone" end) ~= nil)
  ok("ordered list: number highlighted",
    find_mark(buf, NS, row_of(lines, "1. numbered"), function(d) return d.hl_group == "AutodocPreviewListNumber" end) ~= nil)

  -- callout + quote
  local co = row_of(lines, "[!WARNING]")
  ok("callout: sign in the sign column",
    find_mark(buf, NS, co, function(d) return d.sign_text and vim.trim(d.sign_text) == "⚠"
      and d.sign_hl_group == "AutodocPreviewWarning" end) ~= nil)
  ok("callout: [!WARNING] concealed and titled",
    find_mark(buf, NS, co, function(d) return d.conceal == "" end) ~= nil
    and find_mark(buf, NS, co, function(d) return vt(d) == "⚠ Warning" end) ~= nil)
  ok("callout: body bar in the callout colour",
    find_mark(buf, NS, co + 1, function(d) return d.conceal == "▎" and d.hl_group == "AutodocPreviewWarning" end) ~= nil)
  ok("quote: plain quote bar",
    find_mark(buf, NS, row_of(lines, "a plain quote"), function(d)
      return d.conceal == "▎" and d.hl_group == "AutodocPreviewQuote" end) ~= nil)

  -- fenced code
  local code = row_of(lines, "local answer")
  ok("code: background on the block",
    find_mark(buf, NS, code, function(d) return d.line_hl_group == "AutodocPreviewCode" end) ~= nil)
  ok("code: language label",
    find_mark(buf, NS, code, function(d) return vt(d) == "lua" and d.virt_text_pos == "right_align" end) ~= nil)
  ok("code: lua injection highlights the keyword",
    captures(buf, code, 0):find("keyword", 1, true) ~= nil, captures(buf, code, 0))

  -- mermaid
  local mm = row_of(lines, "graph TD")
  ok("mermaid: source kept", lines[mm + 2] == "  A --> B")
  ok("mermaid: hint that B opens it drawn",
    find_mark(buf, NS, mm, function(d) return vt(d) == render.MERMAID_HINT end) ~= nil)

  -- table
  local tt = row_of(lines, "┌") or -100
  -- tt is the 0-based row of the top rule; lines[] is 1-based.
  ok("table: drawn with box borders", tt ~= nil and (lines[tt + 2] or ""):find("│ Name", 1, true) ~= nil, (lines[tt + 2] or ""))
  ok("table: header separator", (lines[tt + 3] or ""):sub(1, #"├") == "├")
  ok("table: cell markup stripped", (lines[tt + 4] or ""):find("bold", 1, true) ~= nil and not (lines[tt + 4] or ""):find("**", 1, true),
    (lines[tt + 4] or ""))
  ok("table: closed by a bottom rule", (lines[tt + 5] or ""):sub(1, #"└") == "└")
  ok("table: source pipe rows not shown", row_of(lines, "|------|") == nil)
  ok("table: header text highlighted",
    find_mark(buf, NS, tt + 1, function(d) return d.hl_group == "AutodocPreviewTableHead" end) ~= nil)
  ok("table: link in a cell is a gx target", s.content.targets[tt + 3] and s.content.targets[tt + 3][1].target == "https://x.example")

  -- horizontal rule
  local hr = row_of(lines, "***")
  local rule = find_mark(buf, NS, hr, function(d) return vt(d) and vt(d):find("^─") end)
  ok("rule: drawn across the width", rule ~= nil and vim.api.nvim_strwidth(vt(rule[4])) == s.content.width)

  -- image placeholder
  local im = row_of(lines, "▣ a diagram")
  ok("image: placeholder line with alt text and path", im ~= nil and lines[im + 1]:find("img/diagram.png", 1, true) ~= nil)

  -- gx: http link → opener
  local lk = row_of(lines, "Intro with")
  opened = {}
  vim.api.nvim_win_set_cursor(win, { lk + 1, (lines[lk + 1]:find("a link", 1, true)) })
  P.open_target("1", P.target_at_cursor("1"))
  ok("gx: a URL goes to the system opener", opened[1] == "https://example.com/page", opened[1])

  -- gx: image → opener with the absolute file
  opened = {}
  vim.api.nvim_win_set_cursor(win, { im + 1, 0 })
  vim.api.nvim_win_call(win, function() vim.cmd("normal gx") end)
  ok("gx (key): an image opens the file itself", opened[1] == DOCDIR .. "/img/diagram.png", opened[1])

  -- gx: #fragment jumps
  P.open_target("1", { target = "#second-part", kind = "link", s = 0, e = 1 })
  local cur = vim.api.nvim_win_get_cursor(win)[1] - 1
  ok("gx: #fragment jumps to the heading", lines[cur + 1] == "## Second part", lines[cur + 1])

  -- gx: relative md link renders into the same slot
  vim.api.nvim_win_set_cursor(win, { lk + 1, (lines[lk + 1]:find("other", 1, true)) })
  P.open_target("1", P.target_at_cursor("1"))
  local s2 = P._slot("1")
  ok("gx: a Markdown link opens in the same slot", s2.path == DOCDIR .. "/other.md", s2.path)
  local nl = vim.api.nvim_buf_get_lines(s2.buf, 0, -1, false)
  ok("gx: … at its #fragment", nl[vim.api.nvim_win_get_cursor(s2.win)[1]] == "## Second part")
  P.close_all()
end)

-- ── [2b] tables wrap to the float width ───────────────────────────────────
section("[2b] tables wrap to the width", function()
  local long = string.rep("word ", 40)
  local c = render.build({
    "| A | B | C |", "|---|---|---|",
    "| " .. long .. " | short | " .. long .. " |",
    "| x | y | z |",
  }, { width = 50 })
  local widths, all_fit = {}, true
  for _, l in ipairs(c.lines) do
    local w = vim.api.nvim_strwidth(l)
    widths[w] = true
    if w > 50 then all_fit = false end
  end
  ok("every table line fits the width", all_fit)
  ok("every table line has the same width (aligned borders)", vim.tbl_count(widths) == 1, vim.inspect(vim.tbl_keys(widths)))
  ok("a long cell wraps onto several lines", #c.lines > 6, #c.lines)
  ok("wrapped body rows get separators", #vim.tbl_filter(function(l) return l:sub(1, #"├") == "├" end, c.lines) >= 2)
end)

-- ── [3] the source buffer is never modified ───────────────────────────────
section("[3] the source buffer is never modified", function()
  local path = SANDBOX .. "/untouched.md"
  write(path, DOC_LINES)
  vim.cmd("edit " .. vim.fn.fnameescape(path))
  local src = vim.api.nvim_get_current_buf()
  local tick = vim.b[src].changedtick
  local before = vim.api.nvim_buf_get_lines(src, 0, -1, false)
  P.render_current("2")
  P.refresh("2")
  P.close_all()
  P.focus("2")
  P.refresh("2")
  ok("changedtick unchanged", vim.b[src].changedtick == tick, vim.b[src].changedtick .. " vs " .. tick)
  ok("lines unchanged", vim.deep_equal(vim.api.nvim_buf_get_lines(src, 0, -1, false), before))
  ok("'modified' still off", vim.bo[src].modified == false)
  ok("no extmark in any namespace on the source",
    #vim.api.nvim_buf_get_extmarks(src, -1, 0, -1, {}) == 0)
  ok("file on disk unchanged", vim.deep_equal(vim.fn.readfile(path), DOC_LINES))
  P.close_all()
end)

-- ── [4] layout and cursor memory ──────────────────────────────────────────
section("[4] the six-float cascade and per-slot cursor memory", function()
  local cfg = require("autodoc.preview.config").values
  for _, slot in ipairs(P.SLOTS) do P.render_path(slot, DOC) end
  local pos = {}
  for _, slot in ipairs(P.SLOTS) do
    local c = vim.api.nvim_win_get_config(P._slot(slot).win)
    pos[slot] = { row = c.row, col = c.col, width = c.width }
  end
  ok("six floats open at once", #vim.tbl_filter(function(sl) return P._slot(sl).win end, P.SLOTS) == 6)
  ok("slot 1 sits top-left", pos["1"].row == 1 and pos["1"].col == 1, vim.inspect(pos["1"]))
  ok("top row shares a row", pos["2"].row == 1 and pos["3"].row == 1)
  ok("bottom row sits cascade_y lower", pos.a.row == 1 + cfg.cascade_y and pos.d.row == 1 + cfg.cascade_y)
  local cols = require("autodoc.preview.layout").columns()
  ok("every float sits at its cascade column",
    #vim.tbl_filter(function(sl) return pos[sl].col == cols[sl] end, P.SLOTS) == 6, vim.inspect(pos))
  ok("slot d is the rightmost and ends inside the screen",
    pos.d.col > pos["3"].col and pos.d.col + pos.d.width <= vim.o.columns)
  ok("columns ascend 1 < 2 < 3 and a < s < d",
    pos["1"].col < pos["2"].col and pos["2"].col < pos["3"].col and pos.a.col < pos.s.col and pos.s.col < pos.d.col)
  ok("bottom row is shifted right of its pair", pos.a.col > pos["1"].col)

  local s = P._slot("a")
  vim.api.nvim_set_current_win(s.win)
  vim.api.nvim_win_set_cursor(s.win, { 9, 2 })
  P.close_all()
  ok("close_all closes every float", #vim.tbl_filter(function(sl) return P._slot(sl).win end, P.SLOTS) == 0)
  ok("close_all keeps the slot's document", P._slot("a").path == DOC)
  P.focus("a")
  ok("focus reopens with the cursor where it was left",
    vim.deep_equal(vim.api.nvim_win_get_cursor(P._slot("a").win), { 9, 2 }),
    vim.inspect(vim.api.nvim_win_get_cursor(P._slot("a").win)))
  P.focus("a")
  ok("focus on an open float jumps into it", vim.api.nvim_get_current_win() == P._slot("a").win)
  P.render_path("a", DOC)
  ok("render_path loads afresh: cursor at the top",
    vim.api.nvim_win_get_cursor(P._slot("a").win)[1] == 1)
  P.close_all()
end)

-- ── [5] refresh on buffer edit (debounced) ────────────────────────────────
section("[5] a buffer-backed slot refreshes on TextChanged, debounced", function()
  local src = vim.api.nvim_create_buf(true, false)
  vim.bo[src].filetype = "markdown"
  vim.api.nvim_buf_set_lines(src, 0, -1, false, { "# Live", "", "first" })
  vim.api.nvim_set_current_buf(src)
  P.render_current("3")
  local s = P._slot("3")
  local calls = 0
  local real = P.refresh
  P.refresh = function(slot) calls = calls + 1; return real(slot) end
  for k = 1, 5 do
    vim.api.nvim_buf_set_lines(src, -1, -1, false, { "edit " .. k })
    vim.api.nvim_exec_autocmds("TextChanged", { buffer = src })
  end
  local got = vim.wait(2000, function()
    return vim.tbl_contains(vim.api.nvim_buf_get_lines(s.buf, 0, -1, false), "edit 5")
  end, 10)
  ok("the float shows the edit", got)
  vim.wait(300)
  ok("five edits inside the debounce window render once", calls == 1, calls)
  ok("the float keeps its window", P._slot("3").win == s.win)
  local own = function()
    return #vim.tbl_filter(function(a) return (a.group_name or ""):find("^autodoc%.preview%.slot") end,
      vim.api.nvim_get_autocmds({ buffer = src, event = "TextChanged" }))
  end
  ok("the slot's TextChanged subscription exists while open", own() == 1, own())
  P.close_all()
  ok("closing the float removes the slot's TextChanged subscription", own() == 0, own())
  calls = 0
  vim.api.nvim_buf_set_lines(src, -1, -1, false, { "after close" })
  vim.api.nvim_exec_autocmds("TextChanged", { buffer = src })
  vim.wait(300)
  ok("no render for a closed slot", calls == 0, calls)
  P.refresh = real

  -- closing the float from inside (`q`, `:quit`) drops the subscription too
  P.focus("3")
  ok("reopened: subscription back", own() == 1, own())
  vim.api.nvim_set_current_win(P._slot("3").win)
  vim.cmd("normal q")
  ok("`q` in the float closes it", P._slot("3").win == nil)
  ok("`q`: subscription removed", own() == 0, own())
  P.focus("3")
  vim.api.nvim_set_current_win(P._slot("3").win)
  vim.cmd("quit")
  ok("`:quit` in the float closes the slot", P._slot("3").win == nil)
  ok("`:quit`: subscription removed", own() == 0, own())

  -- an unnamed source buffer wiped → doc:unpinned
  P.focus("3")
  local unp
  local h = core.events.subscribe("doc:unpinned", function(p) unp = p end)
  vim.api.nvim_set_current_win(vim.fn.win_getid(1))
  vim.api.nvim_buf_delete(src, { force = true })
  vim.wait(500, function() return unp ~= nil end, 10)
  core.events.unsubscribe(h)
  ok("wiping an unnamed source unpins its slot", unp and unp.slot == "3", vim.inspect(unp))
  ok("… and closes its float", P._slot("3").win == nil)
end)

-- ── [6] refresh on core.file:modified ─────────────────────────────────────
section("[6] a file-backed slot refreshes on core.file:modified", function()
  local path = SANDBOX .. "/watched.md"
  write(path, { "# Watched", "", "version one" })
  P.render_path("s", path)
  local s = P._slot("s")
  ok("render_path makes a file-backed slot", s.kind == "file" and s.path == path)
  write(path, { "# Watched", "", "version two" })
  core.events.publish("core.file:modified", { path = SANDBOX .. "/other-file.md", change = "modified" })
  vim.wait(300)
  ok("another path's change is ignored",
    vim.tbl_contains(vim.api.nvim_buf_get_lines(s.buf, 0, -1, false), "version one"))
  core.events.publish("core.file:modified", { path = path, change = "modified" })
  local got = vim.wait(2000, function()
    return vim.tbl_contains(vim.api.nvim_buf_get_lines(s.buf, 0, -1, false), "version two")
  end, 10)
  ok("its own path's change re-renders the float", got)
  P.close_all()
end)

-- ── [7] pins: per project, restored on worktree:switched ──────────────────
section("[7] pins persist per project and come back after worktree:switched", function()
  local A, B = SANDBOX .. "/projA", SANDBOX .. "/projB"
  write(A .. "/a.md", { "# A doc" })
  write(B .. "/b.md", { "# B doc" })
  core.events.publish("worktree:switched", { from = vim.fn.getcwd(), to = A, cwd = A })

  local pinned
  local h1 = core.events.subscribe("doc:pinned", function(p) pinned = p end)
  P.render_path("2", A .. "/a.md")
  ok("doc:pinned carries slot and path", pinned and pinned.slot == "2" and pinned.path == A .. "/a.md", vim.inspect(pinned))
  core.events.unsubscribe(h1)
  vim.api.nvim_win_set_cursor(P._slot("2").win, { 1, 2 })
  P.close_all()

  local unpinned = {}
  local h2 = core.events.subscribe("doc:unpinned", function(p) unpinned[#unpinned + 1] = p end)
  core.events.publish("worktree:switched", { from = A, to = B, cwd = B })
  core.events.unsubscribe(h2)
  ok("switching away publishes doc:unpinned for the slot",
    #vim.tbl_filter(function(p) return p.slot == "2" and p.path == A .. "/a.md" end, unpinned) == 1, vim.inspect(unpinned))
  ok("project B starts without project A's pin", P._slot("2") == nil or P._slot("2").path == nil)
  P.render_path("2", B .. "/b.md")
  P.close_all()

  core.events.publish("worktree:switched", { from = B, to = A, cwd = A })
  P.focus("2")
  local s = P._slot("2")
  ok("back in project A, slot 2 holds A's pin", s.path == A .. "/a.md", s.path)
  ok("… and its cursor", s.win and vim.deep_equal(vim.api.nvim_win_get_cursor(s.win), { 1, 2 }),
    s.win and vim.inspect(vim.api.nvim_win_get_cursor(s.win)))
  P.close_all()

  local ns = require("auto-core.state").namespace("autodoc.preview")
  ns:persist_now()
  local file = SANDBOX .. "/persist/autodoc_preview.json"
  local raw = table.concat(vim.fn.readfile(file), "\n")
  ok("pins are persisted in the autodoc.preview namespace",
    raw:find(A .. "/a.md", 1, true) ~= nil and raw:find(B .. "/b.md", 1, true) ~= nil)

  -- md-harpoon's pins are not imported.
  local mh = require("auto-core.state").namespace("md-harpoon", { persist = "json" })
  local key = vim.fn.sha256(vim.fs.normalize(B)):sub(1, 16)
  mh:set("pins", { [key] = { ["3"] = { source_path = B .. "/b.md" } } })
  core.events.publish("worktree:switched", { from = A, to = B, cwd = B })
  ok("md-harpoon's pins are not read (no pin for slot 3)", require("autodoc.preview.pins").get("3") == nil)
  vim.api.nvim_set_current_buf(vim.api.nvim_create_buf(true, true))  -- not Markdown
  P.focus("3")  -- empty slot + non-Markdown buffer → a warning, no float
  ok("md-harpoon's pins are not read (slot 3 stays empty)", P._slot("3").path == nil and P._slot("3").win == nil)
  P.close_all()
end)

-- ── [8] browser view through autodoc --export --base ──────────────────────
local STUB = SANDBOX .. "/bin"
local ARGS = SANDBOX .. "/stub-args"
write(STUB .. "/autodoc", {
  "#!/bin/sh",
  "printf '%s\\n' \"$@\" > \"" .. ARGS .. "\"",
  "out=''; prev=''; last=''",
  "for a in \"$@\"; do if [ \"$prev\" = '--output' ]; then out=\"$a\"; fi; prev=\"$a\"; last=\"$a\"; done",
  "cp \"$last\" \"" .. ARGS .. ".snapshot\"",
  "printf '<html>stub</html>\\n' > \"$out\"",
})
vim.fn.setfperm(STUB .. "/autodoc", "rwxr-xr-x")
vim.env.PATH = STUB .. ":" .. vim.env.PATH

local function export(fn)
  local done, html, argv
  opened = {}
  vim.fn.delete(ARGS)
  fn(function(ok_, h, a) done, html, argv = ok_, h, a end)
  vim.wait(5000, function() return done ~= nil end, 10)
  local recorded = vim.fn.filereadable(ARGS) == 1 and vim.fn.readfile(ARGS) or {}
  local snap = vim.fn.filereadable(ARGS .. ".snapshot") == 1 and vim.fn.readfile(ARGS .. ".snapshot") or {}
  return done, html, recorded, snap
end
local function arg_after(argv, flag)
  for i, a in ipairs(argv) do if a == flag then return argv[i + 1] end end
end

section("[8] the browser view exports a snapshot with --base", function()
  -- unsaved named buffer
  local dir = SANDBOX .. "/notes"
  local path = dir .. "/draft.md"
  write(path, { "# On disk", "", "see [sibling](sibling.md)" })
  vim.cmd("edit " .. vim.fn.fnameescape(path))
  local b = vim.api.nvim_get_current_buf()
  vim.api.nvim_buf_set_lines(b, 0, 1, false, { "# Unsaved edit" })
  local done, html, argv, snap = export(function(cb) P.browser(nil, { on_done = cb }) end)
  local d = require("autodoc.preview.browser").dir()
  ok("unsaved: export succeeded and the page opened", done == true and opened[1] == html, vim.inspect(opened))
  ok("unsaved: argv is --export html", argv[1] == "--export" and argv[2] == "html", vim.inspect(argv))
  ok("unsaved: --base is the buffer's directory", arg_after(argv, "--base") == dir, arg_after(argv, "--base"))
  ok("unsaved: --theme passed", arg_after(argv, "--theme") == "light" or arg_after(argv, "--theme") == "dark")
  ok("unsaved: --output is <dir>/<name>.html", arg_after(argv, "--output") == d .. "/draft.html")
  ok("unsaved: the snapshot is <dir>/<name>.md, the last argument", argv[#argv] == d .. "/draft.md")
  ok("unsaved: the snapshot holds the buffer's unsaved text", snap[1] == "# Unsaved edit", snap[1])
  ok("unsaved: the source file is untouched", vim.fn.readfile(path)[1] == "# On disk")
  local mode = vim.uv.fs_stat(d).mode % 512
  ok("snapshot directory is private (0700)", mode == tonumber("700", 8), string.format("%o", mode))
  ok("snapshot directory is under the session temp dir", vim.startswith(d, vim.env.TMPDIR))
  vim.bo[b].modified = false

  -- unnamed buffer
  local u = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_lines(u, 0, -1, false, { "# Scratch", "", "```mermaid", "graph LR", "```" })
  vim.api.nvim_set_current_buf(u)
  done, html, argv, snap = export(function(cb) P.browser(nil, { on_done = cb }) end)
  ok("unnamed: exported", done == true)
  ok("unnamed: --base is the cwd", arg_after(argv, "--base") == vim.fn.getcwd(), arg_after(argv, "--base"))
  ok("unnamed: snapshot named untitled-<bufnr>.md", argv[#argv] == d .. "/untitled-" .. u .. ".md", argv[#argv])
  ok("unnamed: snapshot holds the text", snap[3] == "```mermaid")

  -- .markdown buffer, through a slot
  local mpath = dir .. "/legacy.markdown"
  write(mpath, { "# Legacy" })
  P.render_path("d", mpath)
  done, html, argv, snap = export(function(cb) P.browser("d", { on_done = cb }) end)
  ok(".markdown: exported", done == true)
  ok(".markdown: snapshot is <name>.md", argv[#argv] == d .. "/legacy.md", argv[#argv])
  ok(".markdown: --base is its directory", arg_after(argv, "--base") == dir)
  -- B inside the float exports that slot
  opened = {}
  vim.fn.delete(ARGS)
  vim.api.nvim_set_current_win(P._slot("d").win)
  vim.cmd("normal B")
  vim.wait(5000, function() return #opened > 0 end, 10)
  local bargv = vim.fn.filereadable(ARGS) == 1 and vim.fn.readfile(ARGS) or {}
  ok("B in a float exports its document", bargv[#bargv] == d .. "/legacy.md", bargv[#bargv])
  P.close_all()

  -- VimLeavePre removes the directory
  ok("the directory exists before VimLeavePre", vim.fn.isdirectory(d) == 1)
  vim.api.nvim_exec_autocmds("VimLeavePre", {})
  ok("VimLeavePre removes the snapshot directory", vim.fn.isdirectory(d) == 0)

  -- no binary → a warning, no crash
  local cfgmod = require("autodoc.preview.config")
  local saved = cfgmod.values.binary
  cfgmod.values.binary = "autodoc-definitely-missing"
  notes = {}
  local r = P.browser("d")
  cfgmod.values.binary = saved
  ok("a missing binary warns instead of failing", r == nil and #notes > 0 and notes[1].msg:find("not found", 1, true) ~= nil)
end)

-- ── [9] find: any Markdown file under cwd ─────────────────────────────────
section("[9] find lists any Markdown file under cwd (not only KB files)", function()
  local root = SANDBOX .. "/findroot"
  write(root .. "/deep/er/plain.md", { "no frontmatter, not a KB doc" })
  write(root .. "/README.markdown", { "# readme" })
  write(root .. "/node_modules/pkg/x.md", { "skip" })
  write(root .. "/notes.txt", { "not markdown" })
  local files = require("autodoc.preview.find").list(root)
  ok("a nested non-KB file is listed", vim.tbl_contains(files, root .. "/deep/er/plain.md"), vim.inspect(files))
  ok(".markdown files are listed", vim.tbl_contains(files, root .. "/README.markdown"))
  ok("node_modules is skipped", not vim.tbl_contains(files, root .. "/node_modules/pkg/x.md"))
  ok("non-Markdown files are skipped", not vim.tbl_contains(files, root .. "/notes.txt"))

  local real = vim.ui.select
  local prompts = {}
  vim.ui.select = function(items, o, cb)
    prompts[#prompts + 1] = o.prompt
    if #prompts == 1 then
      for _, it in ipairs(items) do
        if it:find("plain.md", 1, true) then return cb(it) end
      end
      return cb(nil)
    end
    ok("slot prompt uses readable labels", o.format_item("a") == "left (a)")
    return cb("a")
  end
  P.find({ cwd = root })
  vim.ui.select = real
  local s = P._slot("a")
  ok("find → pick a slot renders the file there", s and s.path == root .. "/deep/er/plain.md" and s.win ~= nil,
    s and s.path)
  P.close_all()
end)

-- ── [10] mailbox verbs ────────────────────────────────────────────────────
section("[10] mailbox verbs preview.attach / view / browser (owner autodoc)", function()
  local cmds = core.mailbox.commands
  for _, name in ipairs({ "preview.attach", "preview.view", "preview.browser" }) do
    local spec = cmds.get(name)
    ok(name .. " registered by autodoc", spec and spec.owner == "autodoc")
  end
  for _, old in ipairs({ "harpoon_attach", "harpoon_view", "harpoon_render_browser" }) do
    ok("no alias " .. old, cmds.get(old) == nil or cmds.get(old).owner ~= "autodoc")
  end
  local r = cmds.handle_message({ kind = "command", command = "preview.attach", args = { slot = "3", path = DOC } })
  ok("preview.attach renders the path into the slot", r.ok and P._slot("3").path == DOC, vim.inspect(r))
  P.close_all()
  r = cmds.handle_message({ kind = "command", command = "preview.view", args = { slot = "3" } })
  ok("preview.view reopens the slot", r.ok and P._slot("3").win ~= nil, vim.inspect(r))
  r = cmds.handle_message({ kind = "command", command = "preview.attach", args = { slot = "z", path = DOC } })
  ok("a bad slot is refused", r.ok == false and r.code == "invalid_slot", vim.inspect(r))
  r = cmds.handle_message({ kind = "command", command = "preview.attach", args = { slot = "1" } })
  ok("a missing path is refused by the schema", r.ok == false and r.code == "bad_args", vim.inspect(r))
  vim.fn.delete(ARGS)
  opened = {}
  r = cmds.handle_message({ kind = "command", command = "preview.browser", args = { slot = "3" } })
  vim.wait(5000, function() return #opened > 0 end, 10)
  local vargv = vim.fn.filereadable(ARGS) == 1 and vim.fn.readfile(ARGS) or {}
  ok("preview.browser exports the slot", r.ok and arg_after(vargv, "--base") == DOCDIR,
    vim.inspect(r) .. " " .. vim.inspect(vargv))
  P.close_all()
end)

-- ── [11] user commands and keys ───────────────────────────────────────────
section("[11] user commands from setup(); keys only with keys = true", function()
  local have = vim.api.nvim_get_commands({})
  for _, name in ipairs(require("autodoc.preview.commands").USER_COMMANDS) do
    ok(":" .. name .. " defined", have[name] ~= nil)
  end
  ok("no global <leader>m1 by default", vim.fn.maparg("<leader>m1", "n") == "")
  vim.cmd("AutodocPreviewRenderPath 1 " .. vim.fn.fnameescape(DOC))
  ok(":AutodocPreviewRenderPath renders", P._slot("1").path == DOC and P._slot("1").win ~= nil)
  vim.cmd("AutodocPreviewCloseAll")
  ok(":AutodocPreviewCloseAll closes", P._slot("1").win == nil)
  P.setup({ opener = opener, keys = true })
  ok("keys = true maps <leader>m1", vim.fn.maparg("<leader>m1", "n") ~= "")
  ok("keys = true maps <leader>mA and <leader>mf",
    vim.fn.maparg("<leader>mA", "n") ~= "" and vim.fn.maparg("<leader>mf", "n") ~= "")
  local mb = vim.fn.maparg("<leader>mb", "n", false, true)
  ok("keys = true maps <leader>mb to the browser, as md-harpoon did", (mb.desc or ""):find("browser", 1, true) ~= nil, vim.inspect(mb))
  P.teardown()
  ok("teardown removes the keys", vim.fn.maparg("<leader>m1", "n") == "")
  ok("teardown removes the commands", vim.api.nvim_get_commands({}).AutodocPreviewFocus == nil)
  P.setup({ opener = opener, commands = false })
  ok("commands = false defines none", vim.api.nvim_get_commands({}).AutodocPreviewFocus == nil)
  P.commands()
  ok("commands() defines them on demand", vim.api.nvim_get_commands({}).AutodocPreviewFocus ~= nil)
  P.setup({ opener = opener })
end)

-- ── [12] no md-render / md-harpoon anywhere in lua/autodoc ────────────────
section("[12] lua/autodoc requires neither md-render nor md-harpoon", function()
  local root = plugin_root .. "/lua/autodoc"
  local files, hits = 0, {}
  for name, kind in vim.fs.dir(root, { depth = 32 }) do
    if kind == "file" and name:match("%.lua$") then
      files = files + 1
      local body = table.concat(vim.fn.readfile(root .. "/" .. name), "\n")
      for _, dep in ipairs({ "md%-render", "md%-harpoon", "md_render", "md_harpoon" }) do
        if body:find("require%s*%(?%s*[\"']" .. dep) or body:find("pcall%s*%(%s*require%s*,%s*[\"']" .. dep) then
          hits[#hits + 1] = name .. " → " .. dep
        end
      end
    end
  end
  ok("the scan read the tree", files >= 10, files)
  ok("no require of md-render / md-harpoon", #hits == 0, table.concat(hits, "; "))
  local loaded = {}
  for mod in pairs(package.loaded) do
    if mod:find("^md%-render") or mod:find("^md%-harpoon") then loaded[#loaded + 1] = mod end
  end
  ok("neither is loaded after the whole suite ran", #loaded == 0, table.concat(loaded, ", "))
end)

-- ── [13] performance: a ~2,000-line document under the frame budget ───────
-- Budget: a full render (build + apply into the float buffer) of a 2,000-line
-- document in under 50 ms. The cell asserts the median of five runs against
-- 3× the budget so a loaded CI host doesn't flake; the measured number prints.
local BUDGET_MS = 50
section("[13] a 2,000-line document renders under the frame budget", function()
  local doc = { "---", "type: adr", "status: accepted", "updated: 2026-10-06", "---" }
  local k = 0
  while #doc < 2000 do
    k = k + 1
    vim.list_extend(doc, {
      "## Section " .. k, "",
      "Paragraph with *emphasis*, **strong**, `code` and a [link](https://example.com/" .. k .. ").",
      "- item one", "- [ ] task", "  - nested", "",
      "> [!NOTE]", "> A note.", "",
      "```lua", "local x = " .. k, "print(x)", "```", "",
      "| a | b | c |", "|---|---|---|", "| 1 | 2 | 3 |", "| four | five | six |", "",
      "---", "",
    })
  end
  local buf = vim.api.nvim_create_buf(false, true)
  local ns = vim.api.nvim_create_namespace("autodoc.preview.bench")
  local times = {}
  for _ = 1, 5 do
    local t0 = vim.uv.hrtime()
    local c = render.build(doc, { width = 110 })
    render.apply(buf, ns, c)
    times[#times + 1] = (vim.uv.hrtime() - t0) / 1e6
  end
  table.sort(times)
  local median = times[3]
  print(string.format("        render of %d lines: median %.1f ms (min %.1f, max %.1f), budget %d ms",
    #doc, median, times[1], times[5], BUDGET_MS))
  ok(("full render median under the budget (×3 headroom: < %d ms)"):format(BUDGET_MS * 3), median < BUDGET_MS * 3,
    string.format("%.1f ms", median))
  vim.treesitter.start(buf, "markdown")
  local t0 = vim.uv.hrtime()
  vim.treesitter.get_parser(buf, "markdown"):parse(true)
  local parse = (vim.uv.hrtime() - t0) / 1e6
  print(string.format("        first full treesitter parse (with injections): %.1f ms", parse))
  ok("first full treesitter parse completes (informational bound < 1000 ms)", parse < 1000, parse)
end)

P.teardown()
vim.notify = orig_notify
vim.fn.delete(SANDBOX, "rf")

print(string.format("\n%d passed, %d failed", pass_count, fail_count))
os.exit(fail_count == 0 and 0 or 1)
