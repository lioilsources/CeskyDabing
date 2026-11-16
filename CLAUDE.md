# CeskyDabing

<!-- Max 80 řádků. Co se nevejde, jde do docs/ a sem jeden odkaz. Veřejné repo: žádné LAN IP, domovské cesty, ID. -->

## Účel
Lokální AI dabing dětských seriálů EN → CS pro rodinu. Go orchestrátor `dub` na dev Macu,
Python model workery na SPARKu (jen přes spark-broker), sdílené úložiště `/mnt/dabing`.

## Build a test
```
make gate          # před každým pushem: generate, gofmt, vet, test, py_compile, build
make build         # bin/dub
make generate      # sqlc + oapi-codegen; generovaný kód needitovat ručně
```
Worker lokálně bez GPU: `SEPARATE_BACKEND=passthrough` (viz README).

## Mapa
- `cmd/dub/` — CLI (cobra): add, run, status, doctor, service
- `internal/pipeline/` — stavový stroj kroků + implementace kroků
- `internal/store/` — SQLite (modernc), migrace, sqlc dotazy; `db/` je generované
- `internal/workers/<w>/` — Go klient generovaný z `workers/<w>/openapi.yaml`
- `internal/storage/` — shared (cesty) | upload (fallback bez NFS)
- `workers/` — Python FastAPI workery, jeden Dockerfile na worker; `smoke/` = test modelů na sm_121
- `deploy/compose.spark.yaml` — workery na SPARKu (spouští je jen broker)
- `Prompts/` — plány; aktuální je `01-PLAN-M0-dokonceni-M1.md`

## Invarianty
- Logika jen v Go. Workery jsou stateless obaly modelů: cesty dovnitř, cesty ven.
- OpenAPI spec workeru je zdroj pravdy; Python modely a Go klient s ní musí sedět.
- Krok pipeline je idempotentní a zapisuje atomicky (`.jméno.tmp` + rename).
  Hotový krok s existujícími výstupy se přeskočí, spuštěný krok zneplatní pozdější.
- SQLite je lokálně, nikdy na sdíleném úložišti. Nasazenou migraci needitovat.
- Adresy strojů jen z `.env` (`SPARK_HOST`), nikdy v kódu ani v docs.
- Na SPARKu nic mimo broker: build image, smoke i workery jsou úlohy `sparkctl` (docs/spark.md).

## Odchylky
Žádné. Komentáře anglicky, CLI hlášky a docs česky.

## Kam dál
README.md, docs/nfs.md, docs/spark.md, Prompts/
