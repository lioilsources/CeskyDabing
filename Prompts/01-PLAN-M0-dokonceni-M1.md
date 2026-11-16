# 01 — Dokončení M0 a M1 (handoff pro Opus)

> Navazuje na [00-DABING_PLAN.md](00-DABING_PLAN.md). Ten zůstává zdrojem pravdy pro cíl
> a architekturu; tenhle soubor říká, co je hotové, co se od té doby změnilo a v jakém
> pořadí pokračovat. Stav ověřen 12. 11. 2026.
>
> Infra fakta (adresy, porty, pasti SPARKu) jsou v `~/.claude/shared/` (`infra.md`,
> `ports.md`, `spark-gotchas.md`). Přečti je před fází B. Do tohohle repa nepatří —
> repo je veřejné.

## 0. Kde jsme

**Hotovo a otestováno lokálně** (commit `2ae21b8`, worker v režimu `passthrough` na Macu):

- CLI `dub`: `add`, `run --from/--until/--force`, `status`, `doctor`.
- Stavový stroj pipeline (10 kroků, implementované `extract` a `separate`), přeskakování
  hotových kroků, zneplatnění pozdějších kroků, log volání workerů v tabulce `jobs`.
- Celý datový model z §6 (migrace + sqlc).
- Worker `separate` (FastAPI + Demucs), OpenAPI spec, generovaný Go klient.
- Storage `shared` (NFS, cesty) i `upload` (fallback bez NFS).
- Smoke test modelů `workers/smoke/` — napsaný, nespuštěný.

**Neproběhlo:**

- Na SPARKu se nic nestavělo ani nespouštělo. Image `dabing/separate` neexistuje,
  Demucs na sm_121 není ověřený, smoke tabulka není.
- Sdílené úložiště `/mnt/dabing` neexistuje na žádném stroji.
- Pipeline neběžela na skutečném dílu.

**Co se od plánu 00 změnilo** (důvod, proč nejde jen pokračovat):

1. Porty 8102 a 8103 na SPARKu jsou obsazené (AiStack TTS), 8100 je broker. Plán 00
   s nimi počítal pro asr a tts.
2. SPARK řídí `spark-broker`. Služba je položka v jeho configu (`up`, `down`, `detect`,
   `mem_gib`, profily). Vlastní `docker compose up` mimo broker je zakázaný, takže
   workery dabingu se musí stát službami brokeru.
3. Broker už nabízí `tts` (XTTS v2 / Chatterbox z AiStacku) a `asr` (Whisper turbo přes
   LiteLLM, bez diarizace a bez časů slov).
4. Datasety na JODĚ mají konvenci `pool/<x>` s mountpointem `~/pool-<x>`; plán v
   `docs/nfs.md` navrhoval `/pool/dabing`.
5. Na SPARK nepatří always-on služby a M2 má málo paměti (žádný Docker na M2).
6. Platí sdílená pravidla: komentáře v kódu anglicky, commit zprávy anglicky, ve veřejném
   repu žádné LAN IP ani domovské cesty, `git add` jmenovitě, jeden požadavek = jeden PR.

## 1. Rozhodnutí vlastníka (ptej se jen na tohle, zbytek dělej)

| # | Otázka | Výchozí návrh |
|---|---|---|
| R1 | Commit `2ae21b8` je veřejný a obsahuje LAN IP (`docs/nfs.md`) a domovskou cestu (launchd plist). Historii nejde přepsat bez force pushe. | Opravit dopředu, historii nechat. |
| R2 | NFS chce sudo na JODĚ, SPARKu i M2 (na M2 i restart). Dělá ho Olin. | Do té doby `storage: upload`. |
| R3 | Služby dabingu v brokeru + strop priority projektu `dabing`. Je to PR do AiStacku. | Strop 30, služby viz fáze B. |
| R4 | `HF_TOKEN` s odsouhlasenými podmínkami pyannote 3.1. | Bez něj není diarizace. |
| R5 | Testovací díl (skutečné EN MKV) a jméno seriálu. | — |
| R6 | TTS pro M1: služba `tts` z AiStacku, nebo vlastní worker? | Viz C5, rozhodni po ověření API. |

Na R2–R5 nečekej s fází A. Fáze B stojí na R3, fáze C na R4 a R5.

## 2. Fáze A — úklid repa podle pravidel (1 PR, bez závislostí)

1. **Únik ve veřejném repu.** Z `docs/nfs.md` odstraň IP adresy a příkazy se sudo; nech
   jen popis, co úložiště musí splňovat, a odkaz „konkrétní příkazy má vlastník“. Hosty
   jen jako `joda` / `spark`. Launchd plist: žádná `/Users/…` cesta — vygeneruj ho
   příkazem `dub service install` (šablona + `os.UserHomeDir()`), v repu nech šablonu.
   Projdi i `dub.example.yaml`, README, compose a tenhle adresář `Prompts/`.
2. **Adresy přes konfiguraci.** `.env.example` s `SPARK_HOST`, `DUB_ROOT` apod., hodnoty
   v gitignorovaném `.env`. Výchozí URL workerů v `internal/config` bez pevné adresy.
3. **Komentáře anglicky.** Přepiš české komentáře v Go, Pythonu, SQL, Dockerfilech,
   compose a shellu. Uživatelské texty CLI, chybové hlášky a README zůstávají česky.
4. **`CLAUDE.md` repa** (≤ 80 řádků): gate, struktura, odchylky. Použij skill
   `bootstrap-repo` nebo `repo-hygiene`, ať sedí i pre-commit hook proti únikům.
5. **Gate** do `Makefile` jako `make gate`: `gofmt -l`, `go vet ./...`, `go test ./...`,
   `python3 -m py_compile` na workery, kontrola, že `make generate` nic nezmění.
6. **Běhová data** do gitignorovaného `run/` (logy, PID). `bin/dub` zůstává build výstup.
7. Test na `internal/config` a `internal/store` (migrace nahoru a dolů), teď jsou bez testů.

Hotovo, když: `make gate` projde, `repo-auditor` nenajde únik, PR je otevřený.

## 3. Fáze B — SPARK přes broker, dokončení M0

### B1. Úložiště (R2)

- Cíl: jeden adresář viditelný na M2 i SPARKu pod **stejnou cestou** `/mnt/dabing`,
  zapisovatelný z obou stran pod jedním vlastníkem, s podadresáři
  `incoming work done voices series`.
- JODA: dataset `pool/dabing` podle konvence (lz4, `atime=off`, mountpoint `~/pool-dabing`).
  Snap docker na JODĚ vidí jen `/home`, `/media`, `/mnt` — proto ne `/pool/dabing`.
- Příkazy pro Olina připrav do zprávy v chatu, ne do repa. Na M2 je potřeba
  `synthetic.conf` + restart + autofs.
- Dokud to není: `DUB_SEPARATE_STORAGE=upload`. Fallback je otestovaný a pro M1 stačí;
  NFS neblokuje nic kromě pohodlí a rychlosti.
- Ověření: `dub doctor` (`fs=nfs`, žádné chybějící adresáře).

### B2. Workery jako služby brokeru (R3)

PR do AiStacku (`services/spark-broker/deploy/config…` + Makefile cíle), vzor je služba `asr`:

- Služby `dabing-separate`, později `dabing-asr`, `dabing-tts`. Každá má `up` (compose
  z `~/deploy/CeskyDabing`), `down`, `detect`, `mem_gib` (změř, neodhaduj) a `max_jobs`.
- Profily: aspoň `llm`, aby šel překlad (`qwen36`) a workery v jednom stage. Ověř
  paměťový rozpočet proti `spark-gotchas.md` — unified paměť, počítá se `MemFree`.
- Projekt `dabing` se stropem priority (R3).
- Nové GPU kontejnery přidej do `GUARD_TARGETS` OOM guardu.
- **Porty:** 8101 je volný, 8102–8104 ne. Před volbou zkontroluj `ports.md`, nové porty
  tam doplň (PR do DevConventions). Orchestrátor běží na M2, takže port musí být
  dostupný z LAN; adresa jde z `.env`.
- Workery nejsou always-on: `restart: "no"`, model se po nečinnosti uvolní (už umí).
- Nasazení kódu: `make deploy-spark` (rsync do `~/deploy/CeskyDabing`). Build image jako
  úloha brokeru, ne ručně.

### B3. Orchestrátor a lease

- Nejjednodušší cesta, začni jí: `sparkctl exec dabing <služby> <min> "<název>" -- bin/dub run …`.
  `exec` obstará heartbeat i odsunutí; při odsunutí příkaz zabije a později pustí znovu.
  Pipeline je idempotentní, takže to sedí — ověř testem, že `SIGTERM` uprostřed kroku
  nenechá krok ve stavu `running` (dnes by zůstal; po startu ho ber jako nedokončený).
- Úlohy delší než 10 minut na Macu přes `nohup` + `disown`, log a PID do `run/`.
- Vlastní Go klient brokeru (`internal/spark`) až v M4 pro `dub serve`.
- Na konci metriky: `sparkctl done` čte poslední řádek výstupu jako JSON — `dub run`
  ať ho umí vypsat (`--metrics-json`: počet dílů, segmentů, selhání).

### B4. Smoke test a první skutečný běh

1. `workers/smoke/smoke.sh` přes `sparkctl run`. Výsledek ulož jako `docs/smoke-sm121.md`
   (tabulka jede / nejede / proč + datum). Sleduj hlavně:
   - Demucs s torch 2.10 cu130 (demucs je instalovaný `--no-deps`).
   - WhisperX: jestli CTranslate2 na aarch64 vidí CUDA. Když ne, ASR jen na CPU →
     fallback na Whisper přes transformers/torch, nebo na službu `asr` z AiStacku.
   - MOSS-TTS: jeho runtime pinuje torch 2.9.1 cu128, smoke ho zkouší s 2.10 cu130.
   - pyannote 3.1 s torch 2.10.
2. `dub run <serial> --until separate` na skutečném dílu (R5). Poslechni `vocals.wav`
   a `background.wav`, zapiš čas separace a špičku paměti do `docs/`.
3. Tím je M0 hotové. **Nezastavuj se, pokračuj fází C** a krátce to ohlas.

## 4. Fáze C — M1: hrubý dabing end-to-end (strategie A)

Jeden díl, jedna série. Kritérium z plánu 00: dítě rozumí a vydrží koukat. Každý krok =
samostatný PR, vždy s testem na fake kroku nebo `passthrough` backendu, aby šla celá
pipeline pustit na Macu bez GPU.

### C1. `transcribe` — worker `asr`

- `POST /v1/transcribe {audio_path, output_dir, language?, min_speakers?, max_speakers?}`
  → `segments.json`: segmenty s `start`, `end`, `text`, `words[]`, `speaker` (`SPK_xx`)
  a per-speaker `embedding` (192-d) + `f0_mean`, `f0_std`, celková délka řeči.
- Vstup je `vocals.wav`, ne původní audio.
- Stejná kostra jako `separate`: OpenAPI spec je zdroj pravdy, Go klient generovaný,
  souborové API `/v1/files`, backend `passthrough` vracející pevný přepis.
- Společný kód workerů (cesty, scratch, idle unload, chyby) vytáhni do `workers/common/`,
  ať se nekopíruje potřetí.
- Go krok zapíše segmenty do tabulky `segments` a sloučí příliš krátké repliky
  (otevřená otázka z plánu 00: min. délka segmentu — začni na 0,4 s a změř).

### C2. `identify` — minimální verze

- V M1 bez katalogu: každý `SPK_xx` = postava `UNKNOWN_n`, centroid se uloží do
  `characters` a do `series/<name>/cast.yaml` (YAML je zdroj pravdy, DB cache).
- Cosine a práce s `[]float32` BLOB patří do `internal/cast` už teď; matching proti
  katalogu a `dub cast` je M2.

### C3. `refs`

- Z `vocals.wav` segmenty jedné postavy bez překryvu, ≥ 3 s, seřazené podle SNR,
  slepit na 20–30 s, normalizovat → `series/<name>/refs/<postava>.wav`.
- Postava s méně než ~6 s čisté řeči: použij, co je, a nahlas to ve `status`.

### C4. `translate`

- LLM přes gateway AiStacku (ne přímo LiteLLM, ten z LAN není dostupný). Model a alias
  zjisti ze `sparkctl catalog`; v odpovědi kontroluj pole `model` — řetěz umí tiše
  přepadnout na malý fallback.
- Prompt podle §5 plánu 00: segment + sousední kontext + limit znaků (délka slotu ×
  ~14 zn/s) + styl + `glossary.yaml`. Výstup strukturovaně (JSON), dávkuj po scénách.
- `internal/translate`: klient s rozhraním, fake implementace pro testy. Volání loguj
  do `jobs`.
- Glossary v M1 jen načítej, když existuje; plnění je M3.

### C5. `synthesize` — worker `tts` (R6)

- Nejdřív ověř, co umí služba `tts` z AiStacku (XTTS v2: čeština, referenční WAV,
  dostupnost z M2, licence — XTTS je nekomerční, pro rodinné použití v pořádku).
  Když stačí, napiš v Go adaptér a vlastní worker odlož; ušetří to image i paměť.
- Jinak vlastní `workers/tts` se stejnou smlouvou pro XTTS i MOSS (model volí config):
  `POST /v1/synthesize {text, language, reference_path, output_path, speed?, target_duration_s?}`
  → cesta, délka, sample rate.
- MOSS-TTS jen pokud prošel smoke; jinak M1 na XTTS a MOSS až v M3.
- Go drží frontu segmentů a worker pool s limitem (začni na 1–2 souběžných, změř paměť).
  Stav per segment v `segments` (`tts_path`, `tts_duration`), takže přerušený běh
  pokračuje od nehotových segmentů.

### C6. `fit`

- Délka TTS vs. slot: > 1,2× → LLM „zkrať“ (max 2×) → `rubberband` max ±20 %.
- Slot smí přetéct do ticha za replikou, pokud nenarazí na další repliku téže stopy.
- Ulož `stretch` a `fit_iter`; z nich se počítá metrika „% segmentů bez stretche > 10 %“.

### C7. `mix` a `mux`

- Podle §7 plánu 00: segmenty na časovou osu, `background.wav`, loudness na úroveň
  originálu (ebur128 měření), pak mux s `-c:v copy`, česká stopa výchozí, `cs.srt`.
- Výstup do `done/<serial>/`. Mux přes NFS může být pomalý → když je, dělej ho do
  lokálního dočasného adresáře na M2 a hotový soubor přesuň.

### C8. Vyhodnocení M1

Zapiš do `docs/m1-vysledky.md`: časy kroků, paměť na SPARKu, cosine hlas originál vs.
dabing, % segmentů se stretchem > 10 %, zpětný překlad CS→EN na vzorku, subjektivně
míru anglického přízvuku na 3 postavách. Z toho vyplyne, jestli je strategie B (M2)
nutnost. Tady se zastav a ukaž výsledek.

## 5. Pravidla práce

- Před každým pushem `make gate`. Rozbitý build nepushuj.
- `git add` jmenovitě. Commit zprávy anglicky, PR texty česky. O merge rozhoduje Olin.
- Na SPARKu nic mimo broker, žádné sudo, modely stahuj jen se souhlasem (Demucs,
  Whisper large-v3, pyannote, XTTS/MOSS jsou jednotky až desítky GB — zeptej se jednou
  na celý seznam).
- Nové infra fakta (porty, služby brokeru, dataset) patří do `~/.claude/shared/` přes
  PR do DevConventions, ne do tohohle repa a ne do paměti.
- Po milníku pokračuj. Stůj jen u R1–R6, u nevratných věcí a u chybějícího vstupu.

## 6. Pořadí a závislosti

```
A (úklid) ──────────────────────────────► PR hned
B1 úložiště ── R2 (Olin, sudo) ·········· neblokuje, běží fallback upload
B2 broker ──── R3 ──► B3 lease ──► B4 smoke + reálný díl (R5) ──► M0 hotovo
C1 asr (R4) ─► C2 ─► C3 ─► C4 ─► C5 (R6) ─► C6 ─► C7 ─► C8 ──► M1 hotovo
```

Kroky C2, C3, C4, C6 a C7 jsou čisté Go a dají se psát a testovat na Macu souběžně
s čekáním na B2. Nenech se zablokovat SPARKem.
