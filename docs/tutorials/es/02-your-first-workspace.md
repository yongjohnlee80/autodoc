# Tu primer espacio de trabajo

Un **espacio de trabajo** es una carpeta de notas de la que AutoDoc mantiene un índice, para poder buscar en ellas y seguir sus enlaces. Los archivos siguen siendo Markdown plano en tu disco; AutoDoc nunca los mueve.

1. Inicia AutoDoc: `autodoc --ui` o `autodoc --gui`.
2. Añade un espacio de trabajo: **Ir › Administrar espacios de trabajo…** (SPC W), y luego **Añadir…**: un nombre corto y la carpeta.
3. AutoDoc indexa la carpeta en segundo plano; la línea de estado avisa cuando termina.
4. **SPC o** abre una nota; **Ctrl+N** crea una nueva; **Ctrl+S** guarda.

```mermaid
flowchart LR
  F[Tu carpeta de notas] -->|Añadir| W[Un espacio de trabajo]
  W -->|indexado| S[Búsqueda y enlaces]
  W -->|SPC o| P[Una nota en la página]
```

La próxima vez, `autodoc --ui` abre el último espacio de trabajo que usaste, y `autodoc --ui <name>` abre uno por su nombre.

Más: [Referencia, capítulo 2: Inicio rápido y espacios de trabajo](../../reference/02-quickstart-and-workspaces.md).
