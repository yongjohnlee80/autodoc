# 安装 AutoDoc

AutoDoc 只有一个程序：`autodoc`。选一种适合你机器的安装方式：

| 系统 | 安装方式 |
| --- | --- |
| Linux、macOS（需要 Go） | 窗口版：`GOBIN=~/.local/bin go install -tags gui github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` |
| 任意系统（需要 Go） | 终端版：`go install github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` |
| macOS、Linux | mise：`mise use -g github:yongjohnlee80/autodoc`（窗口版） |
| Linux（服务器或容器） | 用 mise 装终端版：`mise use -g 'github:yongjohnlee80/autodoc[asset_pattern=autodoc-*-linux-amd64-tui.tar.gz]'`（arm64 上用 `linux-arm64-tui`） |
| macOS、Linux | Homebrew：`brew install yongjohnlee80/tap/autodoc`（在 Linux 上是终端版） |
| Linux、macOS | 从项目的 Releases 页面下载发布包，`amd64` 或 `arm64` |
| Windows | 先装 WSL2，再按任一 Linux 方式安装 |

**两种运行方式。** `autodoc --ui` 在终端中运行。`autodoc --gui` 打开一个独立窗口，能绘制图表，也能使用你选的字体。`--gui` 是运行时的参数，`-tags gui` 是让 Go 把窗口版编译进去的构建标签。在 Linux 上，mise 和不带后缀的发布包是窗口版，即使用 `--ui` 运行也需要桌面的图形库（EGL、Wayland、X11）；在服务器或容器中，请用 `-tui` 发布包。Linux 上的 Homebrew 只安装终端版。

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
