#!/usr/bin/env bash
# tei-rerank.sh: run a TEI (Hugging Face Text Embeddings Inference) reranker locally, switch its
# model, and test its ranking accuracy by hand. TEI only: the server is TEI's /rerank, started from
# compose/tei-rerank/docker-compose.yaml on a GPU (NVIDIA) or on the CPU.
#
#   shell/tei/tei-rerank.sh up [gpu|cpu|auto] [MODEL]   start TEI and wait until it answers
#   shell/tei/tei-rerank.sh switch MODEL                 restart the running TEI with another model
#   shell/tei/tei-rerank.sh down                         stop TEI (the downloaded models are kept)
#   shell/tei/tei-rerank.sh status                       which TEI runs, and the model it serves
#   shell/tei/tei-rerank.sh logs                         follow TEI's log
#   shell/tei/tei-rerank.sh models                       the reranker models you can switch between
#   shell/tei/tei-rerank.sh rank QUERY TEXT...           rank the texts for the query, best first
#   shell/tei/tei-rerank.sh eval [FILE]                  score a cases file: hit@1, hit@3, MRR
#
# MODEL is a short name from `models` or any Hugging Face id TEI can load as a reranker.
#
# Environment (all optional):
#   TEI_PORT       local port (default 18080)
#   TEI_URL        an already running TEI to test instead (rank, eval, status); up/down ignore it
#   TEI_VERSION    TEI image version (default 1.9)
#   HF_TOKEN       passed to TEI for gated models. This script never prints or writes it, but Docker
#                  puts it in the container's environment, where anyone who can run docker can read
#                  it (docker inspect, docker compose config). Only set it when a model needs it.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
compose_file="$here/../../compose/tei-rerank/docker-compose.yaml"
version="${TEI_VERSION:-1.9}"
port="${TEI_PORT:-18080}"
url="${TEI_URL:-http://127.0.0.1:$port}"
default_model="BAAI/bge-reranker-v2-m3"

die() { echo "tei-rerank: $*" >&2; exit 1; }

# The reranker models TEI loads (XLM-RoBERTa, GTE and ModernBERT sequence classifiers).
# Qwen3-Reranker is not among them: TEI cannot load it yet.
models() {
  cat <<'EOF'
short name      Hugging Face id                              notes
v2-m3           BAAI/bge-reranker-v2-m3                      default; multilingual, ~0.6B
large           BAAI/bge-reranker-large                      English and Chinese, ~0.6B
base            BAAI/bge-reranker-base                       English and Chinese, ~0.3B, fastest
gte-multi       Alibaba-NLP/gte-multilingual-reranker-base   multilingual, ~0.3B
gte-modernbert  Alibaba-NLP/gte-reranker-modernbert-base     English, ~0.15B, long inputs
EOF
}

resolve() {
  case "${1:-}" in
    "" | v2-m3) echo "$default_model" ;;
    large) echo "BAAI/bge-reranker-large" ;;
    base) echo "BAAI/bge-reranker-base" ;;
    gte-multi) echo "Alibaba-NLP/gte-multilingual-reranker-base" ;;
    gte-modernbert) echo "Alibaba-NLP/gte-reranker-modernbert-base" ;;
    */*) echo "$1" ;;
    *) die "unknown model '$1': see \`models\`, or give a Hugging Face id (org/name)" ;;
  esac
}

# cap_tag is the TEI image tag for a compute capability ("12.0"), or nothing when TEI builds none.
# Each GPU image is built for one capability and refuses every other; the bare version is 8.0's.
cap_tag() {
  case "$1" in
    12.0) echo "120-$version" ;;
    12.1) echo "121-$version" ;;
    10.0) echo "100-$version" ;;
    9.0) echo "hopper-$version" ;;
    8.9) echo "89-$version" ;;
    8.6) echo "86-$version" ;;
    8.0) echo "$version" ;;
    7.5) echo "turing-$version" ;;
  esac
}

# gpu_tag is the TEI image tag for this machine's NVIDIA GPU, from its compute capability.
gpu_tag() {
  local cap tag
  cap="$(nvidia-smi --query-gpu=compute_cap --format=csv,noheader 2>/dev/null | head -1 | tr -d ' ')" ||
    true
  [ -n "$cap" ] || die "no NVIDIA GPU found (nvidia-smi): use \`up cpu\`"
  tag="$(cap_tag "$cap")"
  [ -n "$tag" ] || die "no TEI image for compute capability $cap: use \`up cpu\`"
  echo "$tag"
}

# arch_refusal is TEI's refusal of its image, when the GPU container's log has one: "Runtime compute
# cap 120 is not compatible with compile time compute cap 89". TEI then exits, and Docker restarts
# it into the same refusal, so there is nothing to wait for.
arch_refusal() {
  compose --profile gpu logs --no-color tei-gpu 2>/dev/null |
    grep -o 'Runtime compute cap [0-9]* is not compatible with compile time compute cap [0-9]*' |
    tail -1
}

# refused stops the GPU container TEI refused, and says which tag the GPU it ran on needs. The
# numbers are TEI's own: the runtime one is the GPU, the compile-time one the image.
refused() {
  local line="$1" runtime cap tag
  compose --profile gpu down
  runtime="$(printf '%s' "$line" | sed -n 's/^Runtime compute cap \([0-9]*\) .*/\1/p')"
  cap="${runtime%?}.${runtime: -1}"
  tag="$(cap_tag "$cap")"
  echo "tei-rerank: TEI refused the image: $line" >&2
  echo "  The image tag is wrong for the GPU TEI ran on (compute capability $cap); the GPU is" >&2
  echo "  supported. The tag was chosen for the first GPU nvidia-smi lists, and TEI ran on another." >&2
  if [ -n "$tag" ]; then
    echo "  Run TEI on it with ghcr.io/huggingface/text-embeddings-inference:$tag, or use \`up cpu\`." >&2
  else
    echo "  TEI builds no image for compute capability $cap: use \`up cpu\`." >&2
  fi
  exit 1
}

compose() { docker compose -f "$compose_file" "$@"; }

# running is the profile of the TEI this script started, if it still runs.
running() {
  local p
  for p in gpu cpu; do
    if [ -n "$(compose --profile "$p" ps -q "tei-$p" 2>/dev/null)" ]; then
      echo "$p"
      return
    fi
  done
}

wait_ready() {
  local profile="$1" i line
  echo "waiting for TEI at $url (the first start downloads the model) ..."
  for i in $(seq 1 120); do
    if curl -fsS -m 5 "$url/health" >/dev/null 2>&1; then
      echo "ready after about $((i * 5))s"
      return
    fi
    if [ "$profile" = gpu ]; then
      line="$(arch_refusal || true)"
      [ -z "$line" ] || refused "$line"
    fi
    sleep 5
  done
  die "TEI did not answer within 10 minutes: see \`logs\`"
}

up() {
  local profile="${1:-auto}" model
  model="$(resolve "${2:-}")"
  if [ "$profile" = auto ]; then
    if command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi -L >/dev/null 2>&1; then profile=gpu; else profile=cpu; fi
  fi
  case "$profile" in gpu | cpu) ;; *) die "profile is gpu, cpu or auto, not '$profile'" ;; esac
  local other
  other="$(running || true)"
  if [ -n "$other" ] && [ "$other" != "$profile" ]; then
    compose --profile "$other" down
  fi
  if [ "$profile" = gpu ]; then
    export TEI_GPU_TAG
    TEI_GPU_TAG="$(gpu_tag)"
  else
    export TEI_CPU_TAG="cpu-$version"
  fi
  export TEI_MODEL="$model" TEI_PORT="$port"
  echo "starting TEI ($profile) with $model on 127.0.0.1:$port"
  compose --profile "$profile" up -d --force-recreate "tei-$profile"
  url="http://127.0.0.1:$port"
  wait_ready "$profile"
  status
}

switch() {
  [ -n "${1:-}" ] || die "switch needs a model: see \`models\`"
  local profile
  profile="$(running || true)"
  [ -n "$profile" ] || die "no TEI is running: use \`up\`"
  up "$profile" "$1"
}

down() {
  local p
  for p in gpu cpu; do compose --profile "$p" down; done
}

status() {
  local info
  if ! info="$(curl -fsS -m 5 "$url/info" 2>/dev/null)"; then
    echo "no TEI answers at $url"
    return 1
  fi
  printf '%s' "$info" | python3 -c '
import json, sys
d = json.load(sys.stdin)
kind = "reranker" if "reranker" in json.dumps(d.get("model_type", {})) else "NOT a reranker"
print("TEI %s at %s: %s (%s), max input %s tokens" % (
    d.get("version", "?"), sys.argv[1], d.get("model_id", "?"), kind, d.get("max_input_length", "?")))
' "$url"
}

rank() {
  [ $# -ge 2 ] || die "rank needs a query and at least one text"
  local query="$1"
  shift
  python3 - "$url" "$query" "$@" <<'EOF'
import json, sys, time, urllib.request
url, query, texts = sys.argv[1], sys.argv[2], sys.argv[3:]
body = json.dumps({"query": query, "texts": texts, "truncate": True}).encode()
req = urllib.request.Request(url + "/rerank", data=body, headers={"content-type": "application/json"})
start = time.time()
with urllib.request.urlopen(req, timeout=300) as r:
    ranked = json.load(r)
took = time.time() - start
for place, hit in enumerate(sorted(ranked, key=lambda h: -h["score"]), 1):
    text = texts[hit["index"]]
    print("%d. %.6f  [%d] %s" % (place, hit["score"], hit["index"], text if len(text) <= 100 else text[:97] + "..."))
print("(%d texts in %.2fs)" % (len(texts), took))
EOF
}

# eval scores a JSON Lines file of cases: {"query": "...", "texts": [...], "relevant": [indexes]}.
eval_cases() {
  local file="${1:-$here/cases.example.jsonl}"
  [ -f "$file" ] || die "no cases file at $file"
  python3 - "$url" "$file" <<'EOF'
import json, statistics, sys, time, urllib.request
url, path = sys.argv[1], sys.argv[2]
cases = [json.loads(line) for line in open(path, encoding="utf-8") if line.strip() and not line.startswith("#")]
hits1 = hits3 = 0
rr, took = [], []
for n, case in enumerate(cases, 1):
    relevant = set(case["relevant"])
    body = json.dumps({"query": case["query"], "texts": case["texts"], "truncate": True}).encode()
    req = urllib.request.Request(url + "/rerank", data=body, headers={"content-type": "application/json"})
    start = time.time()
    with urllib.request.urlopen(req, timeout=300) as r:
        ranked = sorted(json.load(r), key=lambda h: -h["score"])
    took.append(time.time() - start)
    order = [h["index"] for h in ranked]
    first = next((place for place, i in enumerate(order, 1) if i in relevant), None)
    hits1 += first == 1
    hits3 += first is not None and first <= 3
    rr.append(1 / first if first else 0.0)
    mark = "ok  " if first == 1 else "MISS"
    print("%s %2d. first relevant at %s  %s" % (mark, n, first if first else "-", case["query"][:70]))
count = len(cases)
if count == 0:
    sys.exit("tei-rerank: %s has no cases" % path)
print()
print("cases %d   hit@1 %.2f   hit@3 %.2f   MRR %.3f   median %.2fs per case" % (
    count, hits1 / count, hits3 / count, sum(rr) / count, statistics.median(took)))
EOF
}

case "${1:-}" in
  up) shift; up "$@" ;;
  switch) shift; switch "$@" ;;
  down) down ;;
  status) status ;;
  logs) p="$(running || true)"; [ -n "$p" ] || die "no TEI is running"; compose --profile "$p" logs -f "tei-$p" ;;
  models) models ;;
  rank) shift; rank "$@" ;;
  eval) shift; eval_cases "$@" ;;
  "" | -h | --help | help) sed -n '2,22p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' ;;
  *) die "unknown command '$1': see --help" ;;
esac
