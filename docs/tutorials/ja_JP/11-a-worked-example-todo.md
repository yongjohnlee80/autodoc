# 実例: :todo

テキストを選択してコマンドライン (**:** または **Ctrl+;**) で `todo` を実行すると、そのテキストがワークスペースの todo リストのタスクになります。タイトルを入力しなければ、最初の行がタイトルになります。

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

- `error(…, 0)` は、コマンド自身のエラーとしてプロンプトに表示し、処理を中止します。
- 最初のパラメーターで、どの todo リストかを指定します: ワークスペースの名前か、ワークスペースではないプロジェクトフォルダーなら `{root = "/abs/path"}` です。
- デーモンが todo リストに書き込むのは、そのリストが **claim (確保)** された後だけです。それまでは、コールバックの `err` が `"-32077 …"` (`todos.unclaimed`) になります。何がリストを claim するかは `autodoc --contracts 'todos.*'` で確認できます。

**プラグインにすべきとき。** Lua コマンドはページのためのものです: 編集し、デーモンに問い合わせ、結果を伝える、そのすべてを一瞬で行います。プラグイン (**プラグイン** メニュー、`docs/plugins.md`) は独立したプログラムです: 動き続け、独自のダイアログやカードを描画し、開いているノートに追従し、ファイルやネットワーク、ほかのツールにアクセスできます。プラグインのコマンドはプラグインメニューと **SPC p** に並びます。`os` や `io` が必要なスクリプトや、2 秒以上かかるスクリプトは、プラグインにすべきです。

詳しくは: [リファレンス 第 10 章: 組み込みの Todo タスクエンジン](../../reference/10-todo-task-engine.md)、[リファレンス 第 14 章: Lua スクリプトとカスタムコマンド](../../reference/14-lua-scripting-and-custom-commands.md)。
