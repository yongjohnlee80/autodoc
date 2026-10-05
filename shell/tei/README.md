# TEI reranker for local testing

`tei-rerank.sh` runs a **TEI** ([Hugging Face Text Embeddings Inference](https://github.com/huggingface/text-embeddings-inference))
reranker on your machine, switches between reranker models, and measures their ranking accuracy
by hand. AutoDoc's search can use the same server as its ranker.

The container is defined in [`compose/tei-rerank/docker-compose.yaml`](../../compose/tei-rerank/docker-compose.yaml),
with a `gpu` profile (NVIDIA) and a `cpu` profile. The script picks one, and the right image tag
for your GPU.

## Requirements

- Docker with the Compose plugin, and a user that can run `docker`.
- For the GPU: an NVIDIA GPU, its driver, and the
  [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html).
  TEI's GPU images are NVIDIA-only. On any other machine, use the CPU.
- `curl` and `python3` (standard library only) for `status`, `rank` and `eval`.

## Use

```sh
shell/tei/tei-rerank.sh up            # GPU if one is found, else CPU; the default model
shell/tei/tei-rerank.sh up cpu base   # the CPU, with BAAI/bge-reranker-base
shell/tei/tei-rerank.sh switch large  # restart the running TEI with another model
shell/tei/tei-rerank.sh status        # what is running
shell/tei/tei-rerank.sh rank "error 403 at login" "sunny week" "A 403 means the token expired"
shell/tei/tei-rerank.sh eval          # score shell/tei/cases.example.jsonl
shell/tei/tei-rerank.sh eval my-cases.jsonl
shell/tei/tei-rerank.sh down
```

TEI listens on `http://127.0.0.1:18080` (`TEI_PORT` changes it). The first start of a model
downloads the image and the model (into the `autodoc-tei_tei-models` Docker volume), so later
starts are quick. On a CPU, the warm-up takes a few minutes and briefly needs several GB of memory.

Once started, TEI comes back by itself when Docker restarts, after a reboot for example
(`restart: unless-stopped`). `down` removes the container, so a stopped TEI stays stopped.

## Models

`shell/tei/tei-rerank.sh models` lists the short names. Any Hugging Face id TEI can load as a
reranker works too.

| Short name | Model | Notes |
|---|---|---|
| `v2-m3` (default) | `BAAI/bge-reranker-v2-m3` | multilingual, ~0.6B |
| `large` | `BAAI/bge-reranker-large` | English and Chinese, ~0.6B |
| `base` | `BAAI/bge-reranker-base` | English and Chinese, ~0.3B, fastest |
| `gte-multi` | `Alibaba-NLP/gte-multilingual-reranker-base` | multilingual, ~0.3B |
| `gte-modernbert` | `Alibaba-NLP/gte-reranker-modernbert-base` | English, ~0.15B, long inputs |

`bge-m3` itself is an embedding model; `bge-reranker-v2-m3` is its reranker. TEI cannot load
Qwen3-Reranker yet ([TEI #643](https://github.com/huggingface/text-embeddings-inference/issues/643)).

## Measuring accuracy

`eval` reads JSON Lines, one case per line:

```json
{"query": "how do I cancel a running import", "texts": ["...", "...", "..."], "relevant": [1]}
```

`relevant` holds the 0-based indexes of the texts that answer the query. For each case, `eval`
prints where the first relevant text landed, then **hit@1**, **hit@3** and **MRR** over all cases,
with the median time per case. To compare models, `switch` between them and run `eval` on the
same file. Build the file from your own documents: the example is made up and too easy to tell
models apart.

`TEI_URL` points `status`, `rank` and `eval` at a TEI that is already running elsewhere.

## Tokens

None of the listed models needs a token. For a gated model, export `HF_TOKEN` before `up`. The
script and the Compose file never write it down, but Docker passes it into the container's
environment, so anyone who can run `docker` on the machine can read it (`docker inspect`,
`docker compose config`). Use a read-only token, and unset it when you're done.

## Using it from AutoDoc

Once AutoDoc's ranker models are available (System › AI models…, the **Ranker Models** tab), add
a ranker of kind **TEI** with the base URL `http://127.0.0.1:18080` and no API key, then **Use**
it. AutoDoc checks that the server is a reranker before switching to it, and re-ranks the top
candidates of every worded search with it.
