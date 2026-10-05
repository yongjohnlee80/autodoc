-- autodoc.kb.util: the file, path, hash and JSON helpers the KB tooling shares.
--
-- Every write that matters goes through write_file(), which fsyncs the file before it returns, so a
-- caller that has written a pre-image and verified it can rely on it surviving a crash.

local uv = vim.uv or vim.loop

local M = {}

-- ─── paths ──────────────────────────────────────────────────────────

---Normalize a slash-separated path: collapse `//`, drop `.`, resolve `..` where it can. A relative
---path that climbs above its start keeps its leading `..` segments.
---@param p string
---@return string
function M.normpath(p)
  local abs = p:sub(1, 1) == "/"
  local out = {}
  for seg in p:gmatch("[^/]+") do
    if seg == "." then
      -- skip
    elseif seg == ".." then
      if #out > 0 and out[#out] ~= ".." then
        out[#out] = nil
      elseif not abs then
        out[#out + 1] = ".."
      end
    else
      out[#out + 1] = seg
    end
  end
  local s = table.concat(out, "/")
  if abs then return "/" .. s end
  return s == "" and "." or s
end

---The directory part of a relative path ("" for a top-level file).
function M.dirname(p)
  local d = p:match("^(.*)/[^/]*$")
  return d or ""
end

function M.basename(p)
  return p:match("([^/]*)$")
end

---Join relative path parts, skipping empty ones.
function M.join(...)
  local parts = {}
  for _, s in ipairs({ ... }) do
    if s and s ~= "" then parts[#parts + 1] = s end
  end
  return table.concat(parts, "/")
end

---The relative path from directory `from` (relative to the same root; "" is the root) to `to`.
function M.relpath(to, from)
  local a, b = {}, {}
  for s in to:gmatch("[^/]+") do a[#a + 1] = s end
  for s in (from or ""):gmatch("[^/]+") do b[#b + 1] = s end
  local i = 1
  while i <= #a and i <= #b and a[i] == b[i] do i = i + 1 end
  local out = {}
  for _ = i, #b do out[#out + 1] = ".." end
  for j = i, #a do out[#out + 1] = a[j] end
  local s = table.concat(out, "/")
  return s == "" and "." or s
end

---Decode %XX escapes (a Markdown link's target).
function M.unquote(s)
  return (s:gsub("%%(%x%x)", function(h) return string.char(tonumber(h, 16)) end))
end

---Unicode-agnostic case fold: ASCII lower case (paths in a KB are ASCII in practice; bytes above
---0x7f pass through, so two names that differ only there never fold together).
function M.fold(s)
  return s:lower()
end

-- ─── files ──────────────────────────────────────────────────────────

function M.stat(path)
  return uv.fs_stat(path)
end

function M.exists(path)
  return uv.fs_stat(path) ~= nil
end

function M.isdir(path)
  local st = uv.fs_stat(path)
  return st ~= nil and st.type == "directory"
end

function M.isfile(path)
  local st = uv.fs_stat(path)
  return st ~= nil and st.type == "file"
end

---Read a whole file as bytes, or nil and an error.
function M.read_file(path)
  local fd, err = uv.fs_open(path, "r", 438)
  if not fd then return nil, err end
  local st = uv.fs_fstat(fd)
  local data = st and uv.fs_read(fd, st.size, 0) or nil
  uv.fs_close(fd)
  if not data then return nil, "read failed: " .. path end
  return data
end

---mkdir -p. Returns the directories it created, outermost first.
function M.mkdirp(path)
  local created = {}
  if M.isdir(path) then return created end
  local parent = path:match("^(.*)/[^/]+$")
  if parent and parent ~= "" then
    for _, d in ipairs(M.mkdirp(parent)) do created[#created + 1] = d end
  end
  local ok, err = uv.fs_mkdir(path, 493)
  if not ok and not M.isdir(path) then error("mkdir " .. path .. ": " .. tostring(err)) end
  if ok then created[#created + 1] = path end
  return created
end

local function fsync_dir(dir)
  local fd = uv.fs_open(dir, "r", 0)
  if fd then
    pcall(uv.fs_fsync, fd)
    uv.fs_close(fd)
  end
end

---Write bytes to path, creating its parents, and fsync the file and its directory. Writes through a
---sibling temp file and a rename, so a reader never sees half a file.
function M.write_file(path, data)
  local dir = path:match("^(.*)/[^/]+$")
  if dir then M.mkdirp(dir) end
  local tmp = path .. ".autodoc-tmp"
  local fd, err = uv.fs_open(tmp, "w", 420)
  if not fd then error("open " .. tmp .. ": " .. tostring(err)) end
  local off = 0
  while off < #data do
    local n, werr = uv.fs_write(fd, data:sub(off + 1), off)
    if not n then
      uv.fs_close(fd)
      error("write " .. tmp .. ": " .. tostring(werr))
    end
    off = off + n
  end
  uv.fs_fsync(fd)
  uv.fs_close(fd)
  -- keep the original's mode when replacing a file (a script stays executable)
  local st = uv.fs_stat(path)
  if st then uv.fs_chmod(tmp, st.mode % 4096) end
  local ok, rerr = uv.fs_rename(tmp, path)
  if not ok then error("rename " .. tmp .. ": " .. tostring(rerr)) end
  if dir then fsync_dir(dir) end
end

---Copy a file byte for byte, keeping its mode, fsynced.
function M.copy_file(src, dst)
  local data, err = M.read_file(src)
  if not data then error("read " .. src .. ": " .. tostring(err)) end
  M.write_file(dst, data)
  local st = uv.fs_stat(src)
  if st then uv.fs_chmod(dst, st.mode % 4096) end
end

function M.rename(src, dst)
  local dir = dst:match("^(.*)/[^/]+$")
  if dir then M.mkdirp(dir) end
  local ok, err = uv.fs_rename(src, dst)
  if not ok then error("rename " .. src .. " -> " .. dst .. ": " .. tostring(err)) end
end

function M.remove(path)
  local ok, err = uv.fs_unlink(path)
  if not ok then error("unlink " .. path .. ": " .. tostring(err)) end
end

---Remove a directory tree (the pre-image's own directory; never the KB).
function M.rmtree(path)
  local st = uv.fs_lstat(path)
  if not st then return end
  if st.type == "directory" then
    local h = uv.fs_scandir(path)
    while h do
      local name = uv.fs_scandir_next(h)
      if not name then break end
      M.rmtree(path .. "/" .. name)
    end
    uv.fs_rmdir(path)
  else
    uv.fs_unlink(path)
  end
end

---Walk a tree. Returns sorted relative file paths and relative directory paths. `skip` names
---directories (by basename) never entered; `skip_rel` names relative directories never entered.
---Symbolic links are listed as files and never followed.
function M.walk(root, skip, skip_rel)
  skip = skip or {}
  skip_rel = skip_rel or {}
  local files, dirs = {}, {}
  local function rec(abs, rel)
    local h = uv.fs_scandir(abs)
    if not h then return end
    while true do
      local name, typ = uv.fs_scandir_next(h)
      if not name then break end
      local r = rel == "" and name or (rel .. "/" .. name)
      local a = abs .. "/" .. name
      if typ == nil or typ == "unknown" then
        local st = uv.fs_lstat(a)
        typ = st and st.type or "file"
      end
      if typ == "directory" then
        if not skip[name] and not skip_rel[r] then
          dirs[#dirs + 1] = r
          rec(a, r)
        end
      else
        files[#files + 1] = r
      end
    end
  end
  rec(root, "")
  table.sort(files)
  table.sort(dirs)
  return files, dirs
end

-- ─── hashing ────────────────────────────────────────────────────────

function M.sha256(data)
  return vim.fn.sha256(data)
end

function M.sha256_file(path)
  local data = M.read_file(path)
  if not data then return nil end
  return vim.fn.sha256(data)
end

-- ─── lines ──────────────────────────────────────────────────────────

---Split text into lines, keeping whether it ended with a newline.
function M.split_lines(text)
  local lines = vim.split(text, "\n", { plain = true })
  local trailing = false
  if lines[#lines] == "" then
    trailing = true
    lines[#lines] = nil
  end
  return lines, trailing
end

function M.join_lines(lines, trailing)
  local s = table.concat(lines, "\n")
  if trailing then s = s .. "\n" end
  return s
end

-- ─── versions ───────────────────────────────────────────────────────

---Parse "v1.2.3" / "1.2.3-rc1" into {1,2,3}; nil when it is not a version.
function M.parse_semver(v)
  if type(v) ~= "string" then return nil end
  local a, b, c = v:match("^%s*v?(%d+)%.(%d+)%.(%d+)")
  if not a then return nil end
  return { tonumber(a), tonumber(b), tonumber(c) }
end

----1, 0 or 1 as version a is older than, the same as, or newer than b. Unparseable versions
---compare as nil.
function M.semver_cmp(a, b)
  local x, y = M.parse_semver(a), M.parse_semver(b)
  if not x or not y then return nil end
  for i = 1, 3 do
    if x[i] < y[i] then return -1 end
    if x[i] > y[i] then return 1 end
  end
  return 0
end

-- ─── JSON ───────────────────────────────────────────────────────────

local function is_array(t)
  if next(t) == nil then return true end
  local n = 0
  for k in pairs(t) do
    if type(k) ~= "number" then return false end
    n = n + 1
  end
  return n == #t
end

local function encode(v, indent, depth, flat_at)
  local t = type(v)
  if t ~= "table" then
    if v == nil then return "null" end
    return vim.json.encode(v)
  end
  if v == vim.NIL then return "null" end
  local arr = is_array(v)
  if next(v) == nil then return arr and "[]" or "{}" end
  local flat = depth >= flat_at
  local pad = flat and "" or ("\n" .. string.rep("  ", depth + 1))
  local close = flat and "" or ("\n" .. string.rep("  ", depth))
  local sep = flat and "," or ","
  local parts = {}
  if arr then
    for _, x in ipairs(v) do parts[#parts + 1] = pad .. encode(x, indent, depth + 1, flat_at) end
    return "[" .. table.concat(parts, sep) .. close .. "]"
  end
  local keys = {}
  for k in pairs(v) do keys[#keys + 1] = tostring(k) end
  table.sort(keys)
  for _, k in ipairs(keys) do
    local x = v[k]
    if x == nil then x = v[tonumber(k)] end
    parts[#parts + 1] = pad .. vim.json.encode(k) .. (flat and ":" or ": ") .. encode(x, indent, depth + 1, flat_at)
  end
  return "{" .. table.concat(parts, sep) .. close .. "}"
end

---Encode as JSON with sorted keys; structures deeper than `flat_at` (default 3) stay on one line,
---so a manifest reads one record per line.
function M.json_encode(v, flat_at)
  return encode(v, "  ", 0, flat_at or 3) .. "\n"
end

function M.json_decode(s)
  return vim.json.decode(s, { luanil = { object = true, array = true } })
end

function M.read_json(path)
  local data, err = M.read_file(path)
  if not data then return nil, err end
  local ok, v = pcall(M.json_decode, data)
  if not ok then return nil, v end
  return v
end

-- ─── processes ──────────────────────────────────────────────────────

---Run a command synchronously; returns code, stdout, stderr.
function M.run(cmd, opts)
  local r = vim.system(cmd, { text = true, cwd = opts and opts.cwd, stdin = opts and opts.stdin, env = opts and opts.env }):wait()
  return r.code, r.stdout or "", r.stderr or ""
end

return M
