#!/usr/bin/env bash
# M0 smoke test modelů na SPARKu → tabulka jede / nejede / proč.
#
# Spouštět NA SPARKU přes broker (staví a spouští kontejnery, bere GPU), např. z M2:
#   sparkctl run dabing <služby> <priorita> "cd ~/deploy/CeskyDabing && workers/smoke/smoke.sh" "dabing M0 smoke"
#
# Výstup: workers/smoke/results.md (+ results.jsonl), poslední řádek JSON metrik.
set -uo pipefail
cd "$(dirname "$0")"
MODELS=${MODELS:-"whisperx pyannote moss xtts demucs"}
MODELS_DIR=${MODELS_DIR:-$HOME/deploy/CeskyDabing/models}
mkdir -p "$MODELS_DIR"
: > results.jsonl

run() { # run <model> <image>
  docker run --rm --gpus all -e SMOKE_LOAD="${SMOKE_LOAD:-0}" -e HF_TOKEN="${HF_TOKEN:-}" \
    -v "$MODELS_DIR:/models" -v "$PWD/smoke.py:/smoke.py:ro" "$2" python3 /smoke.py "$1" 2>"logs/$1.err" | tail -1
}
mkdir -p logs
for m in $MODELS; do
  case $m in
    xtts)   img=aistack/audio-tts-xtts:latest ;;          # už postavený image z AiStacku
    demucs) img=dabing/separate:latest
            docker build -q -t "$img" ../separate >"logs/build-$m.log" 2>&1 ;;
    *)      img=dabing/smoke-$m:latest
            docker build --target "$m" -t "$img" . >"logs/build-$m.log" 2>&1 ;;
  esac
  out=$(run "$m" "$img")
  if [[ "$out" != \{* ]]; then
    out=$(jq -cn --arg m "$m" --arg why "$(tail -3 "logs/build-$m.log" "logs/$m.err" 2>/dev/null | tr '\n' ' ' | cut -c1-300)" \
      '{model:$m, ok:false, why:("image/běh selhal: "+$why)}')
  fi
  echo "$out" | tee -a results.jsonl
done

{
  echo "| model | jede | torch | GPU | proč / poznámka |"
  echo "|---|---|---|---|---|"
  jq -r '"| \(.model) | \(if .ok then "✅" else "❌" end) | \(.torch // "-") | \(.cap // "-") | \(.why) |"' results.jsonl
} > results.md
cat results.md
jq -sc '{models:{ok:(map(select(.ok))|length), failed:(map(select(.ok|not))|length)}}' results.jsonl
