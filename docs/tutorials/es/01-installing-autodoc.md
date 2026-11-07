# Instalar AutoDoc

AutoDoc es un solo programa, `autodoc`.

## El script de instalación (recomendado)

En Linux y macOS, una sola línea instala AutoDoc con su ventana:

```sh
curl -fsSL https://github.com/yongjohnlee80/autodoc/releases/latest/download/install-autodoc.sh | sh
```

El script:

- descarga la versión más reciente para tu sistema y la comprueba con el checksum de la versión;
- la instala como `~/.local/bin/autodoc`, solo para ti: nunca pide `sudo`;
- añade un lanzador, para que AutoDoc esté entre tus aplicaciones (AutoDoc.app en macOS, una entrada de escritorio en Linux);
- instala `update.sh` a su lado, que mantiene AutoDoc al día.

Si `~/.local/bin` no está en tu `PATH`, el script muestra la línea que debes añadir al archivo de inicio de tu shell; nunca edita ese archivo por sí mismo. Cuando ninguna versión ya compilada sirve para tu sistema, pregunta antes de compilar una a partir del código fuente de la versión.

Puedes ejecutarlo de nuevo cuando quieras: actualiza o repara la instalación y nunca añade un segundo lanzador. `sh install-autodoc.sh --help` lista sus opciones, como `--version` para una versión concreta y `--no-launcher`.

**Actualizar.** Instalado con el script, AutoDoc se actualiza solo: **Ayuda › Acerca de**, y luego **Actualizar**. La actualización se ejecuta en el panel de terminal de AutoDoc y, cuando la nueva versión está lista, AutoDoc ofrece **Reiniciar ahora**. Desde una shell, `~/.local/state/autodoc/update.sh` hace lo mismo.

## Otras formas

- Homebrew: `brew install yongjohnlee80/tap/autodoc` (en Linux, solo la interfaz de terminal)
- mise: `mise use -g github:yongjohnlee80/autodoc`
- Go: `go install -tags gui github.com/yongjohnlee80/autodoc/cmd/autodoc@latest` (sin `-tags gui`, solo la interfaz de terminal)
- Un archivo comprimido de la página de Releases del proyecto
- Windows: WSL2 y, después, el script de instalación

Estas se actualizan de la misma forma en que se instalaron (`brew upgrade`, `mise upgrade`, `go install` otra vez), así que **Ayuda › Acerca de** no ofrece Actualizar para ellas. Servidores, contenedores y compilar desde el código fuente: [Referencia, capítulo 2](../../reference/02-quickstart-and-workspaces.md).

**Dos formas de ejecutarlo.** `autodoc --ui` se ejecuta en tu terminal. `autodoc --gui` abre una ventana propia, con los diagramas dibujados y las fuentes que elijas.

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
