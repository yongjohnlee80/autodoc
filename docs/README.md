# AutoDoc

AutoDoc is a home for your notes. It keeps a folder of Markdown files as they are, and gives them
an editor, search by words and by meaning, the links between them, and room for AI agents to work
beside you.

- **Your files stay yours.** The notes are plain Markdown on your disk. AutoDoc's index of them
  lives elsewhere, follows every change, and can always be rebuilt from the files.
- **One program, many ways in.** `autodoc --ui` runs in your terminal and `autodoc --gui` opens a
  window of its own; Neovim, plugins and AI agents reach the same notes through `autodoc --call`.
- **An editor for reading and writing.** A page centred on the screen, Vim or ordinary keys,
  diagrams, charts and tables drawn in place, and every note one key away.
- **Search that understands.** By words, or with a model by meaning, ordered again by a reranker.
- **The links between notes.** What a note links to and what links to it, a mind map of its
  neighbours, and the whole workspace as a map.
- **Agents and plugins.** AI agents' command lines in a panel, told about your workspace; plugins,
  such as the AutoDB SQL workbench, that run beside your notes.

## Where to go next

- **New here?** The [tutorials](tutorials/README.md) start with
  [installing AutoDoc](tutorials/01-installing-autodoc.md), a few minutes each.
- **The whole story:** the [Reference Book](reference/README.md) covers every surface and setting.
- **Scripting:** your own commands in [Lua](lua/README.md), and its
  [API reference](lua/reference.md).
- **Plugins:** the [plugin registry](https://github.com/yongjohnlee80/autodoc-plugin-registry), and
  [writing one](plugins.md).
