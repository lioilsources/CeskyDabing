# 02 — Handoff na MacBook

> Pokračování práce z M2 na MacBooku (stejná LAN). Cíl a architektura: [00-DABING_PLAN.md](00-DABING_PLAN.md),
> postup a fáze: [01-PLAN-M0-dokonceni-M1.md](01-PLAN-M0-dokonceni-M1.md). Stav k 22. 11. 2026.
> Infra fakta (adresy, porty, pasti SPARKu a MacBooku) jsou v `~/.claude/shared/` — ne tady, repo je veřejné.

## 1. Stav

**Hotovo a mergnuté v `main`** (PR #1–#10):

- CLI `dub` (`add`, `run`, `status`, `doctor`, `service plist`), stavový stroj se všemi 10 kroky:
  extract → separate → transcribe → identify → refs → translate → synthesize → fit → mix → mux.
- Workery `separate` (Demucs) a `asr` (WhisperX + pyannote) s OpenAPI smlouvou a backendem
  `passthrough`; společný kód `workers/common/`.
- TTS přes službu `audio` z AiStacku (XTTS-v2), překlad přes LLM gateway AiStacku.
- `make gate`, testy bez sítě. End-to-end na M2: workery v `passthrough`, falešné LLM a audio
  služba, skutečné ffmpeg + rubberband → MKV s českou stopou jako výchozí.
- AiStack #46 (doplněk brokeru `dabing-separate`, projekt `dabing` se stropem 30, OOM guard)
  je mergnutý.

**Neověřeno — nic z toho zatím neběželo na SPARKu:**

- image `dabing/separate` a `dabing/asr` nejsou postavené, smoke test modelů na sm_121 neproběhl;
- skutečné modely (Demucs, WhisperX/CTranslate2 na aarch64, pyannote, XTTS přes `audio`);
- délka a paměť na celém 22minutovém dílu.

**Blokery (stav 22. 11.):**

1. **SPARK nedostupný** — broker ani ssh neodpovídají (connection refused). Zjistit, jestli
   restartuje nebo spadl (`spark-gotchas.md`, sekce „Když SPARK zmizí“). Bez něj nejde nic z §3.
2. **Config brokeru na SPARKu:** položky z AiStack #46 (`dabing-separate`, `dabing: {max: 30}`)
   přenést do `~/spark-broker/config.yaml`, broker restartovat, nasadit `spark-oom-guard.env`.
   Dělá Olin. Ověření: `sparkctl catalog` obsahuje `dabing-separate`.
3. **Rozhodnutí vlastníka (Olin):**
   - souhlas se stažením modelů (Demucs htdemucs_ft, Whisper large-v3, pyannote 3.1, wespeaker) —
     desítky GB na SPARKu;
   - `HF_TOKEN` s odsouhlasenými podmínkami pyannote 3.1 (jinak bez diarizace);
   - testovací díl: jedno EN MKV a jméno seriálu;
   - `DUB_TTS_VOICE_RIGHTS` / `DUB_TTS_VOICE_SOURCE` — prohlášení o právech k hlasu, které služba
     `audio` vyžaduje ke klonu. `dub` ho nikdy nedoplňuje sám (`docs/tts.md`). Stačí před `synthesize`.
4. **NFS `/mnt/dabing`** neexistuje nikde. Neblokuje: jede se v režimu `upload` (viz §2).
5. Na SPARKu v `~/deploy/CeskyDabing/` zůstal omylem zkopírovaný macOS `.venv` (z doby před #10).
   Smazat má Olin; agent na cílovém stroji nemaže.

## 2. Příprava MacBooku

```bash
cd "$DEV_ROOT" && gh repo clone lioilsources/CeskyDabing && cd CeskyDabing
brew install go sqlc ffmpeg rubberband
make gate                                   # musí projít čistě
cp .env.example .env                        # SPARK_HOST a další; .env se necommituje
```

Do `.env` na MacBooku:

```bash
SPARK_HOST=<hostname SPARKu z infra.md>
DUB_ROOT=<lokální adresář s incoming/ work/ done/ voices/ series/>   # dokud není NFS
DUB_SEPARATE_STORAGE=upload
DUB_ASR_STORAGE=upload
```

- **Kde běží orchestrátor:** stav `dub` je v lokální SQLite (`~/.local/share/dub/dub.db`), ne
  v repu. Na M2 je jen testovací DB ve scratchpadu, nic se nepřenáší — MacBook začíná čistě.
  Orchestrátor pouštěj jen z jednoho stroje, jinak se stavy rozejdou.
- **Síť:** MacBook je jen na Wi-Fi (USB Ethernet adaptér se nesmí zapojovat, shazuje switch —
  `infra.md`). Režim `upload` posílá WAV dílu (~250 MB) na SPARK a zpět; po Wi-Fi to bude pomalé.
  Až bude NFS, přepnout `*_STORAGE` na `shared`.
- **`sparkctl`** musí na MacBooku fungovat (`sparkctl status`); MacBook je v `infra.md` mezi
  stroji, které smí brokeru posílat úlohy.
- Lokální venv pro `passthrough` workery: `.venv` v repu (`README.md`, „Worker lokálně“).

## 3. Další kroky (v pořadí)

Odpovídá fázím B4 a C8 plánu 01. Na SPARKu nic mimo broker — build, smoke i workery jsou úlohy.

1. `sparkctl status` a `sparkctl catalog` — SPARK žije, `dabing-separate` je v katalogu.
2. `make deploy-spark` (rsync repa, bez `.venv` a `.env`).
3. Build image přes broker (`docs/spark.md`, „Nasazení kódu a build image“): `separate`, pak `asr`.
4. Smoke test: `workers/smoke/smoke.sh` jako `sparkctl run` → výsledek do `docs/smoke-sm121.md`
   (tabulka jede / nejede / proč + datum). Hlavně: Demucs na torch 2.10 cu130, CTranslate2 CUDA
   na aarch64 (když ne, Whisper poběží na CPU), pyannote 3.1.
5. Doplněk `dabing-asr` do AiStacku (stejný vzor jako `dabing-separate`, port 8105, změřené
   `mem_gib`); `mem_gib` u `dabing-separate` opravit podle měření. PR do AiStacku.
6. Testovací díl krok po kroku, vždy přes `sparkctl exec` s potřebnými službami:
   - `--until separate` (`dabing-separate`) — poslechnout `vocals.wav` a `background.wav`;
   - `--until refs` (`dabing-asr`) — zkontrolovat `cast.yaml`, přejmenovat postavy;
   - `--until translate` (`qwen36`) — `jq '.[] | select(.chars > .max_chars)' translate.json`;
   - `--until mux` (`tts`, `qwen36`) — až s prohlášením o právech k hlasu.
7. `docs/m1-vysledky.md`: časy kroků, paměť na SPARKu, % segmentů se stretchem > 10 %
   (`fit.json`), hlasitost (`mix.json`), zpětný překlad na vzorku, subjektivně přízvuk na
   3 postavách. **Tady se zastavit a ukázat výsledek** — rozhodne o strategii B (M2).

## 4. Pravidla, která tu platí

- `make gate` před každým pushem a s **jeho skutečným exit kódem** — nikdy `make gate | tail && git push`.
- `git add` jmenovitě, commit zprávy anglicky, PR texty česky, o merge rozhoduje Olin.
- Komentáře v kódu anglicky, CLI hlášky a docs česky. Ve veřejném repu žádné LAN IP ani domovské cesty.
- Modely stahovat jen se souhlasem; nové infra fakty (porty, služby brokeru) do `~/.claude/shared/`
  přes PR do DevConventions.
