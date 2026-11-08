# A vista Renderizada: Ctrl+T

Na janela e num terminal, **Ctrl+T** alterna a página entre a vista **Bruta**, o Markdown como foi escrito, e a vista **Renderizada**: os títulos nos seus tamanhos, as marcações escondidas, tabelas, código e diagramas Mermaid desenhados.

```mermaid
flowchart LR
  R["Bruta, o Markdown"] -->|Ctrl+T| D["Renderizada, como se lê"]
  D -->|Ctrl+T| R
```

A vista Renderizada continua sendo o editor. Toda linha é editável: a linha sob o cursor mostra o seu Markdown, as outras mostram o resultado. Ctrl+T de novo volta à Bruta.

**Arquivos HTML** perguntam como abrir: **Simplificado**, o texto deles somente leitura, ou **Bruto**, o próprio HTML para editar e salvar. A vista Renderizada de um arquivo HTML bruto é a página dele: na janela, desenhada nativamente com as próprias folhas de estilo e imagens; num terminal, desenhada em células. `j k` rolam, e os links abrem notas, arquivos e o navegador.

**Arquivo › Pré-visualizar HTML** (SPC H) abre a página da nota ao lado do editor, ao vivo enquanto você digita.

**O menu do botão direito**: Desfazer, Refazer, Copiar, Recortar e Colar, Ver diagrama, Ir para o link, Voltar e, na GUI, o Zoom. Pelo teclado: **Shift+F10**, a tecla **Menu** se o seu teclado tiver uma, ou **SPC .**.

Num terminal, a vista Renderizada é o mesmo editor desenhado em células: títulos, tabelas com borda, blocos de código, os seus diagramas Mermaid e os seus gráficos Vega-Lite, e edita-se no lugar como na janela. Um diagrama mais largo que a vista desloca-se com `zh` e `zl`. Num terminal que mostra os gráficos do kitty (kitty ou Ghostty, por exemplo), uma imagem sozinha no seu parágrafo é desenhada no lugar; nos outros aparece o seu texto alternativo.

Mais: [Referência, Capítulo 4: Autoria rica, incorporações e exportação](../../reference/04-rich-authoring-embeds-and-export.md).
