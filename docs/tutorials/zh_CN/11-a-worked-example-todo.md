# 实例讲解：:todo

选中一段文本，在命令行（**:** 或 **Ctrl+;**）中运行 `todo`，它就会成为工作区待办列表中的一个任务：除非你自己输入标题，否则以文本的第一行作为标题。

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

- `error(…, 0)` 在提示符中拒绝执行，显示为命令自身的错误。
- 第一个参数指定使用哪个待办列表：一个工作区的名称；对于不属于任何工作区的项目文件夹，则用 `{root = "/abs/path"}`。
- 待办列表只有在被**认领**之后，守护进程才会写入它。在此之前，回调收到的 `err` 是 `"-32077 …"`（`todos.unclaimed`）；`autodoc --contracts 'todos.*'` 说明了如何认领。

**什么时候该写插件。** Lua 命令是为页面服务的：它编辑、询问守护进程、告诉你做了什么，一瞬间就完成。插件（**插件** 菜单；`docs/plugins.md`）则是一个独立的程序：它持续运行，绘制自己的对话框或卡片，跟随当前打开的笔记，并且能访问文件、网络和其他工具；它的命令出现在插件菜单和 **SPC p** 中。需要 `os` 或 `io`，或者需要运行两秒以上的脚本，就该写成插件。

更多：[参考手册，第 10 章：内置待办任务引擎](../../reference/10-todo-task-engine.md)，以及[参考手册，第 14 章：Lua 脚本与自定义命令](../../reference/14-lua-scripting-and-custom-commands.md)。
