# CeskyDabing

Lokální AI dabing dětských seriálů EN → CS (jen pro rodinu). Plán: [Prompts/00-DABING_PLAN.md](Prompts/00-DABING_PLAN.md).

Go orchestrátor `dub` na Mac Mini M2, Python model workery na SPARKu, úložiště JODA přes NFS `/mnt/dabing`.

## Struktura

```
cmd/dub/                    CLI (cobra): add, run, status, doctor
internal/config/            dub.yaml + env (DUB_ROOT, DUB_DB, DUB_SEPARATE_URL, DUB_SEPARATE_STORAGE)
internal/pipeline/          stavový stroj kroků, kroky extract + separate
internal/media/             ffmpeg/ffprobe
internal/storage/           shared (NFS, cesty) | upload (fallback bez NFS)
internal/store/             SQLite: migrace (golang-migrate) + sqlc (internal/store/db)
internal/workers/separate/  Go klient generovaný z workers/separate/openapi.yaml (oapi-codegen)
workers/separate/           FastAPI + Demucs htdemucs_ft, Dockerfile pro GB10
workers/smoke/              M0 smoke test modelů na sm_121
deploy/                     compose.spark.yaml, launchd plist (M4)
docs/nfs.md                 nastavení NFS JODA → M2 + SPARK
```

## Použití

```bash
make build                                  # bin/dub
cp dub.example.yaml ~/.config/dub/dub.yaml  # volitelné
bin/dub doctor                              # NFS, ffmpeg, rubberband, SQLite, workery
bin/dub add bluey /mnt/dabing/incoming/bluey/Bluey.S01E03.mkv   # → díl s01e03
bin/dub run bluey --until separate          # hotové kroky přeskočí
bin/dub run bluey s01e03 --from separate    # znovu od kroku (pozdější kroky se zneplatní)
bin/dub status
```

Výstupy: `work/<serial>/<dil>/` — `audio.wav` (48 kHz), `info.json`, `vocals.wav`,
`background.wav`, `stems/{drums,bass,other}.wav`, `separate.json`. Stav kroků je
v tabulce `episode_steps`, každé volání workeru v `jobs`.

Kroky: extract → separate → transcribe → identify → refs → translate → synthesize → fit → mix → mux
(M0 má extract + separate, ostatní vrátí „krok zatím není implementovaný“).

## Worker lokálně (bez GPU)

```bash
cd workers/separate
SEPARATE_BACKEND=passthrough SEPARATE_ROOTS=/tmp/dabing uvicorn app.main:app --port 8101
```

## Generování

`make generate` = `sqlc generate` + `go generate ./...` (oapi-codegen jako `go tool`).
