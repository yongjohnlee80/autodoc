-- Shared by the specs: the runtimepath, a tiny test runner, and waiting.
local M = {}

local root = vim.fn.getcwd()
vim.opt.runtimepath:prepend(root)
vim.opt.runtimepath:prepend(assert(os.getenv("AUTODOC_TEST_AUTOCORE"), "AUTODOC_TEST_AUTOCORE"))
package.path = root .. "/tests/?.lua;" .. package.path

M.passed, M.failed = 0, 0

function M.ok(cond, name, detail)
  if cond then
    M.passed = M.passed + 1
    print("ok   " .. name)
  else
    M.failed = M.failed + 1
    print("FAIL " .. name .. (detail and (": " .. tostring(detail)) or ""))
  end
end

function M.eq(got, want, name)
  M.ok(vim.deep_equal(got, want), name, string.format("got %s, want %s", vim.inspect(got), vim.inspect(want)))
end

---section runs fn, counting an error as a failure rather than ending the suite.
function M.section(name, fn)
  print("-- " .. name)
  local ok, err = xpcall(fn, debug.traceback)
  if not ok then M.ok(false, name .. " (raised)", err) end
end

---wait pumps the event loop until cond() or timeout; returns whether cond held.
function M.wait(ms, cond) return vim.wait(ms, cond, 20) end

---await runs fn(done) and waits for done(...) up to ms; returns its arguments.
function M.await(ms, fn)
  local args, finished = nil, false
  fn(function(...) args, finished = { ... }, true end)
  vim.wait(ms, function() return finished end, 20)
  if not finished then return nil, "timed out" end
  return unpack(args)
end

function M.tmp(...) return table.concat({ assert(os.getenv("AUTODOC_TEST_TMP")), ... }, "/") end

function M.finish()
  print(string.format("SUMMARY: %d passed, %d failed", M.passed, M.failed))
  os.exit(M.failed == 0 and 0 or 1)
end

return M
