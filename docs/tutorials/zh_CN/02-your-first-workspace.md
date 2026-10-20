# 你的第一个工作区

**工作区**是 AutoDoc 为其维护索引的一个笔记文件夹，有了索引，它就能搜索这些笔记并跟随其中的链接。文件仍是你磁盘上的普通 Markdown，AutoDoc 从不移动它们。

1. 启动 AutoDoc：`autodoc --ui` 或 `autodoc --gui`。
2. 添加工作区：**转到 › 管理工作区…**（SPC W），然后点 **添加…**：填一个简短的名称，再选文件夹。
3. AutoDoc 在后台为该文件夹建立索引，完成时状态栏会提示。
4. **SPC o** 打开笔记；**Ctrl+N** 新建笔记；**Ctrl+S** 保存。

```mermaid
flowchart LR
  F[你的笔记文件夹] -->|添加| W[一个工作区]
  W -->|建立索引| S[搜索与链接]
  W -->|SPC o| P[页面上的一篇笔记]
```

下次启动时，`autodoc --ui` 会打开你上次用的工作区，`autodoc --ui <name>` 则按名称打开指定的工作区。

更多：[参考手册，第 2 章：快速入门与工作区](../../reference/02-quickstart-and-workspaces.md)。
