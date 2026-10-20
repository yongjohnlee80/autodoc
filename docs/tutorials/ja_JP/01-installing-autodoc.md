# AutoDoc のインストール

AutoDoc は `autodoc` というひとつのプログラムです。お使いのマシンに合った方法を選んでください。

| 対象 | インストール方法 |
| --- | --- |
| macOS、Linux | Homebrew: `brew install yongjohnlee80/tap/autodoc` |
| Linux、macOS | mise: `mise use -g github:yongjohnlee80/autodoc` |
| Linux、macOS | プロジェクトの Releases ページにあるリリースアーカイブ (`amd64` または `arm64`) |
| Go があればどこでも | `go install github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` |
| Windows | WSL2 を入れてから、Linux のいずれかの方法で |

**実行方法は 2 つあります。** `autodoc --ui` はターミナルの中で動きます。`autodoc --gui` は専用のウィンドウを開き、図を描画し、お好みのフォントで表示します。Linux でウィンドウ版を使うには、`-gui` 付きのリリースアーカイブか、`go install -tags gui …` が必要です。

```mermaid
flowchart LR
  I[autodoc をインストール] --> U["autodoc --ui: ターミナルで"]
  I --> G["autodoc --gui: ウィンドウで"]
  U --> D[(デーモン)]
  G --> D
```

**デーモンは自動で起動します。** どちらも、ファイルのインデックスを保持するバックグラウンドプログラム「デーモン」とやり取りします。最初に実行した `autodoc --ui` または `--gui` がデーモンを起動し、以降はそれを共有します。手動で起動する必要はありません。

`autodoc --version` で、お使いのバージョンを確認できます。

詳しくは: [リファレンス 第 2 章: クイックスタートとワークスペース](../../reference/02-quickstart-and-workspaces.md)。
