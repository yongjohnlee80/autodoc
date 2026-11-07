# 安装 AutoDoc

AutoDoc 是一个程序：`autodoc`。

## 安装脚本（推荐）

在 Linux 和 macOS 上，一行命令即可安装带窗口的 AutoDoc：

```sh
curl -fsSL https://github.com/yongjohnlee80/autodoc/releases/latest/download/install-autodoc.sh | sh
```

这个脚本会：

- 下载适合你系统的最新版本，并用该版本的校验和进行校验；
- 安装为 `~/.local/bin/autodoc`，只为你自己安装，从不需要 `sudo`；
- 添加启动器，让 AutoDoc 出现在你的应用程序中（macOS 上是 AutoDoc.app，Linux 上是桌面条目）；
- 在旁边安装 `update.sh`，用来让 AutoDoc 保持最新。

如果 `~/.local/bin` 不在你的 `PATH` 中，脚本会打印需要加到 shell 启动文件里的那一行；它从不自己修改这个文件。如果没有适合你系统的预编译版本，它会先询问，再从该版本的源码构建。

随时可以重新运行：它会升级或修复安装，不会重复添加启动器。`sh install-autodoc.sh --help` 会列出选项，例如安装指定版本的 `--version` 和 `--no-launcher`。

**更新。** 用脚本安装的 AutoDoc 可以自行更新：打开 **帮助 › 关于**，然后点 **更新**。更新在 AutoDoc 的终端面板中运行，新版本就绪后，AutoDoc 会提供 **立即重启**。在 shell 中运行 `~/.local/state/autodoc/update.sh` 效果相同。

## 其他方式

- Homebrew：`brew install yongjohnlee80/tap/autodoc`（在 Linux 上只有终端界面）
- mise：`mise use -g github:yongjohnlee80/autodoc`
- Go：`go install -tags gui github.com/yongjohnlee80/autodoc/cmd/autodoc@latest`（不带 `-tags gui` 时只有终端界面）
- 项目 Releases 页面上的发布包
- Windows：先安装 WSL2，再用安装脚本

这些方式按安装时的方式更新（`brew upgrade`、`mise upgrade`、再次 `go install`），所以 **帮助 › 关于** 不会为它们提供更新。服务器、容器以及从源码构建：见 [参考手册，第 2 章](../../reference/02-quickstart-and-workspaces.md)。

**两种运行方式。** `autodoc --ui` 在终端中运行。`autodoc --gui` 打开一个独立窗口，能绘制图表，也能使用你选的字体。

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
