-- :usage — what AutoDoc uses: a card (autodoc.ui.card) of the daemon's and the app's memory and
-- CPU, and the workspace's index, refreshed each second while the card is open (ADR 1792256101
-- §4.6). In a window that draws charts (autodoc.ui.charts()) two vega-lite line charts follow the
-- tables: CPU and memory over the last minute; a terminal shows the tables alone.

local keep = 60 -- samples kept per process: a minute at one a second

-- bytes as a person reads them
local function size(n)
  if not n or n <= 0 then return "—" end
  local units = { "B", "KiB", "MiB", "GiB", "TiB" }
  local i = 1
  while n >= 1024 and i < #units do n, i = n / 1024, i + 1 end
  if i == 1 then return ("%d %s"):format(math.floor(n), units[i]) end
  return ("%.1f %s"):format(n, units[i])
end

local function duration(s)
  s = math.floor(s or 0)
  if s < 60 then return s .. " s" end
  if s < 3600 then return ("%d min %d s"):format(math.floor(s / 60), s % 60) end
  return ("%d h %d min"):format(math.floor(s / 3600), math.floor((s % 3600) / 60))
end

-- sample adds one reading of a process to its history and gives the reading, with cpu its share
-- since the last one: nil after a restart (another pid or start), a step of no time, or a counter
-- that went back.
local function sample(hist, st)
  local last = hist[#hist]
  if last and (last.pid ~= st.pid or last.started_at ~= st.started_at) then
    for i = #hist, 1, -1 do hist[i] = nil end
    last = nil
  end
  local t = autodoc.now()
  local cpu
  if last then
    local dt, dc = t - last.t, st.cpu_seconds - last.cpu_seconds
    if dt > 0 and dc >= 0 then cpu = dc / dt * 100 end
  end
  local mem = st.rss > 0 and st.rss or st.peak_rss
  hist[#hist + 1] = { t = t, pid = st.pid, started_at = st.started_at, cpu_seconds = st.cpu_seconds, cpu = cpu, mem = mem }
  while #hist > keep do table.remove(hist, 1) end
  return { st = st, cpu = cpu }
end

local function percent(v) return v and ("%.1f %%"):format(v) or "—" end

-- width is how many characters s shows: UTF-8 continuation bytes are not characters
local function width(s) return select(2, tostring(s):gsub("[^\128-\191]", "")) end

local function pad(s, w)
  s = tostring(s)
  return s .. string.rep(" ", w - width(s))
end

-- kv_table is rows of {label, value} as a Markdown table padded to its columns: the GUI renders
-- it, and the terminal, which shows the Markdown as it is, reads it as aligned columns
local function kv_table(rows)
  local kw, vw = 3, 3
  for _, r in ipairs(rows) do
    kw, vw = math.max(kw, width(r[1])), math.max(vw, width(r[2]))
  end
  local out = {
    "| " .. pad("", kw) .. " | " .. pad("", vw) .. " |",
    "| " .. string.rep("-", kw) .. " | " .. string.rep("-", vw) .. " |",
  }
  for _, r in ipairs(rows) do
    out[#out + 1] = "| " .. pad(r[1], kw) .. " | " .. pad(r[2], vw) .. " |"
  end
  return table.concat(out, "\n")
end

local function process_table(name, r)
  local st = r.st
  local rss = st.rss > 0 and size(st.rss) or ("— (peak " .. size(st.peak_rss) .. ")")
  return "### " .. name .. "\n\n" .. kv_table({
    { "CPU", percent(r.cpu) .. " of one core, " .. st.cpus .. " cores" },
    { "resident memory", rss },
    { "peak resident", size(st.peak_rss) },
    { "Go memory", size(st.go_memory) .. " (heap " .. size(st.go_heap) .. ")" },
    { "goroutines", st.goroutines },
    { "up", duration(st.uptime) },
    { "pid, version", st.pid .. ", " .. tostring(st.version) },
  })
end

-- model is an embedding model's id as a person reads it: provider|name|digest|dims becomes
-- "provider name, dims dims", the digest left out; any other shape as it is
local function model(id)
  id = tostring(id)
  local parts, from = {}, 1
  while true do -- split on "|" by find, not gmatch: gopher-lua's gmatch fails on this string
    local at = id:find("|", from, true)
    if not at then
      parts[#parts + 1] = id:sub(from)
      break
    end
    parts[#parts + 1] = id:sub(from, at - 1)
    from = at + 1
  end
  if #parts == 4 then return parts[1] .. " " .. parts[2] .. ", " .. parts[4] .. " dims" end
  return id
end

local function workspace_table(ws, status, models)
  if not ws then return "### Workspace\n\nNo workspace is open." end
  if status.error then return "### Workspace `" .. ws .. "`\n\nThe index did not answer: " .. status.error end
  local rows = {
    { "documents", status.docs },
    { "sections", status.chunks },
    { "waiting to index", status.pending_jobs },
    { "failing", #(status.failing or {}) },
    { "held", status.held },
  }
  local e = status.embeddings
  if e then
    rows[#rows + 1] = { "meaning search", tostring(e.semantic) .. " (" .. model(e.model) .. ")" }
  end
  for _, m in ipairs(models or {}) do
    rows[#rows + 1] = { "vectors, " .. m.name, m.vectors .. " (" .. tostring(m.state) .. ", " .. size(m.f32_bytes + m.bits_bytes + m.key_bytes) .. ")" }
  end
  return "### Workspace `" .. ws .. "`\n\n" .. kv_table(rows)
end

-- json writes the values a chart needs: numbers, strings, lists and maps of them
local function json(v)
  local t = type(v)
  if t == "number" then
    if v ~= v or v == math.huge or v == -math.huge then return "null" end
    return ("%.6g"):format(v)
  elseif t == "string" then
    return '"' .. v:gsub('[%c"\\]', function(c) return ("\\u%04x"):format(c:byte()) end) .. '"'
  elseif t == "table" then
    if #v > 0 or next(v) == nil then
      local out = {}
      for i, x in ipairs(v) do out[i] = json(x) end
      return "[" .. table.concat(out, ",") .. "]"
    end
    local keys = {}
    for k in pairs(v) do keys[#keys + 1] = k end
    table.sort(keys)
    local out = {}
    for _, k in ipairs(keys) do out[#out + 1] = json(k) .. ":" .. json(v[k]) end
    return "{" .. table.concat(out, ",") .. "}"
  end
  return "null"
end

-- chart is a vega-lite line chart of field over the histories, one line per process
local function chart(title, field, scale, label, hists)
  local values, t0 = {}, nil
  for _, h in pairs(hists) do
    if h[1] and (not t0 or h[1].t < t0) then t0 = h[1].t end
  end
  for name, h in pairs(hists) do
    for _, s in ipairs(h) do
      if s[field] then
        values[#values + 1] = { seconds = s.t - t0, value = s[field] / scale, process = name }
      end
    end
  end
  return "```vega-lite\n" .. json({
    title = title, mark = "line",
    data = { values = values },
    encoding = {
      x = { field = "seconds", type = "quantitative", title = "seconds" },
      y = { field = "value", type = "quantitative", title = label },
      color = { field = "process", type = "nominal" },
    },
  }) .. "\n```"
end

local function render(rows, ws, status, models, hists)
  local parts = { "## AutoDoc usage", "", process_table("Daemon", rows.daemon), "", process_table("App", rows.app), "",
    workspace_table(ws, status or {}, models) }
  if autodoc.ui.charts() then
    parts[#parts + 1] = ""
    parts[#parts + 1] = chart("CPU", "cpu", 1, "% of one core", hists)
    parts[#parts + 1] = ""
    parts[#parts + 1] = chart("Memory", "mem", 1024 * 1024, "MiB", hists)
  end
  return table.concat(parts, "\n")
end

autodoc.command{
  name = "usage",
  title = "what AutoDoc uses",
  help = "a card of the daemon's and the app's memory and CPU, and the workspace's index",
  run = function()
    local hists = { daemon = {}, app = {} }
    autodoc.ui.card{ title = "AutoDoc usage", text = "reading…", every = 1000, tick = function(card)
      local app = sample(hists.app, autodoc.app.stats())
      if not (autodoc.sys and autodoc.sys.stats) then
        card:set("The daemon is older than protocol 25: it cannot say what it uses.\n\n" .. process_table("App", app))
        return
      end
      autodoc.sys.stats(function(d, err)
        if err then
          card:set("The daemon did not answer: " .. err .. "\n\n" .. process_table("App", app))
          return
        end
        local rows = { daemon = sample(hists.daemon, d), app = app }
        local ws = autodoc.current_workspace()
        if not ws then
          card:set(render(rows, nil, nil, nil, hists))
          return
        end
        autodoc.index.status(ws, function(status, err2)
          if err2 then
            card:set(render(rows, ws, { error = err2 }, nil, hists))
            return
          end
          autodoc.index.models(ws, function(models)
            card:set(render(rows, ws, status, models, hists))
          end)
        end)
      end)
    end }
  end,
}
