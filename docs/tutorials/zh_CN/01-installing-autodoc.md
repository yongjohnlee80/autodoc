# 安装 AutoDoc

AutoDoc 只有一个程序：`autodoc`。选一种适合你机器的安装方式：

| 系统 | 安装方式 |
| --- | --- |
| macOS、Linux | Homebrew：`brew install yongjohnlee80/tap/autodoc` |
| Linux、macOS | mise：`mise use -g github:yongjohnlee80/autodoc` |
| Linux、macOS | 从项目的 Releases 页面下载发布包，`amd64` 或 `arm64` |
| 任意系统（需要 Go） | `go install github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` |
| Windows | 先装 WSL2，再按任一 Linux 方式安装 |

**两种运行方式。** `autodoc --ui` 在终端中运行。`autodoc --gui` 打开一个独立窗口，能绘制图表，也能使用你选的字体。在 Linux 上，窗口版需要 `-gui` 发布包，或用 `go install -tags gui …` 安装。

```mermaid
flowchart LR
  I[安装 autodoc] --> U["在终端中运行 autodoc --ui"]
  I --> G["在窗口中运行 autodoc --gui"]
  U --> D[(守护进程)]
  G --> D
```

**守护进程会自行启动。** 两者都与同一个后台程序通信，即守护进程，它负责维护你文件的索引。第一次运行 `autodoc --ui` 或 `--gui` 时会启动它，之后的实例共用它。你永远不必手动启动它。

`autodoc --version` 会显示你安装的版本。

更多：[参考手册，第 2 章：快速入门与工作区](../../reference/02-quickstart-and-workspaces.md)。
