-- autodoc.kb.migrate.schema_check: the migration's offline copy of the daemon's frontmatter rules.
--
-- Offline copy for migrate's planned tree; parity: tests/kbparity_spec.lua. Validating a document is
-- the daemon's (doc.validate, against the workspace's schema). The migration cannot ask it: it
-- counts diagnostics of texts it has planned and not written, against the schema AutoDoc ships
-- (written into the KB by the same apply), for a KB that need not be a workspace yet. Nothing but
-- the migration uses this module; tests/kbparity_spec.lua holds it to doc.validate.

local schema = require("autodoc.kb.schema")
local frontmatter = require("autodoc.kb.frontmatter")

local M = {}

local function enum_has(enum, v)
  for _, e in ipairs(enum) do
    if e == v then return true end
  end
  return false
end

local function check_scalar(ftype, v)
  if ftype == "integer" then return v:match("^%-?%d+$") ~= nil end
  if ftype == "number" then return tonumber(v) ~= nil end
  if ftype == "boolean" then return v == "true" or v == "false" end
  if ftype == "date" then return v:match("^%d%d%d%d%-%d%d%-%d%d") ~= nil end
  return true
end

---Diagnose a document's text against the schema: a list of {field, rule, message}. Mirrors
---core/schema's rules at the level the migration reports them (required, enum, type, unknown_type),
---and adds no_frontmatter, which the migration counts; the daemon's own `doc.validate` is the
---authority.
function M.validate(s, text)
  local fm = frontmatter.parse_text(text)
  local diags = {}
  local function add(field, rule, msg) diags[#diags + 1] = { field = field, rule = rule, message = msg } end
  if not fm then
    add("", "no_frontmatter", "the document has no frontmatter")
    return diags
  end
  local tv = fm.keys[s.discriminator] and fm.keys[s.discriminator].value
  local t = type(tv) == "string" and tv or nil
  -- a type the schema has no fields for is checked against common alone
  local fields = schema.fields_for(s, t and s.types[t] and t or nil)
  for _, f in ipairs(fields) do
    local k = fm.keys[f.name]
    local v = k and k.value
    if v == nil then
      if f.required and not (f.name == s.discriminator and t) then add(f.name, "required", f.name .. " is required") end
    else
      local vals = type(v) == "table" and v or { v }
      if f.type ~= "list" and type(v) == "table" then
        add(f.name, "type", f.name .. " must be a " .. f.type .. ", not a list")
      else
        local itype = f.type == "list" and (f.item_type or "string") or f.type
        for _, x in ipairs(vals) do
          if not check_scalar(itype, x) then
            add(f.name, "type", ("%s: %q is not a %s"):format(f.name, x, itype))
          elseif f.enum and not enum_has(f.enum, x) then
            add(f.name, "enum", ("%s: %q is not one of %s"):format(f.name, x, table.concat(f.enum, ", ")))
          elseif f.name == s.discriminator and not s.types[x] then
            -- as core/schema: a type its enum allows that the schema has no section for
            add(f.name, "unknown_type", ("unknown type %q"):format(x))
          end
        end
      end
    end
  end
  return diags
end

return M
