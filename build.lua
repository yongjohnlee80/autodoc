-- lazy.nvim runs this after an install or update (and on :Lazy build autodoc): it puts the AutoDoc
-- binary in bin/autodoc, the release asset built for this checkout's tag, checksummed, else a
-- local `make build` (autodoc.install). Plugin managers that do not run build.lua can call
-- require("autodoc.install").run(), or run `make build` here.
local dir = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h")
local install = dofile(dir .. "/lua/autodoc/install.lua")
local ok, msg = install.run({ dir = dir })
if not ok then error("autodoc: " .. msg) end
print("autodoc: " .. msg)
