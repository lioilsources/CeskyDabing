# Workery na SPARKu (přes spark-broker)

SPARK sdílí víc projektů a řídí ho fronta spark-broker. Workery dabingu proto nejsou
always-on kontejnery, ale **doplňky (addons) brokeru**: broker je zvedne, když úloha
projektu `dabing` chce danou službu, a shodí je, když je nikdo nechce. Ručně se na
SPARKu nespouští nic — ani `docker compose up`, ani build image.

## Služby

| Služba brokeru | Kontejner | Port | Paměť | Stav |
|---|---|---|---|---|
| `dabing-separate` | `dabing-separate` | 8101 | změřit (odhad 8 GiB) | definice níže, čeká na PR do AiStacku |
| `dabing-asr` | `dabing-asr` | 8105 | změřit | worker hotový, PR do AiStacku po smoke testu |
| `tts` (AiStack) | `audio-tts-xtts` za službou `audio` | 8093 | už v brokeru | M1 používá tuhle službu, vlastní worker odložený (docs/tts.md) |

Porty 8102–8104 jsou na SPARKu obsazené jinými službami.

## Definice doplňku (config brokeru v AiStacku)

```yaml
  dabing-separate:  # Demucs htdemucs_ft (CeskyDabing), port 8101
    profiles: [comfy, llm, gemma]
    up: cd ~/deploy/CeskyDabing && docker compose -f deploy/compose.spark.yaml up -d separate && for i in $(seq 60); do curl -sf -m5 -o /dev/null localhost:8101/healthz && exit 0; sleep 5; done; exit 1
    down: cd ~/deploy/CeskyDabing && docker compose -f deploy/compose.spark.yaml stop separate
    detect: '[ "$(docker inspect -f {{.State.Running}} dabing-separate 2>/dev/null)" = true ]'
    mem_gib: 8
    max_jobs: 1
```

`dabing-asr` bude stejný, jen se službou `asr` a portem 8105. Potřebuje `HF_TOKEN`
v `.env` na SPARKu (podmínky pyannote 3.1 odsouhlasené na Hugging Face).

K tomu v AiStacku:
- projekt `dabing` se stropem priority (návrh 30),
- `docker:dabing-separate` do `GUARD_TARGETS` OOM guardu.

`down` je `stop`, ne `down` — kontejner zůstane a příští `up` je rychlý.

## Nasazení kódu a build image

```bash
make deploy-spark     # rsync repa na SPARK
sparkctl run dabing dabing-separate 20 \
  "cd ~/deploy/CeskyDabing && docker compose -f deploy/compose.spark.yaml build separate" \
  "dabing: build separate"
```

## Smoke test modelů (M0)

```bash
sparkctl run dabing <služba> 20 "cd ~/deploy/CeskyDabing && workers/smoke/smoke.sh" "dabing: smoke sm_121"
```

Výsledek: `workers/smoke/results.md` na SPARKu → zkopírovat do `docs/smoke-sm121.md`.

## Běh pipeline

Orchestrátor běží na dev Macu a lease drží `sparkctl exec`:

```bash
sparkctl exec dabing dabing-separate 60 20 "dabing: bluey s01e03" -- \
  bin/dub run bluey s01e03 --until separate
```

`exec` posílá heartbeat, při odsunutí příkaz zastaví a později pustí znovu. Pipeline je
idempotentní, takže přerušený krok se při dalším běhu prostě zopakuje.

Adresa SPARKu je v `.env` (`SPARK_HOST`), viz `.env.example`.
