# Lua로 나만의 명령 만들기

명령줄은 Lua로 작성한 명령을 실행합니다. Vim의 Normal 모드에서는 **:**로, 어느 모드에서든 **Ctrl+;**로 열며, 터미널이 Ctrl+;를 보내지 못하는 경우에는 **검색 › 명령줄…**을 씁니다. `:table`과 `:mermaid`는 AutoDoc의 기본 명령이고, 나만의 명령은 `config.toml` 옆의 **`~/.config/autodoc/lua/`**에 `*.lua` 파일로 넣습니다.

**불러오는 순서.** 시작할 때 AutoDoc의 스크립트를 먼저 불러오고, 그다음 내 스크립트를 파일 이름 순서대로 불러옵니다. 이미 불러온 명령과 이름이 같은 명령은 기존 명령을 대체합니다. 즉 내 명령이 AutoDoc의 명령을, 나중 파일이 앞선 파일의 명령을 대체할 수 있습니다. 불러오지 못한 파일은 알림으로 표시되고, 나머지 파일은 그대로 불러옵니다. 편집한 파일을 불러오려면 **:lua-reload**를 실행하세요(AutoDoc에서 그 파일을 저장해도 됩니다).

```mermaid
flowchart LR
  A[AutoDoc의 스크립트] --> Y[내 스크립트, 파일 이름 순]
  Y --> C[명령줄의 명령]
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

**`name`**은 입력하는 이름이고, **`aliases`**는 같은 명령의 다른 이름이며, **`args`**와 **`help`**는 Tab을 누를 때 표시되는 내용입니다. **`run`**은 편집기 `ed`, 명령 뒤의 단어들 `args`, 입력한 그대로의 나머지 줄 `line`을 받습니다.

**id로 어떤 명령이든.** `autodoc.run("todos.open")`은 AutoDoc의 명령이나 내 명령(`autodoc.run("lua.upper", "3")`)을 실행하고 `ok, err`를 돌려줍니다. F1을 누르면 COMMANDS 아래에 모든 id가 나열됩니다.

**편집기 `ed`.** 위치는 1부터 셉니다: 1행 1열이 첫 글자입니다. 범위는 시작부터 끝 직전까지이며 끝은 포함하지 않습니다: `ed:text(2, 1, 2, 6)`은 2행의 처음 다섯 글자입니다. `ed:cursor()`와 `ed:selection()`은 현재 위치를 알려 주고, `ed:insert`, `ed:replace`, `ed:delete`는 편집하며, `ed:find(term)`은 검색하고, `ed:run("12")`는 다른 명령을 실행합니다. 명령 하나가 편집한 것은 모두 **실행 취소 한 번**입니다: `u` 한 번으로 전부 되돌립니다.

**샌드박스.** 스크립트는 Lua의 base, table, string, math 라이브러리만 쓸 수 있습니다: `os`도, `io`도, 파일도, `require`도 없습니다. 실패하거나 2초를 넘겨 실행되는 스크립트는 중지되고, 오류는 프롬프트에 표시되며, AutoDoc은 계속 동작합니다.

**전체 튜토리얼**은 `:hello`부터 데몬에 묻는 `:link`까지 단계별로 다루며, AutoDoc 저장소의 `docs/lua/tutorial-vim-commands.md`에 있습니다.

더 보기: [레퍼런스 14장: Lua 스크립팅과 사용자 명령](../../reference/14-lua-scripting-and-custom-commands.md).
