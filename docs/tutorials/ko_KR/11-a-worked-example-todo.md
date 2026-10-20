# 실전 예제: :todo

텍스트를 선택하고 명령줄(**:** 또는 **Ctrl+;**)에서 `todo`를 실행하면, 그 텍스트가 워크스페이스의 할 일 목록에 작업으로 등록됩니다. 제목을 입력하지 않으면 첫 줄이 제목이 됩니다.

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

- `error(…, 0)`은 명령 자체의 오류로서 프롬프트에서 거부합니다.
- 첫 번째 매개변수는 어느 할 일 목록인지 정합니다: 워크스페이스 이름, 또는 워크스페이스가 아닌 프로젝트 폴더라면 `{root = "/abs/path"}`.
- 데몬은 **클레임된** 할 일 목록에만 씁니다. 그전까지 콜백의 `err`는 `"-32077 …"` (`todos.unclaimed`)입니다. 무엇이 클레임하는지는 `autodoc --contracts 'todos.*'`에서 확인하세요.

**플러그인을 써야 할 때.** Lua 명령은 페이지를 위한 것입니다: 편집하고, 데몬에 묻고, 한 일을 알리는 것까지 순식간에 끝납니다. 플러그인(**플러그인** 메뉴, `docs/plugins.md`)은 독립된 프로그램입니다: 계속 실행되고, 자체 대화상자나 카드를 그리며, 열린 노트를 따라가고, 파일, 네트워크, 다른 도구에 접근합니다. 플러그인의 명령은 플러그인 메뉴와 **SPC p**에 나타납니다. `os`나 `io`가 필요하거나 2초보다 오래 걸리는 스크립트라면 플러그인으로 만드는 것이 맞습니다.

더 보기: [레퍼런스 10장: 내장 할 일 작업 엔진](../../reference/10-todo-task-engine.md), [레퍼런스 14장: Lua 스크립팅과 사용자 명령](../../reference/14-lua-scripting-and-custom-commands.md).
