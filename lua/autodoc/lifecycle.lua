---Find the AutoDoc binary, and the daemon through it.
---
---The binary is asked where the daemon is (`autodoc --print-endpoint`), never second-guessed: it
---resolves the config's socket, or another config's when that daemon serves the same store, from
---one resolver the TUI and `--call` share. `--ensure` makes it start the daemon first when nothing
---answers, through the same code path the TUI uses, restart handoffs and all, so this plugin never
---spawns a daemon of its own. When something cannot be resolved with confidence, it stops and
---says what it found, with the manual command (a convenience, not an orchestrator).
---@module 'autodoc.lifecycle'

local M = {}

M.BINARY_NAME = "autodoc"

---plugin_root is this plugin's directory, from this file's own path.
---@return string
function M.plugin_root()
  local src = debug.getinfo(1, "S").source:sub(2)
  return vim.fn.fnamemodify(vim.fn.fnamemodify(src, ":p"), ":h:h:h")
end

---binary_candidates lists where the binary may be, in order. The plugin's own build comes before
---PATH: it is the same commit as this Lua, so its protocol matches; a packaged autodoc on PATH is
---the fallback, not the default.
---@param configured string?
---@return { label: string, path: string }[]
function M.binary_candidates(configured)
  local out = {}
  if configured and configured ~= "" then
    out[#out + 1] = { label = "configured (opts.bin)", path = vim.fn.expand(configured) }
  end
  out[#out + 1] = { label = "plugin build (lazy `build = \"make build\"`)", path = M.plugin_root() .. "/bin/" .. M.BINARY_NAME }
  out[#out + 1] = { label = "PATH (Homebrew, mise, go install)", path = M.BINARY_NAME }
  out[#out + 1] = { label = "managed cache", path = vim.fn.stdpath("data") .. "/autodoc/bin/" .. M.BINARY_NAME }
  return out
end

---describe_manual is a failure that says what was tried and how to run the daemon by hand.
---@param msg string
---@param tried string[]?
---@return string
function M.describe_manual(msg, tried)
  local lines = { "autodoc: " .. msg }
  if tried and #tried > 0 then
    lines[#lines + 1] = "searched:"
    for _, t in ipairs(tried) do lines[#lines + 1] = "  " .. t end
  end
  lines[#lines + 1] = "By hand: build it (make build in the autodoc checkout), then run `autodoc --serve`."
  return table.concat(lines, "\n")
end

---resolve_binary finds the executable, or explains what it looked at. An opts.bin is honoured or
---refused, never quietly replaced by another build.
---@param configured string?
---@return string|nil path, string|nil err, string|nil label
function M.resolve_binary(configured)
  if configured and configured ~= "" then
    local explicit = vim.fn.expand(configured)
    if vim.fn.executable(explicit) == 1 then return explicit, nil, "configured (opts.bin)" end
    return nil, M.describe_manual(string.format("opts.bin is %q, which is not executable", explicit))
  end
  local tried = {}
  for _, c in ipairs(M.binary_candidates(nil)) do
    if vim.fn.executable(c.path) == 1 then
      local resolved = c.path == M.BINARY_NAME and vim.fn.exepath(M.BINARY_NAME) or c.path
      return resolved, nil, c.label
    end
    tried[#tried + 1] = string.format("%-44s %s", c.label, c.path)
  end
  return nil, M.describe_manual("no autodoc executable found", tried)
end

---parse_endpoint reads `--print-endpoint`'s one line, `unix<TAB><socket>`.
---@param out string
---@return string|nil
function M.parse_endpoint(out)
  local addr = tostring(out or ""):match("^unix\t([^\r\n]+)")
  return addr
end

---endpoint asks bin where the daemon is; with ensure it starts the daemon first when nothing
---answers. `cb(addr, err)` fires once, on the main loop.
---@param bin string
---@param opts { ensure: boolean?, config: string? }
---@param cb fun(addr: string|nil, err: string|nil)
function M.endpoint(bin, opts, cb)
  local cmd = { bin, "--print-endpoint" }
  if opts.ensure then cmd[#cmd + 1] = "--ensure" end
  if opts.config and opts.config ~= "" then
    cmd[#cmd + 1] = "--config"
    cmd[#cmd + 1] = vim.fn.expand(opts.config)
  end
  local ok, err = pcall(vim.system, cmd, { text = true }, vim.schedule_wrap(function(res)
    if res.code ~= 0 then
      return cb(nil, M.describe_manual(string.format("`%s` failed: %s", table.concat(cmd, " "),
        vim.trim(res.stderr or ""))))
    end
    local addr = M.parse_endpoint(res.stdout)
    if not addr then
      return cb(nil, "autodoc: --print-endpoint printed no endpoint: " .. vim.trim(res.stdout or ""))
    end
    cb(addr, nil)
  end))
  if not ok then cb(nil, "autodoc: running " .. bin .. ": " .. tostring(err)) end
end

return M
