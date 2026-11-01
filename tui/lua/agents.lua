-- agents.lua — the page to one of AutoDoc's agents, with your instruction: :agent-buffer (SPC a b)
-- hands it the page, and :agent-forward (SPC a f) the selection, else what the clipboard holds.
-- Either asks what the agent should do with it, then sends it a request from you, which it
-- answers: the subject names what is handed over, and the body says which command sent it, your
-- instruction, and where the text is. A saved page goes as its workspace, path and version, never
-- its text: the agent reads it there. The text itself goes only where it can't be read (a page with
-- unsaved changes, the untitled draft, a selection, the clipboard), and at most LIMIT of it.
--
-- The agent: the one running; with several, the choice of them; with none, the choice of the
-- profiles, the one chosen started once you agree.

-- LIMIT is the most text a request carries: about 2,000 tokens.
local LIMIT = 8 * 1024

local function label(a)
  local mark = a.running and "  ▶" or ""
  return a.name .. "  (" .. a.codename .. ")" .. mark
end

-- choose calls with(ref) with the agent to send to, asking when it must.
local function choose(with)
  local all, running = autodoc.agents.list(), {}
  local open = {}
  for _, a in ipairs(all) do
    if not a.closing then -- a closing agent refuses a send: never offered
      open[#open + 1] = a
      if a.running then
        running[#running + 1] = a
      end
    end
  end
  all = open
  if #running == 1 then
    return with({ codename = running[1].codename, instance = running[1].instance }, running[1].name)
  end
  local list = #running > 1 and running or all
  if #list == 0 then
    autodoc.notify("no agent to send to: System › Agent profiles… adds one")
    return
  end
  local items = {}
  for _, a in ipairs(list) do
    local detail = a.running and "running" or "stopped: you'll be asked to start it"
    items[#items + 1] = { label = label(a), detail = detail, value = a }
  end
  local title = #running > 1 and "send to which agent?" or "start which agent and send?"
  autodoc.ui.pick({ title = title, items = items }, function(a, err)
    if err then
      if err ~= "cancelled" then
        autodoc.notify("no agent chosen: " .. err)
      end
      return
    end
    local ref = { codename = a.codename }
    if a.running then
      ref.instance = a.instance
    end
    with(ref, a.name)
  end)
end

-- hand asks your instruction for the agent chosen, then sends it request(instruction): a subject
-- and a body. An empty instruction, or Esc, sends nothing.
local function hand(what, request)
  choose(function(ref, name)
    autodoc.ui.input({ title = "hand " .. what .. " to " .. name, label = "What should " .. name .. " do with it?" },
      function(text, err)
        if err then
          if err ~= "cancelled" then
            autodoc.notify("not sent: " .. err)
          end
          return
        end
        local instruction = (text:gsub("^%s+", ""):gsub("%s+$", ""))
        if instruction == "" then
          autodoc.notify("not sent: say what " .. name .. " should do with " .. what)
          return
        end
        local subject, body = request(instruction)
        autodoc.agents.send(ref, { subject = subject, body = body }, function(ok, serr)
          if ok then
            autodoc.notify("sent " .. what .. " to " .. name)
          elseif serr ~= "cancelled" then
            autodoc.notify("not sent to " .. name .. ": " .. serr)
          end
        end)
      end)
  end)
end

-- place is the page's place: "<path> in <workspace>", or the untitled draft.
local function place(doc)
  if doc.path and doc.workspace then
    return doc.path .. " in " .. doc.workspace
  end
  return doc.path or "the untitled draft"
end

-- body is a request's body: the command that sent it, the instruction, then lines.
local function body(from, instruction, lines)
  local out = { "From: " .. from .. ", sent by the user", "Instruction: " .. instruction }
  for _, l in ipairs(lines) do
    out[#out + 1] = l
  end
  return table.concat(out, "\n")
end

-- tooLong says text is over LIMIT, and what to do instead; it answers whether it was.
local function tooLong(text, what, instead)
  if #text <= LIMIT then
    return false
  end
  autodoc.notify(what .. " is over " .. math.floor(LIMIT / 1024) .. " KiB, too much to send as text: " .. instead)
  return true
end

local FROM_PAGE = "SPC a b (:agent-buffer)"
local FROM_FORWARD = "SPC a f (:agent-forward)"

autodoc.command({
  name = "agent-buffer",
  title = "hand the page to an agent",
  help = "hand the page to an agent with your instruction: a saved page by its path (SPC a b)",
  run = function(ed)
    local text = ed:value()
    if text == "" then
      autodoc.notify("the page is empty: nothing to send")
      return
    end
    local doc = autodoc.document()
    if doc.path and doc.workspace and not ed:modified() then
      -- saved: its place and version, read by the agent there, never its text
      autodoc.call("doc.read", { doc.workspace, doc.path }, function(res, err)
        if err then
          autodoc.notify("not sent: the page's version could not be read (" .. tostring(err) .. ")")
          return
        end
        hand("the page", function(instruction)
          return "Page: " .. place(doc), body(FROM_PAGE, instruction, {
            "Workspace: " .. doc.workspace,
            "File: " .. doc.path,
            "Version: " .. tostring(res.version),
            "Its text is not in this message: read the file in the workspace.",
          })
        end)
      end)
      return
    end
    local unsaved = doc.path and "unsaved changes" or "the untitled draft"
    if tooLong(text, "the page (" .. unsaved .. ")", "save it to hand it over by its path, or select the part to send (SPC a f)") then
      return
    end
    hand("the page", function(instruction)
      local lines = {}
      if doc.workspace then
        lines[#lines + 1] = "Workspace: " .. doc.workspace
      end
      lines[#lines + 1] = "File: " .. (doc.path and (doc.path .. " (with unsaved changes: the text below is the page now)") or "the untitled draft (never saved)")
      lines[#lines + 1] = "Text:"
      lines[#lines + 1] = text
      return "Page: " .. place(doc) .. " (unsaved)", body(FROM_PAGE, instruction, lines)
    end)
  end,
})

autodoc.command({
  name = "agent-forward",
  title = "hand the selection or the clipboard to an agent",
  help = "hand the selection, else the clipboard, to an agent with your instruction (SPC a f)",
  run = function(ed)
    local l1, c1, l2, c2 = ed:selection()
    if l1 then
      local text = ed:text(l1, c1, l2, c2)
      if text ~= "" then
        if tooLong(text, "the selection", "select less, or hand the saved page over by its path (SPC a b)") then
          return
        end
        local doc = autodoc.document()
        return hand("the selection", function(instruction)
          local lines = {}
          if doc.workspace then
            lines[#lines + 1] = "Workspace: " .. doc.workspace
          end
          lines[#lines + 1] = "File: " .. (doc.path or "the untitled draft") .. ", lines " .. l1 .. " to " .. l2
          lines[#lines + 1] = "Text:"
          lines[#lines + 1] = text
          return "Selection: " .. place(doc) .. ", lines " .. l1 .. " to " .. l2, body(FROM_FORWARD, instruction, lines)
        end)
      end
    end
    autodoc.clipboard.read(function(text, err)
      if err == "unsupported" then
        autodoc.notify("select the text to send: this terminal doesn't let AutoDoc read its clipboard")
      elseif err then
        autodoc.notify("the clipboard could not be read (" .. err .. "): select the text to send")
      elseif text == "" then
        autodoc.notify("nothing to send: select some text, or copy some")
      elseif not tooLong(text, "the clipboard", "select the part to send instead") then
        hand("the clipboard", function(instruction)
          return "Clipboard text", body(FROM_FORWARD, instruction, { "Text:", text })
        end)
      end
    end)
  end,
})
