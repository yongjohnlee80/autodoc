# Un ejemplo completo: :todo

Selecciona un texto, ejecuta `todo` en la línea de comandos (**:** o **Ctrl+;**) y se convierte en una tarea de la lista de tareas del espacio de trabajo: su primera línea es el título, salvo que escribas uno.

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

- `error(…, 0)` rechaza en el prompt, como un error propio del comando.
- El primer parámetro indica qué lista de tareas: el nombre de un espacio de trabajo, o `{root = "/abs/path"}` para una carpeta de proyecto que no es un espacio de trabajo.
- El daemon solo escribe en una lista de tareas una vez que está **reclamada**. Hasta entonces, el `err` del callback es `"-32077 …"` (`todos.unclaimed`); `autodoc --contracts 'todos.*'` indica qué la reclama.

**Cuándo escribir un plugin en su lugar.** Un comando Lua es para la página: edita, consulta al daemon y dice lo que hizo, todo en un momento. Un plugin (el menú **Plugins**; `docs/plugins.md`) es un programa propio: sigue ejecutándose, dibuja su propio diálogo o tarjeta, sigue la nota abierta y accede a archivos, a la red y a otras herramientas; sus comandos van en el menú Plugins y en **SPC p**. Un script que necesita `os` o `io`, o más de dos segundos, debería ser un plugin.

Más: [Referencia, capítulo 10: El motor de tareas integrado](../../reference/10-todo-task-engine.md), y [Referencia, capítulo 14: Scripts en Lua y comandos personalizados](../../reference/14-lua-scripting-and-custom-commands.md).
