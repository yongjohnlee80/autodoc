# A worked example: :todo

Select some text, run `todo` on the command line (**:** or **Ctrl+;**), and it is a task in the workspace's todo list: its first line the title, unless you type one.

```lua
-- :todo [title] — the selection, filed as a task in the workspace's todo list
autodoc.command{
  name = "todo", args = "[title]", help = "file the selection as a task",
  run = function(ed, args, line)
    local l1, c1, l2, c2 = ed:selection()
    if not l1 then error("select the task's text first", 0) end
    local text = ed:text(l1, c1, l2, c2)
    local title = line ~= "" and line or text:match("[^\n]+")
    local workspace = autodoc.current_workspace()
    if not workspace then error("enter a workspace first", 0) end
    autodoc.todos.add(workspace, {title = title, description = text}, function(task, err)
      if err then autodoc.notify("todo: " .. err) return end
      autodoc.notify("filed " .. task.id)
    end)
  end,
}
```

- `error(…, 0)` refuses in the prompt, as a command's own error.
- The first param says which todo list: a workspace's name, or `{root = "/abs/path"}` for a project folder that is no workspace.
- The daemon writes a todo list only once it is **claimed**. Until then the callback's `err` is `"-32077 …"` (`todos.unclaimed`); `autodoc --contracts 'todos.*'` says what claims one.

**When to write a plugin instead.** A Lua command is for the page: it edits, asks the daemon and says what it did, all in a moment. A plugin (the **Plugins** menu; `docs/plugins.md`) is a program of its own: it keeps running, draws its own dialog or card, follows the open note, and reaches files, the network and other tools; its commands go on the Plugins menu and **SPC p**. A script that wants `os` or `io`, or more than two seconds, wants to be a plugin.
