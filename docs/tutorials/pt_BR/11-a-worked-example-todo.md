# Um exemplo completo: :todo

Selecione um texto, rode `todo` na linha de comando (**:** ou **Ctrl+;**), e ele vira uma tarefa na lista de tarefas do espaço de trabalho: a primeira linha é o título, a menos que você digite um.

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

- `error(…, 0)` recusa no prompt, como um erro do próprio comando.
- O primeiro parâmetro diz qual lista de tarefas: o nome de um espaço de trabalho, ou `{root = "/abs/path"}` para uma pasta de projeto que não é um espaço de trabalho.
- O daemon só escreve numa lista de tarefas depois que ela é **reivindicada**. Até lá, o `err` do callback é `"-32077 …"` (`todos.unclaimed`); `autodoc --contracts 'todos.*'` diz o que reivindica uma.

**Quando escrever um plugin em vez disso.** Um comando Lua é para a página: ele edita, consulta o daemon e diz o que fez, tudo num instante. Um plugin (o menu **Plugins**; `docs/plugins.md`) é um programa próprio: continua rodando, desenha o próprio diálogo ou cartão, acompanha a nota aberta e acessa arquivos, a rede e outras ferramentas; os comandos dele ficam no menu Plugins e em **SPC p**. Um script que quer `os` ou `io`, ou mais de dois segundos, quer ser um plugin.

Mais: [Referência, Capítulo 10: O motor de tarefas integrado](../../reference/10-todo-task-engine.md) e [Referência, Capítulo 14: Scripts Lua e comandos personalizados](../../reference/14-lua-scripting-and-custom-commands.md).
