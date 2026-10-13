-- agents.lua — the page to one of AutoDoc's agents: :agent-buffer sends the whole page, and
-- :agent-forward the selection, else what the clipboard holds. The text is pasted into the agent's
-- prompt, not submitted: read it there, add to it, press Enter.
--
-- The agent: the one running; with several, the choice of them; with none, the choice of the
-- profiles, the one chosen started once you agree.

local function label(a)
  local mark = a.running and "  ▶" or ""
  return a.name .. "  (" .. a.codename .. ")" .. mark
end

-- choose calls with(ref) with the agent to send to, asking when it must.
local function choose(with)
  local all, running = autodoc.agents.list(), {}
  for _, a in ipairs(all) do
    if a.running then
      running[#running + 1] = a
    end
  end
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

-- send pastes text into the agent chosen, and says how it went.
local function send(text, what)
  choose(function(ref, name)
    autodoc.agents.send(ref, text, function(ok, err)
      if ok then
        autodoc.notify("sent " .. what .. " to " .. name .. ": press Enter there to submit it")
      elseif err ~= "cancelled" then
        autodoc.notify("not sent to " .. name .. ": " .. err)
      end
    end)
  end)
end

autodoc.command({
  name = "agent-buffer",
  title = "send the page to an agent",
  help = "paste the whole page into an agent's prompt (SPC a b)",
  run = function(ed)
    local text = ed:value()
    if text == "" then
      autodoc.notify("the page is empty: nothing to send")
      return
    end
    send(text, "the page")
  end,
})

autodoc.command({
  name = "agent-forward",
  title = "send the selection or the clipboard to an agent",
  help = "paste the selection, else the clipboard, into an agent's prompt (SPC a f)",
  run = function(ed)
    local l1, c1, l2, c2 = ed:selection()
    if l1 then
      local text = ed:text(l1, c1, l2, c2)
      if text ~= "" then
        return send(text, "the selection")
      end
    end
    autodoc.clipboard.read(function(text, err)
      if err == "unsupported" then
        autodoc.notify("select the text to send: this terminal doesn't let AutoDoc read its clipboard")
      elseif err then
        autodoc.notify("the clipboard could not be read (" .. err .. "): select the text to send")
      elseif text == "" then
        autodoc.notify("nothing to send: select some text, or copy some")
      else
        send(text, "the clipboard")
      end
    end)
  end,
})
