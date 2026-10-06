-- The knowledge base's <leader>m group against a real daemon built from this checkout: the
-- primary KB found by its root alone (auto-core's import records no workspace name), a document by
-- name, recent files shared with the TUI, backlinks, the restart and the offer to restart an older
-- daemon, the release-binary install, and the versions maintenance shows.
local t = require("helpers")

local bin = assert(os.getenv("AUTODOC_TEST_BIN"), "AUTODOC_TEST_BIN")
local kb = t.tmp("kb")
vim.fn.mkdir(kb .. "/notes", "p")
local sock = t.tmp("run", "autodoc.sock")
local cfg = t.tmp("config.toml")
vim.fn.writefile({
  "[server]",
  string.format("socket = %q", sock),
  string.format("state_dir = %q", t.tmp("state", "autodoc")),
  string.format("data_dir = %q", t.tmp("data", "autodoc")),
  "[follow]",
  'poll_interval = "50ms"',
}, cfg)
vim.fn.writefile({ "# Birds", "", "kestrel hovering", "", "See [[notes/owls]]." }, kb .. "/birds.md")
vim.fn.writefile({ "# Owls", "", "barn owl" }, kb .. "/notes/owls.md")
for i = 1, 3 do vim.fn.writefile({ "# Doc " .. i }, kb .. "/doc" .. i .. ".md") end

-- the project's primary KB as auto-core's first-run import records it: the root alone
local primary = { root = kb }
package.loaded["auto-core.kb"] = { primary = function() return primary end }

local autodoc = require("autodoc")
local session = require("autodoc.session")
local finders = require("autodoc.finders")
local client = require("autodoc.client")
local lifecycle = require("autodoc.lifecycle")
autodoc.setup({ bin = bin, config = cfg })

-- poll calls check, which may itself wait on the daemon, until it holds or ms have passed by the
-- clock. Never t.wait around a check that waits: nested vim.wait loops never reach the outer
-- deadline when the check keeps failing, so a failing cell would hang the suite.
local function poll(ms, check)
  local deadline = vim.uv.now() + ms
  repeat
    if check() then return true end
    vim.wait(50)
    vim.uv.update_time()
  until vim.uv.now() >= deadline
  return false
end

-- the pickers' items, instead of a picker
local presented
finders.present = function(title, items) presented = { title = title, items = items } end
local function present_wait(fn)
  presented = nil
  fn()
  t.wait(10000, function() return presented ~= nil end)
  return presented
end

t.section("opening a KB file starts no daemon", function()
  vim.cmd("edit " .. vim.fn.fnameescape(kb .. "/birds.md"))
  vim.wait(300)
  t.ok(not session.is_ready() and vim.uv.fs_stat(sock) == nil, "a file opened before any KB call connects nothing")
  vim.cmd("bwipeout!")
end)

t.section("the primary KB by its root", function()
  t.eq(session.selected(), nil, "before the workspaces are listed, the root names no workspace yet")
  local _, err = t.await(20000, function(done) session.call("workspace.add", { "global", kb }, done) end)
  t.ok(err == nil, "workspace.add", err and err.message)
  session.reset_for_tests() -- forget the listing workspace.add made: a fresh Neovim
  autodoc.setup({ bin = bin, config = cfg })
  local name = t.await(20000, function(done) session.resolve_selected(done) end)
  t.eq(name, "global", "resolve_selected lists the workspaces and finds the primary by its root")
  t.eq(session.selected(), "global", "selected() then answers it")
  t.ok(session.is_primary({ name = "global", root = kb .. "/" }, primary), "a root with a trailing slash is the same root")
  t.ok(not session.is_primary({ name = "other", root = t.tmp("other") }, primary), "another root is not the primary")
  t.ok(session.is_primary({ name = "named" }, { workspace = "named", root = "/x" }), "a record naming a workspace matches by name")
  t.ok(not session.is_primary({ name = "global", root = kb }, { workspace = "named", root = kb }), "and by name only, then")
  session.reset_for_tests()
  autodoc.setup({ bin = bin, config = cfg })
  local res, serr
  poll(20000, function() -- the new workspace indexes first
    res, serr = t.await(5000, function(done) require("autodoc.api").search("kestrel", { limit = 5 }, done) end)
    return serr ~= nil or (res and #(res.hits or {}) > 0)
  end)
  t.ok(serr == nil and res and #(res.hits or {}) > 0, "a search with nothing selected searches the primary",
    serr and serr.message or vim.inspect(res))
end)

t.section("a document by name", function()
  poll(10000, function()
    local paths = t.await(5000, function(done) finders.list_paths("global", done) end)
    return paths and #paths == 5
  end)
  local page = finders.PAGE
  finders.PAGE = 2 -- five documents over three pages
  local paths, err = t.await(10000, function(done) finders.list_paths("global", done) end)
  finders.PAGE = page
  t.ok(err == nil, "index.list pages", err and err.message)
  t.eq(paths, { "birds.md", "doc1.md", "doc2.md", "doc3.md", "notes/owls.md" }, "every page is read")
  local p = present_wait(finders.files)
  t.ok(p and #p.items == 5, "files offers every document", vim.inspect(p))
  t.eq(p and p.items[1].file, vim.fs.normalize(kb) .. "/birds.md", "an item is the file's absolute path")
end)

t.section("recent files, shared with the TUI", function()
  local function recent()
    return t.await(5000, function(done) finders.read_recent(done) end) or {}
  end
  finders.note_recent("global", "doc1.md")
  poll(5000, function() return #recent() == 1 end)
  finders.note_recent("global", "doc2.md")
  poll(5000, function() return #recent() == 2 end)
  finders.note_recent("global", "doc1.md")
  t.ok(poll(5000, function() local r = recent() return r[1] and r[1].path == "doc1.md" end), "a file opened again is first")
  t.eq(recent(), { { workspace = "global", path = "doc1.md" }, { workspace = "global", path = "doc2.md" } }, "once each, newest first")
  -- the TUI's own shape, written by the TUI: a list Neovim reads, and a KB that is gone left out
  local tui = vim.json.encode({ { workspace = "gone", path = "x.md" }, { workspace = "global", path = "doc3.md" } })
  t.await(5000, function(done) session.call("preference.set", { finders.PREF_RECENT, tui }, done) end)
  local p = present_wait(finders.recent)
  t.eq(p and vim.tbl_map(function(it) return it.path end, p.items), { "doc3.md" }, "the TUI's list is offered, a gone KB left out")
  -- the list keeps twenty
  local docs = {}
  for i = 1, 25 do docs = finders.with_recent(docs, { workspace = "w", path = i .. ".md" }) end
  t.eq(#docs, finders.MAX_RECENT, "at most twenty are kept")
  t.eq(docs[1].path, "25.md", "the newest first")
  t.eq(finders.decode_recent("not json"), {}, "anything but a list reads as none")
  -- a KB file opened in Neovim, with a session up, is noted
  vim.cmd("edit " .. vim.fn.fnameescape(kb .. "/notes/owls.md"))
  t.ok(poll(5000, function() local r = recent() return r[1] and r[1].path == "notes/owls.md" end),
    "opening a KB file puts it first among the recent files")
  vim.cmd("bwipeout!")
end)

t.section("backlinks", function()
  local p
  poll(15000, function()
    p = present_wait(function() finders.backlinks(kb .. "/notes/owls.md") end)
    return p ~= nil and #p.items > 0
  end)
  t.eq(p and vim.tbl_map(function(it) return it.path end, p.items), { "birds.md" }, "birds.md links to notes/owls.md")
  t.ok(p and p.title:find("links to notes/owls.md", 1, true) ~= nil, "the title names the file", p and p.title)
end)

t.section("restart", function()
  local before = session.client()
  local was = before and before:hello().instance
  local c, err = t.await(30000, function(done) session.restart(done) end)
  t.ok(c ~= nil, "the daemon restarted and the session reconnected", err)
  t.ok(c and was and c:hello().instance ~= was, "a new daemon instance answers", c and c:hello().instance)
  t.ok(before and not before:is_ready(), "the old connection is closed")
end)

t.section("the offer to restart an older daemon", function()
  local asked, restarted = {}, 0
  local real_select, real_restart, real_connect = vim.ui.select, session.restart, client.connect
  vim.ui.select = function(_, opts, cb) asked[#asked + 1] = opts.prompt; cb("Restart the daemon", 1) end
  session.restart = function(cb) restarted = restarted + 1; cb(nil, nil) end
  local older = { protocol = client.PROTOCOL - 1, min_protocol = client.PROTOCOL - 1, version = "v-old", instance = "i1", pid = 7 }

  -- a refused connect is what brings the offer: the probe's answer reaches it
  session.reset_for_tests()
  autodoc.setup({ bin = bin, config = cfg })
  client.connect = function(_, cb) cb(nil, client.mismatch(older), older) end
  local _, err = t.await(10000, function(done) session.ensure(done) end)
  t.ok(err and err:find("DAEMON is older", 1, true), "the connect is refused", err)
  t.ok(t.wait(5000, function() return #asked == 1 end), "and the restart is offered", vim.inspect(asked))
  t.ok(asked[1] and asked[1]:find("autodoc v-old", 1, true) and asked[1]:find("protocol " .. (client.PROTOCOL - 1), 1, true),
    "naming the daemon's version and protocol", asked[1])
  t.eq(restarted, 1, "a yes restarts it")

  -- the question comes after `bin --version` answers: give a second one time to come
  local function settled() vim.wait(2000, function() return #asked > 1 end) return #asked end
  session.offer_restart(older)
  t.eq(settled(), 1, "the same daemon instance is not asked about twice")
  session.offer_restart({ protocol = client.PROTOCOL + 1, min_protocol = client.PROTOCOL + 1, instance = "i2" })
  t.eq(settled(), 1, "a NEWER daemon is not offered a restart: the plugin is the older side")
  session.configure({ bin = bin, config = cfg, offer_restart = false })
  session.offer_restart(vim.tbl_extend("force", older, { instance = "i3" }))
  t.eq(settled(), 1, "offer_restart = false asks nothing")
  -- the cells above observe a second question: an older daemon of a new instance is asked about
  session.configure({ bin = bin, config = cfg })
  session.offer_restart(vim.tbl_extend("force", older, { instance = "i5" }))
  t.ok(t.wait(5000, function() return #asked == 2 end), "another older daemon instance is asked about")
  table.remove(asked)
  session.configure({ bin = bin, config = cfg })
  client.connect = real_connect

  -- a restart that failed leaves the session restarted: the next older daemon is reported, not offered
  session.restart = real_restart
  local real_endpoint = lifecycle.endpoint
  lifecycle.endpoint = function(_, _, cb) cb(nil, "no endpoint") end
  local _, rerr = t.await(5000, function(done) session.restart(done) end)
  lifecycle.endpoint = real_endpoint
  t.eq(rerr, "no endpoint", "a failed restart says why")
  local said
  local real_notify = vim.notify
  vim.notify = function(msg) said = msg end
  session.offer_restart(vim.tbl_extend("force", older, { instance = "i4" }))
  vim.wait(2000, function() return #asked > 1 end)
  vim.notify = real_notify
  t.eq(#asked, 1, "after this session's own restart, an older daemon is not offered again (no loop)")
  t.ok(said and said:find("its binary is older", 1, true), "it says the binary is the older one", said)
  vim.ui.select = real_select
  session.reset_for_tests()
  autodoc.setup({ bin = bin, config = cfg })
end)

t.section("install: the release binary", function()
  local install = require("autodoc.install")
  t.eq(install.platform({ sysname = "Linux", machine = "x86_64" }), "linux-amd64", "Linux x86_64")
  t.eq(install.platform({ sysname = "Darwin", machine = "arm64" }), "darwin-arm64", "macOS arm64")
  t.eq(install.platform({ sysname = "Linux", machine = "aarch64" }), "linux-arm64", "Linux aarch64")
  t.eq(install.platform({ sysname = "Windows_NT", machine = "x86_64" }), nil, "no asset for Windows")
  t.eq(install.parse_sha256(string.rep("a", 64) .. "  autodoc.tar.gz\n"), string.rep("a", 64), "a shasum line")
  t.eq(install.parse_sha256("deadbeef  x"), nil, "a short sum is none")

  -- a release laid out as GitHub serves it, under a file:// base
  local plat = assert(install.platform())
  local rel = t.tmp("release")
  local name = "autodoc-v9.9.9-" .. plat
  vim.fn.mkdir(rel .. "/v9.9.9", "p")
  vim.fn.writefile({ "#!/bin/sh", "echo autodoc v9.9.9" }, rel .. "/" .. name)
  vim.fn.system({ "chmod", "+x", rel .. "/" .. name })
  vim.fn.system({ "tar", "-czf", rel .. "/v9.9.9/" .. name .. ".tar.gz", "-C", rel, name })
  local sum = vim.fn.system({ "sh", "-c", "sha256sum " .. vim.fn.shellescape(rel .. "/v9.9.9/" .. name .. ".tar.gz") })
  vim.fn.writefile({ sum }, rel .. "/v9.9.9/" .. name .. ".tar.gz.sha256")
  local base = install.BASE
  install.BASE = "file://" .. rel

  local dir = t.tmp("plugin")
  vim.fn.mkdir(dir .. "/bin", "p")
  vim.fn.writefile({ "old" }, dir .. "/bin/autodoc")
  -- inside a coroutine, as a lazy build task or :AutodocMaintenance runs it
  local ok, err, finished
  coroutine.wrap(function()
    ok, err = install.download(dir, "v9.9.9", function() end)
    finished = true
  end)()
  t.ok(t.wait(20000, function() return finished end), "the download finishes in a coroutine")
  t.ok(ok, "the release binary is installed", err)
  t.eq(vim.fn.system({ dir .. "/bin/autodoc" }), "autodoc v9.9.9\n", "bin/autodoc is the release's, and runs")
  t.eq(vim.fn.glob(dir .. "/bin/.autodoc.new"), "", "nothing staged is left behind")

  -- a checksum that does not match installs nothing
  vim.fn.writefile({ "old" }, dir .. "/bin/autodoc")
  vim.fn.writefile({ string.rep("0", 64) .. "  " .. name .. ".tar.gz" }, rel .. "/v9.9.9/" .. name .. ".tar.gz.sha256")
  local bad_ok, bad_err = install.download(dir, "v9.9.9", function() end)
  t.ok(not bad_ok and bad_err:find("checksum mismatch", 1, true), "a bad checksum is refused", bad_err)
  t.eq(vim.fn.readfile(dir .. "/bin/autodoc"), { "old" }, "and the binary there is untouched")
  local miss_ok, miss_err = install.download(dir, "v0.0.0", function() end)
  t.ok(not miss_ok and miss_err:find("download failed", 1, true), "a missing release is a failed download", miss_err)
  install.BASE = base

  -- the tag: on a release tag, the release; elsewhere, make build
  local repo = t.tmp("repo")
  vim.fn.mkdir(repo, "p")
  vim.fn.system({ "sh", "-c", "cd " .. vim.fn.shellescape(repo)
    .. " && git init -q && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m x" })
  t.eq(install.tag(repo), nil, "an untagged commit has no release")
  vim.fn.system({ "git", "-C", repo, "tag", "v9.9.9" })
  t.eq(install.tag(repo), "v9.9.9", "a release tag is found")
  local built
  local real_build, real_download = install.build, install.download
  install.build = function(d) built = d; return true end
  install.download = function() return false, "offline" end
  local rok, rmsg = install.run({ dir = repo, log = function() end })
  t.ok(rok and built == repo and rmsg:find("offline", 1, true), "a failed download falls back to make build, saying why", rmsg)
  built = nil
  vim.fn.system({ "git", "-C", repo, "tag", "-d", "v9.9.9" })
  rok, rmsg = install.run({ dir = repo, log = function() end })
  t.ok(rok and built == repo and rmsg:find("not on a release tag", 1, true), "off a tag it builds", rmsg)
  install.build, install.download = real_build, real_download
end)

t.section("versions", function()
  local m = require("autodoc.maintenance")
  local lines, warn = m.describe({ daemon = { version = "v0.1.18", protocol = 13, min_protocol = 12, server_protocol = 13, pid = 1 },
    bin = "/p/bin/autodoc", bin_version = "v0.1.18", path_bin = "/u/bin/autodoc", path_version = "v0.1.14" })
  t.ok(warn and table.concat(lines, "\n"):find("ln -sf /p/bin/autodoc /u/bin/autodoc", 1, true),
    "a TUI on PATH of another build is warned about, with the link that mends it", table.concat(lines, "\n"))
  local _, same = m.describe({ daemon = { version = "v0.1.18" }, bin = "/p/bin/autodoc", bin_version = "v0.1.18",
    path_bin = "/u/bin/autodoc", path_version = "v0.1.18" })
  t.ok(not same, "the same build on PATH is no warning")
  t.eq(m.path_binary(nil) == nil or type(m.path_binary(nil)) == "string", true, "path_binary answers")
end)

t.finish()
