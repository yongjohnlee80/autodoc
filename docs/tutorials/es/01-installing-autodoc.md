# Instalar AutoDoc

AutoDoc es un solo programa, `autodoc`. Elige la forma que mejor se adapte a tu equipo:

| En | Instalar con |
| --- | --- |
| Linux, macOS, con Go | la ventana: `GOBIN=~/.local/bin go install -tags gui github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` |
| Cualquiera, con Go | la interfaz de terminal: `go install github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` |
| macOS, Linux | mise: `mise use -g github:yongjohnlee80/autodoc` (con la ventana) |
| Linux, un servidor o contenedor | mise, la interfaz de terminal: `mise use -g 'github:yongjohnlee80/autodoc[asset_pattern=autodoc-*-linux-amd64-tui.tar.gz]'` (en arm64, `linux-arm64-tui`) |
| macOS, Linux | Homebrew: `brew install yongjohnlee80/tap/autodoc` (en Linux, la interfaz de terminal) |
| Linux, macOS | un archivo comprimido de la página Releases del proyecto, `amd64` o `arm64` |
| Windows | WSL2, y luego cualquier método de Linux |

**Dos formas de ejecutarlo.** `autodoc --ui` se ejecuta en tu terminal. `autodoc --gui` abre una ventana propia, con los diagramas dibujados y las fuentes que elijas. `--gui` es cómo lo ejecutas; `-tags gui` es cómo Go incluye la ventana al compilar. En Linux, mise y el archivo comprimido sin sufijo traen la ventana y necesitan las bibliotecas gráficas de un escritorio (EGL, Wayland, X11), incluso para `--ui`: en un servidor o un contenedor, usa el archivo `-tui`. Homebrew en Linux instala solo la interfaz de terminal.

```mermaid
flowchart LR
  I[Instalar autodoc] --> U[autodoc --ui en el terminal]
  I --> G[autodoc --gui en una ventana]
  U --> D[(el daemon)]
  G --> D
```

**El daemon se inicia solo.** Ambos hablan con un único programa en segundo plano, el daemon, que mantiene el índice de tus archivos. El primer `autodoc --ui` o `--gui` lo inicia; los siguientes lo comparten. Nunca tienes que iniciarlo a mano.

`autodoc --version` indica qué versión tienes.

Más: [Referencia, capítulo 2: Inicio rápido y espacios de trabajo](../../reference/02-quickstart-and-workspaces.md).
