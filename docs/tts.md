# Syntéza řeči (krok synthesize)

M1 používá službu `audio` z AiStacku na SPARKu (port 8093, `/v1/audio/tts`) s enginem
XTTS-v2, který umí češtinu a klonování hlasu podle reference. Vlastní TTS worker zatím není
potřeba. MOSS-TTS přijde na řadu, až projde smoke testem na sm_121 (M3).

## Jak to běží

1. Pro každou postavu se reference `series/<serial>/refs/<postava>.wav` jednou uloží do
   služby jako hlas `dub-<serial>-<postava>-<hash>`. Nová reference = nový hlas.
2. Každá replika: `POST /v1/audio/tts` s tímhle hlasem, `commercial_only=false` (XTTS-v2 je
   nekomerční, pro rodinné použití v pořádku), výsledek se stáhne do `work/…/tts/NNNN-<hash>.wav`.
3. Paralelně běží `tts.parallel` replik (výchozí 2). Hotové repliky se stejným textem a hlasem
   se při dalším běhu nepočítají znovu.

Lease: `sparkctl exec dabing tts …` (služba `tts` je v profilech `comfy` a `llm`).

## Prohlášení o právech k hlasu

Služba hlas bez prohlášení neuloží: `rights` musí být `own`, `consented`, `licensed`
nebo `synthetic` a `source` popisuje, kdo mluví a odkud vzorek je. `dub` tyhle hodnoty
**nikdy nedoplňuje sám** — jdou jen z `DUB_TTS_VOICE_RIGHTS` a `DUB_TTS_VOICE_SOURCE`
(nebo `tts.voice_rights` / `voice_source` v `dub.yaml`). Bez nich krok skončí chybou dřív,
než cokoli nahraje.

U strategie A (klon hlasu původního herce z dílu) je to rozhodnutí vlastníka; plán 00
počítá s klonováním skutečných herců jen pro domácí použití. U strategie B (katalog
z Common Voice CC0, vlastní nahrávky se svolením) jsou hodnoty jasné: `licensed`
nebo `consented`.
