---Install the AutoDoc binary into this plugin's `bin/autodoc`: the release asset built for the
---plugin's own tag, checksummed, else a local `make build`.
---
---The release binary is the same commit as this Lua (so its protocol matches), needs no Go and no
---Command Line Tools, and on macOS is built with cgo on a Mac, so it keeps the FSEvents watcher.
---A checkout that is not on a release tag (a dev branch, a worktree), a platform without an asset,
---or a download or checksum that fails falls back to `make build`, which needs Go.
---
---lazy.nvim runs it through the repository's `build.lua`, inside its build task: each step then
---suspends the task instead of blocking the editor. `:AutodocMaintenance` runs it too.
---@module 'autodoc.install'

local M = {}

M.REPO = "yongjohnlee80/autodoc"
-- Where the release assets are, `<tag>/<asset>` under it (a test points it at a local directory).
M.BASE = "https://github.com/" .. M.REPO .. "/releases/download"

---platform is the release's os-arch suffix for this machine, or nil when no asset is built for it.
---@param uname table? vim.uv.os_uname()'s shape (a test seam)
---@return string|nil
function M.platform(uname)
  uname = uname or vim.uv.os_uname()
  local os_name = ({ Linux = "linux", Darwin = "darwin" })[uname.sysname]
  local arch = ({ x86_64 = "amd64", amd64 = "amd64", aarch64 = "arm64", arm64 = "arm64" })[uname.machine]
  if not os_name or not arch then return nil end
  return os_name .. "-" .. arch
end

---flavour is the release asset's suffix for this machine: "-gui" on Linux with a graphical session
---(WAYLAND_DISPLAY or DISPLAY set), the build that carries --gui and needs the window system's
---libraries, which a desktop has; "" elsewhere: macOS's one build carries the GUI, and a Linux with
---no display takes the static TUI. AUTODOC_GUI=1 or 0 chooses instead.
---@param platform string? M.platform()'s
---@param getenv (fun(name: string): string?)? os.getenv's shape (a test seam)
function M.flavour(platform, getenv)
  getenv = getenv or os.getenv
  if not platform or not platform:match("^linux%-") then return "" end
  local choice = getenv("AUTODOC_GUI")
  if choice == "1" then return "-gui" end
  if choice == "0" then return "" end
  if (getenv("WAYLAND_DISPLAY") or "") ~= "" or (getenv("DISPLAY") or "") ~= "" then return "-gui" end
  return ""
end

---asset is the release asset's name and URL for tag on platform.
---@param tag string
---@param platform string
---@return string name, string url
function M.asset(tag, platform)
  local name = string.format("autodoc-%s-%s", tag, platform)
  return name, string.format("%s/%s/%s.tar.gz", M.BASE, tag, name)
end

---parse_sha256 reads a `shasum -a 256` line: the hex digest first, or nil.
---@param text string
---@return string|nil
function M.parse_sha256(text)
  local sum = tostring(text or ""):match("^%s*(%x+)")
  if sum and #sum == 64 then return sum:lower() end
  return nil
end

---step runs cmd and returns its vim.SystemCompleted without blocking the editor when it can:
---inside a lazy build task the task suspends (lazy's executor resumes it); inside another
---coroutine (`:AutodocMaintenance`) that coroutine yields; on the main thread it waits.
---@param cmd string[]
---@param opts table?
---@return vim.SystemCompleted
local function step(cmd, opts)
  local lok, lazy_async = pcall(require, "lazy.async")
  local async = lok and type(lazy_async.running) == "function" and lazy_async.running() or nil
  local co = coroutine.running()
  if not async and not co then
    local ok, obj = pcall(vim.system, cmd, opts or {})
    if not ok then return { code = -1, stdout = "", stderr = tostring(obj) } end
    return obj:wait()
  end
  local res
  local ok, err = pcall(vim.system, cmd, opts or {}, function(r)
    res = r
    vim.schedule(function()
      if async then async:resume() else coroutine.resume(co) end
    end)
  end)
  if not ok then return { code = -1, stdout = "", stderr = tostring(err) } end
  while not res do
    if async then async:suspend() else coroutine.yield() end
  end
  return res
end
M._step = step

---tag is the release tag the checkout at dir is on, or nil (a branch, an untagged commit).
---@param dir string
---@return string|nil
function M.tag(dir)
  local res = step({ "git", "-C", dir, "describe", "--tags", "--exact-match", "HEAD" }, { text = true })
  if res.code ~= 0 then return nil end
  local tag = vim.trim(res.stdout or "")
  return tag:match("^v%d+%.%d+%.%d+") and tag or nil
end

---sha256 is file's SHA-256, from sha256sum or shasum.
---@param file string
---@return string|nil sum, string|nil err
local function sha256(file)
  for _, cmd in ipairs({ { "sha256sum", file }, { "shasum", "-a", "256", file } }) do
    if vim.fn.executable(cmd[1]) == 1 then
      local res = step(cmd, { text = true })
      if res.code == 0 then return M.parse_sha256(res.stdout), nil end
      return nil, cmd[1] .. ": " .. vim.trim(res.stderr or "")
    end
  end
  return nil, "neither sha256sum nor shasum is installed"
end

---download fetches tag's release binary for this platform into dir/bin/autodoc, checksummed, and
---moves it into place only once it is whole (a running daemon keeps the file it started from).
---@param dir string the plugin's root
---@param tag string
---@param log fun(msg: string)
---@return boolean ok, string|nil err
function M.download(dir, tag, log)
  local platform = M.platform()
  if not platform then
    local u = vim.uv.os_uname()
    return false, string.format("no release binary is built for %s/%s", u.sysname, u.machine)
  end
  for _, tool in ipairs({ "curl", "tar" }) do
    if vim.fn.executable(tool) ~= 1 then return false, tool .. " is not installed" end
  end
  local flavour = M.flavour(platform)
  local ok, err = M.fetch(dir, tag, platform .. flavour, log)
  if not ok and flavour ~= "" then
    -- a release from before the GUI builds, or one without this one: the static TUI still serves
    log("no GUI build (" .. tostring(err) .. "); installing the TUI build")
    ok, err = M.fetch(dir, tag, platform, log)
  end
  return ok, err
end

---fetch installs tag's asset for platform (an os-arch, with its flavour) into dir/bin/autodoc.
---@return boolean ok, string|nil err
function M.fetch(dir, tag, platform, log)
  local name, url = M.asset(tag, platform)
  local tmp = vim.fn.tempname()
  vim.fn.mkdir(tmp, "p")
  local function cleanup() vim.fn.delete(tmp, "rf") end
  local tarball = tmp .. "/" .. name .. ".tar.gz"
  log("downloading " .. url)
  for _, f in ipairs({ { url, tarball }, { url .. ".sha256", tarball .. ".sha256" } }) do
    local res = step({ "curl", "-fsSL", "--retry", "2", "-o", f[2], f[1] }, { text = true })
    if res.code ~= 0 then
      cleanup()
      return false, "download failed: " .. f[1] .. ": " .. vim.trim(res.stderr or "")
    end
  end
  local want = M.parse_sha256(table.concat(vim.fn.readfile(tarball .. ".sha256"), "\n"))
  local got, serr = sha256(tarball)
  if not want or not got or want ~= got then
    cleanup()
    return false, string.format("checksum mismatch for %s.tar.gz (published %s, downloaded %s)%s", name,
      tostring(want), tostring(got), serr and (": " .. serr) or "")
  end
  local res = step({ "tar", "-xzf", tarball, "-C", tmp }, { text = true })
  if res.code ~= 0 or vim.fn.filereadable(tmp .. "/" .. name) ~= 1 then
    cleanup()
    return false, "unpacking " .. name .. ".tar.gz failed: " .. vim.trim(res.stderr or "")
  end
  vim.fn.mkdir(dir .. "/bin", "p")
  local staged = dir .. "/bin/.autodoc.new"
  local target = dir .. "/bin/autodoc"
  -- copy across filesystems (the temp dir may be tmpfs), then rename within bin/: atomic
  local cp = step({ "cp", tmp .. "/" .. name, staged }, { text = true })
  cleanup()
  if cp.code ~= 0 then return false, "copying the binary into bin/ failed: " .. vim.trim(cp.stderr or "") end
  vim.uv.fs_chmod(staged, 493) -- 0755
  local ok, rerr = os.rename(staged, target)
  if not ok then
    os.remove(staged)
    return false, "moving the binary into place failed: " .. tostring(rerr)
  end
  return true, nil
end

---build runs `make build` in dir: the local build, which needs Go.
---@param dir string
---@param log fun(msg: string)
---@return boolean ok, string|nil err
function M.build(dir, log)
  if vim.fn.executable("make") ~= 1 or vim.fn.executable("go") ~= 1 then
    return false, "make build needs make and Go, and " .. (vim.fn.executable("go") ~= 1 and "go" or "make") .. " is not on PATH"
  end
  log("building from source (make build)")
  local res = step({ "make", "build" }, { cwd = dir, text = true })
  if res.code ~= 0 then
    return false, "make build failed: " .. vim.trim((res.stderr or "") ~= "" and res.stderr or res.stdout or "")
  end
  return true, nil
end

---run installs bin/autodoc for the plugin at dir: the release binary for its tag, else make build.
---It returns what happened; build.lua raises a failure, so lazy reports the build failed.
---@param opts { dir: string?, log: fun(msg: string)?, prefer_build: boolean? }?
---@return boolean ok, string msg
function M.run(opts)
  opts = opts or {}
  local dir = opts.dir or require("autodoc.lifecycle").plugin_root()
  local log = opts.log or function(msg) print("autodoc: " .. msg) end
  local tag = not opts.prefer_build and M.tag(dir) or nil
  local why
  if tag then
    local ok, err = M.download(dir, tag, log)
    if ok then return true, "installed the release binary for " .. tag end
    why = err
    log(err .. "; building from source instead")
  else
    why = opts.prefer_build and "a source build was asked for" or "this checkout is not on a release tag"
  end
  local ok, err = M.build(dir, log)
  if ok then return true, "built from source (" .. why .. ")" end
  return false, string.format("no binary installed: %s; and %s", why, err)
end

return M
