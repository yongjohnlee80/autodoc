-- :mermaid <type> — a fenced Mermaid diagram to start from, under the cursor's line.
local templates = {
  flowchart = [=[
flowchart TD
    A[Start] --> B{Is it working?}
    B -- Yes --> C[Keep going]
    B -- No --> D[Fix it]
    D --> B]=],
  sequence = [=[
sequenceDiagram
    participant A as Alice
    participant B as Bob
    A->>B: Hello Bob
    B-->>A: Hi Alice]=],
  class = [=[
classDiagram
    class Animal {
        +String name
        +speak()
    }
    class Dog
    Animal <|-- Dog]=],
  state = [=[
stateDiagram-v2
    [*] --> Idle
    Idle --> Running: start
    Running --> Idle: stop
    Running --> [*]]=],
  er = [=[
erDiagram
    CUSTOMER ||--o{ ORDER : places
    ORDER ||--|{ LINE_ITEM : contains
    CUSTOMER {
        string name
    }]=],
  gantt = [=[
gantt
    title A plan
    dateFormat YYYY-MM-DD
    section Work
    Design :a1, 2026-01-01, 7d
    Build  :after a1, 14d]=],
  pie = [=[
pie title Share
    "One" : 40
    "Two" : 35
    "Three" : 25]=],
  mindmap = [=[
mindmap
  root((Topic))
    Idea one
      Detail
    Idea two]=],
  timeline = [=[
timeline
    title History
    2024 : First step
    2025 : Second step
    2026 : Today]=],
  journey = [=[
journey
    title A user's day
    section Morning
      Wake up: 3: User
      Coffee: 5: User]=],
}

local names = {}
for name in pairs(templates) do names[#names + 1] = name end
table.sort(names)
local list = table.concat(names, ", ")

autodoc.command{
  name = "mermaid",
  args = "<" .. table.concat(names, "|") .. ">",
  help = "insert a Mermaid diagram to start from",
  complete = names, -- Tab offers the types
  run = function(ed, args)
    local kind = args[1]
    if not kind then
      error("mermaid <type>: " .. list, 0)
    end
    local body = templates[kind]
    if not body then
      error("mermaid: no " .. kind .. " template; there are " .. list, 0)
    end
    local text = "```mermaid\n" .. body .. "\n```"
    local line = ed:cursor()
    if ed:line(line) == "" then
      ed:insert_at(line, 1, text)
    else
      ed:insert_at(line, #ed:line(line) + 1, "\n\n" .. text)
      line = line + 2
    end
    ed:set_cursor(line + 1, 1)
  end,
}
