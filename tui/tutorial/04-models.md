# Search models: embeddings and a reranker

Without a model, search is by words. **System › Search models…** (SPC a) adds the models that search by meaning. It has two tabs.

**Embedding Models** turn each section of your files into a vector, so a search finds what *means* the same.

1. **Add…** a provider: a local **Ollama** with an embedding model (`ollama pull nomic-embed-text`), **Ollama Cloud**, or any **OpenAI-compatible** endpoint with its key.
2. **Use** it. The workspace is indexed in the background; the status line's mark names the model searching.
3. **Cancel indexing** stops a long first pass; **Words only** goes back to search by words.

**Ranker Models** are a second stage: a reranker reads the top hits against your question and orders them again.

1. **Add…** a ranker: its kind, base URL and model; **Check** tries it.
2. **Use** it. **Window…** sets how many of the top hits it reads.

The search's checkboxes (Lexical, Semantic, Rerank) choose the stages for each search.
