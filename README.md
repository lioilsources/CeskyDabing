# CeskyDabing

Lokální AI dabing dětských seriálů EN → CS (jen pro rodinu). Plán: [Prompts/00-DABING_PLAN.md](Prompts/00-DABING_PLAN.md),
aktuální postup: [Prompts/01-PLAN-M0-dokonceni-M1.md](Prompts/01-PLAN-M0-dokonceni-M1.md).

Go orchestrátor `dub` běží na dev Macu, Python model workery na GPU stroji (SPARK, jen přes
spark-broker), soubory si předávají přes sdílené úložiště `/mnt/dabing`.

## Struktura

```
cmd/dub/                    CLI (cobra): add, run, status, doctor, service
internal/config/            dub.yaml + .env + proměnné prostředí
internal/pipeline/          stavový stroj kroků, kroky extract, separate, transcribe
internal/media/             ffmpeg/ffprobe
internal/storage/           shared (cesty) | upload (fallback bez sdíleného úložiště)
internal/store/             SQLite: migrace (golang-migrate) + sqlc (internal/store/db)
internal/workers/<w>/       Go klienti generovaní z workers/<w>/openapi.yaml (oapi-codegen)
internal/workers/files/     společné souborové API workerů (upload fallback)
workers/common/             společný kód Python workerů (kořeny, scratch, /v1/files)
workers/separate/           FastAPI + Demucs htdemucs_ft, Dockerfile pro GB10
workers/asr/                FastAPI + WhisperX + pyannote (přepis, slova, mluvčí, embedding, F0)
workers/smoke/              M0 smoke test modelů na sm_121
deploy/compose.spark.yaml   workery na SPARKu (spouští je broker)
docs/                       nfs.md (úložiště), spark.md (broker, build, běh)
```

## Nastavení

```bash
cp .env.example .env              # SPARK_HOST a další; .env se necommituje
cp dub.example.yaml ~/.config/dub/dub.yaml   # volitelné
make build
bin/dub doctor                    # úložiště, ffmpeg, rubberband, SQLite, workery
```

## Použití

```bash
bin/dub add bluey /mnt/dabing/incoming/bluey/Bluey.S01E03.mkv   # → díl s01e03
bin/dub run bluey --until separate          # hotové kroky přeskočí
bin/dub run bluey s01e03 --from separate    # znovu od kroku (pozdější kroky se zneplatní)
bin/dub status
```

Na SPARK se pipeline pouští přes `sparkctl exec` — viz [docs/spark.md](docs/spark.md).

Výstupy: `work/<serial>/<dil>/` — `audio.wav` (48 kHz), `info.json`, `vocals.wav`,
`background.wav`, `stems/{drums,bass,other}.wav`, `separate.json`. Stav kroků je
v tabulce `episode_steps`, každé volání workeru v `jobs`.

Kroky: extract → separate → transcribe → identify → refs → translate → synthesize → fit → mix → mux.

Soubory seriálu v `series/<serial>/`:
- `cast.yaml` — postavy (zdroj pravdy). Nová postava se jmenuje `UNKNOWN_n`; jméno, strategii
  a poznámky můžeš upravit, `centroid` a `episodes` spravuje `dub`.
- `refs/<postava>.wav` — reference hlasu; existující se nepřepisuje, pro novou ji smaž.
- `glossary.yaml` — volitelný glosář pro překlad:
  ```yaml
  terms:
    - {en: Bingo, cs: Bingo, note: nesklonňovat}
    - {en: Keepy Uppy, cs: Nespadni}
  ```

Překlad jde přes LLM gateway AiStacku (`DUB_LLM_URL`, výchozí model `openclaw-default` = qwen36,
služba brokeru `qwen36`). Odpověď záložního modelu se odmítne.

## Worker lokálně (bez GPU)

```bash
python3 -m venv .venv && .venv/bin/pip install fastapi "uvicorn[standard]" python-multipart numpy soundfile
cd workers/separate && SEPARATE_BACKEND=passthrough SEPARATE_ROOTS=/tmp/dabing PYTHONPATH=../common \
  ../../.venv/bin/uvicorn app.main:app --port 8101
cd workers/asr && ASR_BACKEND=passthrough ASR_ROOTS=/tmp/dabing PYTHONPATH=../common \
  ../../.venv/bin/uvicorn app.main:app --port 8105
DUB_ROOT=/tmp/dabing DUB_SEPARATE_URL=http://localhost:8101 DUB_ASR_URL=http://localhost:8105 bin/dub run …
```

## Vývoj

`make gate` před každým pushem (generate, gofmt, vet, test, py_compile, build).
`make generate` = `sqlc generate` + `go generate ./...` (oapi-codegen jako `go tool`).

Launchd služba pro `dub serve` (M4): `bin/dub service plist > ~/Library/LaunchAgents/io.github.lioilsources.dub.plist`.
