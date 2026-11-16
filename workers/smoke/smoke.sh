#!/usr/bin/env bash
# M0 smoke test of the models on SPARK → table works / fails / why.
#
# Runs ON SPARK as a spark-broker job (it builds and starts containers and uses
# the GPU); see docs/spark.md for the sparkctl command.
#
# Output: workers/smoke/results.md (+ results.jsonl); the last line is JSON metrics.
set -uo pipefail
cd "$(dirname "$0")"
MODELS=${MODELS:-"whisperx pyannote moss xtts demucs"}
MODELS_DIR=${MODELS_DIR:-$(cd .. && cd .. && pwd)/models}
mkdir -p "$MODELS_DIR"
: > results.jsonl

run() { # run <model> <image>
  docker run --rm --gpus all -e SMOKE_LOAD="${SMOKE_LOAD:-0}" -e HF_TOKEN="${HF_TOKEN:-}" \
    -v "$MODELS_DIR:/models" -v "$PWD/smoke.py:/smoke.py:ro" "$2" python3 /smoke.py "$1" 2>"logs/$1.err" | tail -1
}
mkdir -p logs
for m in $MODELS; do
  case $m in
    xtts)   img=aistack/audio-tts-xtts:latest ;;          # image already built by AiStack
    demucs) img=dabing/separate:latest
            docker build -q -t "$img" -f ../separate/Dockerfile .. >"logs/build-$m.log" 2>&1 ;;
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
