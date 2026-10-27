# DABING_PLAN.md — Český dabing (handoff pro Opus)

> Lokální AI dabing dětských seriálů EN → CS na domácí infrastruktuře (Mac Mini M2 + DGX Spark + JODA).
> Výstup: MKV s původní stopou + novou českou stopou, konzistentní hlasy postav napříč díly.
> Jen pro osobní použití v rodině.
>
> **Architektura: Go orchestrátor + Python model workery jako služby.** Veškerá logika v Go, Python jen obaluje modely.

## 0. Kontext

- **Repo:** `lioilsources/CeskyDabing` (monorepo: `cmd/dub`, `internal/…`, `workers/…`)
- **Spark** (GB10 Blackwell sm_121, ARM64, 128 GB) — Python workery (Demucs, WhisperX+pyannote, TTS) + vLLM/LiteLLM, co už běží.
- **Mac Mini M2** — Go orchestrátor (darwin/arm64 binárka, launchd služba), SQLite, ffmpeg + rubberband z Homebrew, watch folder, Discord.
- **JODA** — úložiště: `incoming/`, `work/`, `done/`, `voices/`, `series/`. NFS export mountnutý na M2 i Sparku pod **stejnou cestou** `/mnt/dabing`, takže orchestrátor i workery si předávají jen cesty.
- **Stack jako obvykle:** Go, `sqlc` + `golang-migrate`, SQLite, Docker Compose, Caddy. Stejný vzor jako AiStack (gateway + workery).
- **Princip:** každý krok pipeline je idempotentní, výstupy cachované do `work/<serial>/<dil>/`, stav v SQLite, `dub run` přeskakuje hotové kroky a dá se spustit od libovolného kroku.

## 1. Architektura

```
┌──────────── Mac Mini M2 (Go) ──────────────┐     ┌───────── Spark (Python workery) ─────────┐
│ cmd/dub (CLI)                              │     │ workers/separate   Demucs        :8101   │
│ internal/pipeline   stavový stroj kroků    │ HTTP│ workers/asr        WhisperX+pyannote     │
│ internal/cast       embeddingy, matching   │────▶│                    + embeddings  :8102   │
│ internal/translate  LiteLLM client         │     │ workers/tts        MOSS-TTS / XTTS :8103 │
│ internal/fit        délkový loop, stretch  │     │ (LiteLLM + vLLM už běží)         :4000   │
│ internal/media      ffmpeg wrapper         │     └──────────────────────────────────────────┘
│ internal/store      sqlc + SQLite          │
│ internal/watch      watch folder, Discord  │
└────────────────────────────────────────────┘
                 │                                               │
                 └──────────── JODA: NFS /mnt/dabing ────────────┘
```

**Smlouva workerů:** každý worker je stateless FastAPI, `POST /v1/<job>` s cestami k souborům na `/mnt/dabing` (NFS z JODA, stejná cesta na M2 i Sparku), vrací JSON s cestami výstupů + metadata. Fallback, kdyby NFS zlobil: workery přijmou upload a vrátí download URL — Go klient to má za interfacem `Storage`. `GET /healthz`. Žádná logika, žádný stav, žádné rozhodování — to je v Go. OpenAPI spec per worker, Go klient generovaný (oapi-codegen).

## 2. Pipeline (per díl, Go stavový stroj)

| # | Krok | Kde | Vstup → výstup |
|---|---|---|---|
| 1 | `extract` | Go/ffmpeg | mkv → `audio.wav` (48k), `info.json` |
| 2 | `separate` | worker separate | audio → `vocals.wav`, `background.wav` |
| 3 | `transcribe` | worker asr | vocals → `segments.json` (slova, časy, SPK_xx, embedding per segment/cluster) |
| 4 | `identify` | Go cast | clustery → postavy dle `cast.yaml`, nové → `UNKNOWN_n` |
| 5 | `refs` | Go/ffmpeg | per postava 20–30 s čisté reference |
| 6 | `translate` | Go → LiteLLM | EN segmenty → CS s délkovým limitem + glossary |
| 7 | `synthesize` | worker tts | text CS + reference → wav per segment |
| 8 | `fit` | Go | délka vs. slot → re-translate loop (max 2×) → rubberband stretch max ±20 % |
| 9 | `mix` | Go/ffmpeg | dub segmenty + background → `cs.wav`, loudness match (ebur128) |
| 10 | `mux` | Go/ffmpeg | `-c:v copy`, cs stopa default (`language=ces`), orig jako 2., `cs.srt` |

Kroky 7 a 8 se iterují per segment; Go drží frontu segmentů a posílá je na TTS worker paralelně (worker pool, limit dle VRAM).

## 3. Python workery

| Worker | Model | Fallback / poznámka |
|---|---|---|
| separate | Demucs `htdemucs_ft` | — |
| asr | WhisperX large-v3 + pyannote 3.1 + ECAPA/wespeaker embeddingy; vrací i per-cluster embedding a F0 stats | faster-whisper |
| tts | **MOSS-TTS** (čeština, zero-shot klon, duration control) | XTTS v2 (čeština, stabilní). Endpoint stejný, model se volí configem |
| translate | není worker — LiteLLM/vLLM (Qwen3) | Gemma 3, NLLB-200 jako tupý fallback |

**ARM64 pozor:** torch z `nvcr.io/nvidia/pytorch` kontejneru, ne pip. Každý model ověřit na sm_121 v M0. Pokud MOSS nejede, M1 na XTTS v2, MOSS zkusit v M3. Workery v Docker Compose na Sparku, každý vlastní image.

## 4. Hlasy — klíčová část

### 4.1 Tři strategie (postupně)

**A) Cross-lingual klon originálu (default pro M1)**
Reference EN z kroku 5 + text CS → TTS mluví česky barytonem původního herce. Bez katalogu, bez matchingu. Riziko: anglický přízvuk v češtině → změřit v M1 na 3 postavách.

**B) Vlastní katalog českých hlasů + matching (M2)**
Hotový český katalog klonovacích hlasů neexistuje. Banka `voices/`:
- **Common Voice Czech** (CC0, tisíce mluvčích) — hlavní zdroj; vyfiltrovat kvalitní klipy, 1 mluvčí = reference 20–30 s slepená z více klipů.
- **Vlastní nahrávky** (rodina, kamarádi, vlastní hlas).
- **Syntetické** — MOSS-VoiceGenerator (hlas z textového popisu: "hluboký mužský baryton, vřelý, kreslená postava").
- **Piper cs_CZ** jen jako nouzovka.

Každý hlas: `meta.json` — pohlaví, věková skupina, F0 mean/std, ECAPA embedding (192-d), tagy, zdroj+licence. Import do SQLite tabulky `voices` (`dub voices import`).

**Matching EN → CS (jak matchovat baryton) — čistě Go:**
1. Pro postavu z originálu: embedding + F0 stats (dodá asr worker), pohlaví/věk (heuristika z F0, případně LLM z kontextu přepisu).
2. Tvrdý filtr: pohlaví, věková skupina.
3. Skóre = `0.6·cos(emb_orig, emb_cand) + 0.3·(1 − |ΔF0_mean|/60 Hz) + 0.1·překryv tagů`. Speaker embeddingy jsou do značné míry jazykově nezávislé, F0 kotví baryton vs. tenor.
4. `dub cast <series>` vypíše top-5 per postava, `dub cast pick SPK_02 voices/cz_m_042` uloží do `cast.yaml` + DB.

**C) Hybrid (M3)**
Česká reference z katalogu + emoce/styl z originálního segmentu (Chatterbox, IndexTTS-2), nebo voice conversion (Seed-VC/RVC) na výstup TTS — barva blíž originálu, přízvuk český.

### 4.2 Konzistence postav napříč díly
- `series/<name>/cast.yaml` (zdroj pravdy, DB je cache): postava → centroid embeddingu, zvolený hlas (A/B/C), reference, rychlost, poznámky.
- Nový díl: clustery → cosine k centroidům; > 0.75 = známá postava, jinak `UNKNOWN_n` → auto-přiřadit nejbližší hlas z katalogu, aby `dub run` nikdy nezastavil, a nahlásit k potvrzení.
- Centroid se po každém dílu aktualizuje (klouzavý průměr).

### 4.3 Reference klipy
Z `vocals.wav` segmenty jedné postavy bez překryvu, ≥ 3 s, nejvyšší SNR, slepit na 20–30 s, normalizovat → `series/<name>/refs/<postava>.wav`.

## 5. Překlad + časování

- Čeština je ~15–25 % delší. Prompt: segment + sousední kontext + **limit znaků** (délka slotu × ~14 zn/s) + styl ("dětský seriál, přirozená mluvená čeština, oslovení jako v českém dabingu, tykání/vykání dle vztahu") + glossary.
- `series/<name>/glossary.yaml`: jména, místa, hlášky → stabilní napříč díly.
- Fit loop: TTS → délka > 1.2× slotu → LLM "zkrať" (max 2×) → rubberband max ±20 %. Nikdy nekrátit originál.
- **Písničky:** detekce (poměr vocals/other z Demucs + LLM klasifikace textu) → originál + titulky.
- Překryvy mluvčích: dabovat oba, lehký ducking.

## 6. Datový model (SQLite, sqlc)

`series`, `episodes` (stav kroků, cesty, verze modelů), `characters` (cast, centroid BLOB), `voices` (katalog, embedding BLOB, meta), `segments` (text EN/CS, časy, mluvčí, délka TTS, iterace fit), `jobs` (volání workerů, trvání, chyby). Embeddingy jako `[]float32` BLOB, cosine v Go.

## 7. Zabalení do videa (krok 9–10)

1. Dub segmenty poskládat na časovou osu do ticha, přidat `background.wav`, loudness srovnat s originálem (ffmpeg `loudnorm`/ebur128 měření v Go).
2. `ffmpeg -i input.mkv -i cs.wav -map 0:v -map 1:a -map 0:a -map 0:s? -c:v copy -c:s copy -c:a aac -b:a 192k -metadata:s:a:0 language=ces -metadata:s:a:0 title="Český AI dabing" -disposition:a:0 default -metadata:s:a:1 language=eng -disposition:a:1 0 output.mkv`
3. `cs.srt` z přeložených segmentů jako další stopa (`-i cs.srt -map 2:s -metadata:s:s:N language=ces`) nebo vedle souboru. Plex/Jellyfin/VLC pak pustí česky automaticky.

## 8. Milníky

- **M0 — Kostra (1–2 dny):** NFS export z JODA mountnutý na M2 i Sparku jako `/mnt/dabing`; Go repo, CLI `dub` (cobra), migrace, `internal/pipeline` se stavovým strojem, `internal/media` (extract, ffmpeg z brew), worker `separate` v Dockeru na Sparku + OpenAPI + Go klient. `dub run --until separate` na jednom dílu. Ověřit všechny modely na sm_121 (jen import + smoke test), zalogovat výsledek.
- **M1 — End-to-end hrubý dabing (strategie A):** workery asr + tts, překlad přes LiteLLM, fit, mix, mux. 1 díl, 1 série. Kritérium: dítě rozumí a vydrží koukat. Změřit EN přízvuk.
- **M2 — Katalog + matching + cast:** import Common Voice CZ (~200 vybraných mluvčích), skóre, `dub cast`, konzistence napříč 3 díly.
- **M3 — Kvalita:** písničky, překryvy, glossary, fit loop ladění, loudness; A/B s ElevenLabs na 2 min jako laťka; MOSS vs XTTS.
- **M4 — Automatizace:** `dub serve` jako launchd služba na M2 — `internal/watch` sleduje `/mnt/dabing/incoming/<serie>/` → `dub run` → `done/`, Discord notifikace (Ol1nBot), později malé API pro UI.

## 9. Metriky kvality

- Zpětný překlad CS→EN vs. originál (sémantika, přes LLM).
- Cosine hlas originál vs. dabing (A) / vs. reference (B).
- % segmentů bez stretche > 10 %.
- Subjektivně: 5 min s dětmi, 1–5.

## 10. Otevřené otázky

- MOSS-TTS na ARM64/sm_121 — jede? (rozhodne M0)
- Míra EN přízvuku u A → je B nutnost, nebo nice-to-have?
- Propustnost NFS z JODA pro `-c:v copy` mux velkých MKV — pokud bude M2 ↔ JODA pomalé, mux dělat na JODA (ssh) nebo stage do lokálního disku M2.
- Diarizace u krátkých dětských replik — min. délka segmentu, ruční korekce v `dub cast`.
- Klonovat hlasy skutečných herců (A) jen doma; pro cokoli veřejného default B.

## 11. První úkol pro Opus

1. Založit repo: `cmd/dub`, `internal/{pipeline,media,store,workers}`, `workers/separate`, `deploy/compose.spark.yaml`, `deploy/launchd/com.ol1n.dub.plist`, migrace, sqlc config. Ověřit NFS mount `/mnt/dabing` na M2 a Sparku.
2. Worker `separate`: FastAPI + Demucs, Dockerfile z `nvcr.io/nvidia/pytorch`, OpenAPI spec, Go klient.
3. `dub add <series> <input.mkv>` + `dub run --until separate` na jednom dílu, stav v SQLite, výstupy v `work/`.
4. Smoke test importů WhisperX, pyannote, XTTS, MOSS-TTS na Sparku — jen tabulka "jede / nejede / proč".
5. Zastavit se a ukázat výsledek — pak M1.
