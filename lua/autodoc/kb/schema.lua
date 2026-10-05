-- autodoc.kb.schema: the KB's shipped frontmatter schema (templates/_schema/frontmatter.yaml).
--
-- That YAML file is the single source: the daemon validates against it (core/schema), the
-- `_templates/<type>.md` files are generated from it here, and the migration counts schema
-- diagnostics with validate() below. The parser reads only the shape the shipped file is written
-- in (top-level keys, `common:` and `types:` with one flow mapping per field); it is not a general
-- YAML reader and refuses anything else.

local util = require("autodoc.kb.util")
local frontmatter = require("autodoc.kb.frontmatter")

local M = {}

local here = debug.getinfo(1, "S").source:sub(2):match("^(.*)/[^/]+$")
M.templates_dir = here .. "/templates"
M.schema_path = M.templates_dir .. "/_schema/frontmatter.yaml"

-- split "a, [b, c], d" at top-level commas
local function split_top(s)
  local out, depth, cur = {}, 0, {}
  for i = 1, #s do
    local c = s:sub(i, i)
    if c == "[" or c == "{" then depth = depth + 1 end
    if c == "]" or c == "}" then depth = depth - 1 end
    if c == "," and depth == 0 then
      out[#out + 1] = vim.trim(table.concat(cur))
      cur = {}
    else
      cur[#cur + 1] = c
    end
  end
  local last = vim.trim(table.concat(cur))
  if last ~= "" then out[#out + 1] = last end
  return out
end

local function flow_map(s, where)
  s = vim.trim(s)
  if s:sub(1, 1) ~= "{" or s:sub(-1) ~= "}" then error(where .. ": expected a {flow mapping}") end
  local f = {}
  for _, kv in ipairs(split_top(s:sub(2, -2))) do
    local k, v = kv:match("^([%w_]+)%s*:%s*(.-)$")
    if not k then error(where .. ": bad entry " .. kv) end
    if v:sub(1, 1) == "[" then
      local items = {}
      for _, it in ipairs(split_top(v:sub(2, -2))) do items[#items + 1] = it end
      f[k] = items
    elseif v == "true" then
      f[k] = true
    elseif v == "false" then
      f[k] = false
    else
      f[k] = v
    end
  end
  return f
end

---Parse the shipped schema's text.
---@return table schema {version, strict, discriminator, common = {fields}, common_by = {}, types = {name -> {fields}}, type_order = {}}
function M.parse(text)
  local s = { common = {}, common_by = {}, types = {}, type_order = {} }
  local section, cur_type
  for n, ln in ipairs(vim.split(text, "\n", { plain = true })) do
    local where = "schema line " .. n
    if ln:match("^%s*#") or ln:match("^%s*$") then
      -- comment or blank
    elseif ln:match("^%S") then
      local k, v = ln:match("^([%w_]+):%s*(.-)%s*$")
      if not k then error(where .. ": bad top-level line") end
      section, cur_type = nil, nil
      if k == "version" then
        s.version = tonumber(v)
      elseif k == "strict" then
        s.strict = v == "true"
      elseif k == "discriminator" then
        s.discriminator = v
      elseif k == "common" or k == "types" then
        section = k
      else
        error(where .. ": unknown key " .. k)
      end
    elseif section == "common" then
      local name, decl = ln:match("^  ([%w_%-]+):%s*(.+)$")
      if not name then error(where .. ": bad common field") end
      local f = flow_map(decl, where)
      f.name = name
      s.common[#s.common + 1] = f
      s.common_by[name] = f
    elseif section == "types" then
      local tname = ln:match("^  ([%w_%-]+):%s*$")
      if tname then
        cur_type = { name = tname, fields = {}, by = {} }
        s.types[tname] = cur_type
        s.type_order[#s.type_order + 1] = tname
      else
        local name, decl = ln:match("^    ([%w_%-]+):%s*(.+)$")
        if not name or not cur_type then error(where .. ": bad type field") end
        local f = flow_map(decl, where)
        f.name = name
        cur_type.fields[#cur_type.fields + 1] = f
        cur_type.by[name] = f
      end
    else
      error(where .. ": unexpected indentation")
    end
  end
  if s.version ~= 2 or not s.discriminator then error("schema: expected version 2 with a discriminator") end
  return s
end

---The shipped schema, parsed (cached).
local cached
function M.shipped()
  if not cached then
    local text = assert(util.read_file(M.schema_path))
    cached = M.parse(text)
  end
  return cached
end

---Fields a document of type `t` is checked against: common, with the type's own over it.
function M.fields_for(s, t)
  local out, by = {}, {}
  local own = s.types[t]
  for _, f in ipairs(s.common) do
    local merged = vim.deepcopy(f)
    if own and own.by[f.name] then
      for k, v in pairs(own.by[f.name]) do merged[k] = v end
      merged.required = f.required or own.by[f.name].required
    end
    out[#out + 1] = merged
    by[f.name] = merged
  end
  if own then
    for _, f in ipairs(own.fields) do
      if not by[f.name] then
        out[#out + 1] = f
        by[f.name] = f
      end
    end
  end
  return out, by
end

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
---core/schema's rules at the level the migration reports them (required, enum, type, unknown_type);
---the daemon's own `doc.validate` is the authority.
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
  local fields
  if t and s.types[t] then
    fields = M.fields_for(s, t)
  else
    fields = M.fields_for(s, nil)
    if t then add(s.discriminator, "unknown_type", ("unknown type %q"):format(t)) end
  end
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
          elseif f.enum and not enum_has(f.enum, x) and not (f.name == s.discriminator and not s.types[x]) then
            add(f.name, "enum", ("%s: %q is not one of %s"):format(f.name, x, table.concat(f.enum, ", ")))
          end
        end
      end
    end
  end
  return diags
end

-- ─── templates ──────────────────────────────────────────────────────

local function placeholder(f)
  if f.enum then return f.enum[1] end
  if f.type == "date" then return "{{date}}" end
  if f.type == "list" then return "[]" end
  if f.type == "integer" or f.type == "number" then return "0" end
  if f.type == "boolean" then return "false" end
  if f.name == "abstract" then return '"One to three sentences: what this document is and why it matters."' end
  return '""'
end

---The `_templates/<type>.md` text for type `t`: every required field with a valid starting value,
---and the type's optional fields as comments that name their values. `{{date}}` and `{{title}}`
---are Obsidian template variables.
function M.template(s, t)
  local fields = M.fields_for(s, t)
  local lines = { "---" }
  for _, f in ipairs(fields) do
    if f.name == s.discriminator then
      lines[#lines + 1] = f.name .. ": " .. t
    elseif f.required then
      lines[#lines + 1] = f.name .. ": " .. placeholder(f)
    end
  end
  lines[#lines + 1] = "title: \"{{title}}\""
  for _, f in ipairs(fields) do
    if not f.required and f.name ~= "title" and f.name ~= s.discriminator then
      local hint = f.type
      if f.enum then hint = table.concat(f.enum, " | ") end
      if f.type == "list" then hint = "list" end
      lines[#lines + 1] = "# " .. f.name .. ": " .. hint
    end
  end
  lines[#lines + 1] = "---"
  lines[#lines + 1] = ""
  lines[#lines + 1] = "# {{title}}"
  lines[#lines + 1] = ""
  return table.concat(lines, "\n")
end

return M
