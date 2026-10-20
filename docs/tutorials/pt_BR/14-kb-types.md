# Tipos de KB: um espaço de trabalho que já começa com um método

Um novo espaço de trabalho pode começar vazio, ou a partir de um **tipo de KB**: as pastas, os modelos e o esquema de um jeito de organizar notas, prontos para preencher.

**Adicione um espaço de trabalho com um tipo.** **Ir › Gerenciar espaços de trabalho…** (SPC W), depois **Adicionar…**:

1. **raiz**: a pasta. Digite o caminho completo e aperte **Nova pasta** para criá-la, ou **Procurar…** para escolher uma que já existe.
2. **tipo de KB**: escolha um. O AutoDoc o baixa e mostra as pastas dele e para que serve cada uma, antes de escrever qualquer coisa.
3. **Salvar**: o espaço de trabalho é adicionado, e as pastas e arquivos do tipo são criados nele. Um arquivo que já existe é mantido, nunca sobrescrito.

```mermaid
flowchart LR
  A[Adicione um espaço de trabalho] --> T[Escolha um tipo de KB]
  T --> S[Veja as pastas dele]
  S -->|Salvar| W["Um espaço de trabalho, pronto para preencher"]
```

**Os tipos.**

- **Zettelkasten**: notas transitórias, de literatura e permanentes, e notas de estrutura que as ligam.
- **PARA**: Projetos, Áreas, Recursos e Arquivo.
- **Slip-box**: uma caixa de entrada para capturar, o slip-box de notas prontas, referências e projetos.
- **AutoVim coding KB**: as decisões e o conhecimento de um projeto: ADRs, convenções, playbooks, revisões e mais, para pessoas e agentes de IA que trabalham com código.
- **Nenhum**: uma pasta existente, ou um repositório de código, como está.
- **URL personalizada…**: o repositório git `https://` de qualquer tipo de KB.

Cada tipo também traz modelos e um esquema de frontmatter, para que as notas novas já comecem no formato certo e a linha sobre a página aponte o que falta numa nota. Um tipo só é lido e copiado: nada nele é executado.

**Um espaço de trabalho existente** também pode receber um tipo. Num comando Lua ou em `:lua`, `autodoc.run("kbtype.apply", "para.kb")` adiciona as pastas e arquivos do PARA ao espaço de trabalho aberto, mais uma vez mantendo todo arquivo que já está lá.

Mais: [Referência, Capítulo 2: Início rápido e espaços de trabalho](../../reference/02-quickstart-and-workspaces.md) e [Referência, Capítulo 7: Esquemas de frontmatter e validação](../../reference/07-frontmatter-schemas-and-validation.md).
