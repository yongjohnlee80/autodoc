# Seu primeiro espaço de trabalho

Um **espaço de trabalho** é uma pasta de notas da qual o AutoDoc mantém um índice, para poder buscá-las e seguir os links entre elas. Os arquivos continuam sendo Markdown puro no seu disco; o AutoDoc nunca os move.

1. Inicie o AutoDoc: `autodoc --ui` ou `autodoc --gui`.
2. Adicione um espaço de trabalho: **Ir › Gerenciar espaços de trabalho…** (SPC W), depois **Adicionar…**: um nome curto e a pasta.
3. O AutoDoc indexa a pasta em segundo plano; a linha de status avisa quando terminar.
4. **SPC o** abre uma nota; **Ctrl+N** cria uma nova; **Ctrl+S** salva.

```mermaid
flowchart LR
  F[Sua pasta de notas] -->|Adicionar| W[Um espaço de trabalho]
  W -->|indexado| S[Busca e links]
  W -->|SPC o| P[Uma nota na página]
```

Da próxima vez, `autodoc --ui` abre o último espaço de trabalho que você usou, e `autodoc --ui <name>` abre um pelo nome.

Mais: [Referência, Capítulo 2: Início rápido e espaços de trabalho](../../reference/02-quickstart-and-workspaces.md).
