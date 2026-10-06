# KB retrieval: AutoDoc search vs grep (2026-10-06)

These measurements back the retrieval rules in `lua/autodoc/kb/templates/KB_OPERATIONS.md`
(revision 2), which every KB receives as its `KB_OPERATIONS.md`. Both runs used AutoDoc 0.1.17 on a
KB in the v2 layout.

## KB A: 2,200 documents, 32 lookups

### Method

- **Questions.** 32 lookups an agent had really needed, each with its expected answers pinned to a
  document and a line range: 62 accepted answers in all.
  - By kind: 3 title, 13 section, 10 paraphrase, 6 fact.
  - **Checked before any run.** Four independent reviewers, using grep and file reads only (so the key
    isn't biased toward the system being measured), checked the key. They added the other passages
    that also answer a question, and reworded the questions that had a wrong premise.
- **The key lives outside the KB.** Inside it, both grep and AutoDoc would find the questions and
  their answers.
- **Models.** Ollama `snowflake-arctic-embed2` (1024 dimensions) with a reranker.
- **Ranking.**
  - AutoDoc `search.query` with `limit 10`, once per stage set.
  - A naive grep ranker over the tree: files ranked by how many of the question's keywords they
    contain, then by total matches.
- **Agents.** One agent per question and method, 96 in all.
  - Methods: grep over the KB before the v2 migration, grep over the v2 tree, and AutoDoc
    search-first.
  - Costs come from the API's own usage records.
  - **Pulled in** is the context of an agent's last request minus its first. The fixed system context
    is the same for every agent, so this isolates the KB material the lookup read.
  - A compliance check over every tool call found no agent outside its method. The check was itself
    tested against a planted violation of each kind.

### Ranking

| Method | answer doc in top 1 | top 3 | top 5 | top 10 | answer section in top 3 |
|---|---|---|---|---|---|
| AutoDoc, semantic + reranker | 0.69 | 0.94 | 0.94 | 0.94 | 0.81 |
| AutoDoc, semantic only | 0.59 | 0.72 | 0.78 | 0.91 | 0.59 |
| AutoDoc, words only, full question as query | 0.00 | 0.00 | 0.00 | 0.00 | 0.00 |
| grep ranker, v2 tree without `archive/` and `raw/` | 0.22 | – | 0.47 | 0.63 | – |
| grep ranker, whole tree (old indexes and logs included) | 0.03 | – | 0.22 | 0.47 | – |

### Agents

| Method | found the answer | KB tokens pulled in, mean (p90) | tool calls, mean | wall time, mean |
|---|---|---|---|---|
| grep, pre-v2 KB | 32/32 | 9,238 (13,439) | 3.09 | 15.4 s |
| grep, v2 KB | 32/32 | 8,008 (11,826) | 2.59 | 14.0 s |
| AutoDoc search-first | 32/32 | 7,140 (9,067) | 2.47 | 8.8 s |

## KB B: 18 lookups

### Method

- **Questions.** 18, each with a known answer document; half were paraphrased.
- **Models.** `bge` embeddings with `bge-reranker-v2-m3`.
- **Calls.** Two tool calls per protocol.
- **Tokens** are estimated as characters / 4.

### Results

| Protocol | answer doc first | in top 3 / found | tokens per lookup | time |
|---|---|---|---|---|
| grep: `rg -c` ranking, then `-C1` context | 6/18 | 9/18 top 3 | ~9.9k | 14 ms |
| lean grep: files with every keyword, then 3 matches | 5/18 | 17/18 found | ~515 | 22 ms |
| AutoDoc, all stages, plain question, `limit 10` | 15/18 (scoped with `paths`) | 18/18 top 3 | ~2.2k | 0.4 s |
| AutoDoc, all stages, `limit 3`, plus the line-range read | 15/18 | 18/18 top 3 | ~840 | 0.6 s |
| AutoDoc, words only, 2–3 keywords, `limit 3` | 12/18 | 14/18 found | ~580 | 42 ms |
| AutoDoc, words only, the full question | 1/18 | – | – | – |

- **What words-only search misses.** It misses what substring grep finds: an unstarred prefix, two
  words that never share a section, and wording that differs from the keywords.
- **Scoping.** Unscoped, the retired `archive/index.md` and `log.md` took grep's top hit in 14 of 18
  lookups. They also crowded AutoDoc's top 3 in 5 of 18.
- **Usage.** 30 days of agent transcripts on this KB showed about 29 KB searches and 18 reads a day.
  Agents usually already knew the target path. A whole-file read was a median 2.6k tokens, against
  about 220 for a hit's line range.

## What the rules take from this

1. **A known target goes straight to Read or `rg`;** search is for topics.
2. **With the reranker, `limit 3`.** It loses nothing against `limit 10` on either KB (KB A 0.94 = 0.94;
   KB B 18/18) and costs about 60% fewer tokens. Without the reranker, use `limit 10`: KB A's top 3
   holds the answer 0.72 of the time, against 0.91 for the top 10.
3. **Words-only search needs 2–3 distinctive terms.** A sentence finds nothing. Retry once (drop a
   word, star a prefix), then `rg` with the terms OR'd.
4. **Scope with `paths`, and never grep the KB root unscoped.** Retired indexes and logs crowd out the
   live documents.
5. **Read the hit's line range,** and read a whole file only when it must be read end to end.
6. **Agents find answers either way;** search changes the cost. On KB A, AutoDoc search-first used 11%
   fewer KB tokens than grep over the same tree (23% fewer than the pre-v2 KB). It also used fewer
   tool calls, finished faster, and cut the heaviest lookups most.
