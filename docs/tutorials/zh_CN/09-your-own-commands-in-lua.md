# 用 Lua 编写你自己的命令

命令行运行用 Lua 编写的命令：在 Vim 的 Normal 模式下按 **:** 打开它，在任何模式下按 **Ctrl+;**，终端无法发送 Ctrl+; 时则用 **搜索 › 命令行…**。`:table` 和 `:mermaid` 是 AutoDoc 自带的命令；你自己的命令放在 **`~/.config/autodoc/lua/`** 中，与 `config.toml` 放在一起，写成 `*.lua` 文件。

**加载顺序。** 启动时先加载 AutoDoc 的脚本，再按文件名顺序加载你的脚本。与已加载命令同名的命令会替换它：你的命令可以替换 AutoDoc 的，靠后的文件也可以替换靠前文件的。加载失败的文件会产生一条通知，其他文件照常加载。运行 **:lua-reload**（或在 AutoDoc 中保存该文件）即可加载修改过的文件。

```mermaid
flowchart LR
  A[AutoDoc 的脚本] --> Y["你的脚本，按文件名顺序"]
  Y --> C[命令行中的命令]
```

```lua
-- :upper [line] — upper-case a line, the cursor's by default
autodoc.command{
  name = "upper", aliases = {"up"}, args = "[line]", help = "upper-case a line",
  run = function(ed, args, line)
    local n = tonumber(args[1]) or ed:cursor()
    ed:replace(n, 1, n, #ed:line(n) + 1, ed:line(n):upper())
    autodoc.notify("upper-cased line " .. n)
  end,
}
```

**`name`** 是你输入的命令名，**`aliases`** 是它的别名，**`args`** 和 **`help`** 是按 Tab 时显示的内容。**`run`** 接收三样东西：编辑器 `ed`；命令后面的各个词 `args`；以及按原样输入的整行其余部分 `line`。

**按 id 运行任意命令。** `autodoc.run("todos.open")` 运行 AutoDoc 的或你自己的命令（`autodoc.run("lua.upper", "3")`），并返回 `ok, err`；F1 在“命令”下列出所有 id。

**编辑器 `ed`。** 位置从 1 开始计数：第 1 行第 1 列就是第一个字符。范围从起点开始，到终点为止，但不包含终点：`ed:text(2, 1, 2, 6)` 是第 2 行的前五个字符。`ed:cursor()` 和 `ed:selection()` 告诉你当前所在位置；`ed:insert`、`ed:replace` 和 `ed:delete` 用于编辑；`ed:find(term)` 用于搜索；`ed:run("12")` 运行另一条命令。一条命令所做的全部编辑算作**一次撤销**：按 `u` 会全部撤回。

**沙盒。** 脚本可以使用 Lua 的 base、table、string 和 math 库：没有 `os`，没有 `io`，不能访问文件，也没有 `require`。脚本失败或运行超过两秒就会被停止；错误显示在提示符中，AutoDoc 照常运行。

**完整教程**从 `:hello` 一步步讲到向守护进程查询的 `:link`，位于 AutoDoc 仓库中的 `docs/lua/tutorial-vim-commands.md`。

更多：[参考手册，第 14 章：Lua 脚本与自定义命令](../../reference/14-lua-scripting-and-custom-commands.md)。
