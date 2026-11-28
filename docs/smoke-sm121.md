# Smoke test modelů na SPARKu (GB10, sm_121)

28. 11. 2026, úloha brokeru `dabing: smoke sm_121`, `SMOKE_LOAD=1`
(`workers/smoke/smoke.sh`, výsledek z `workers/smoke/results.md`).

| model | jede | torch | GPU | proč / poznámka |
|---|---|---|---|---|
| demucs | ✅ | 2.10.0+cu130 | sm_121 | váhy htdemucs_ft stažené a načtené na GPU (57 s) |
| whisperx | ❌ | 2.10.0+cu130 | sm_121 | import projde, ale CTranslate2 4.9.1 (aarch64 wheel z PyPI) nevidí CUDA → Whisper jen na CPU |
| pyannote | ❌ | 2.10.0+cu130 | sm_121 | import projde; váhy nestažené, chybí `HF_TOKEN` (GatedRepoError 401) |
| xtts | ✅ | 2.10.0+cu130 | sm_121 | image z AiStacku |
| moss | – | – | – | netestováno (mimo odsouhlasený seznam modelů) |

## Co z toho plyne

- **Instalace:** whisperx v každé verzi pinuje torch 2.8, proto jde do image `asr`
  s `--no-deps` nad pyannote.audio 4 a torchcodec 0.10 (`workers/asr/requirements.txt`).
  pyannote 3.x na torchaudio 2.10 padá už při importu (`torchaudio.AudioMetaData`).
  torchcodec potřebuje `libpython3.12.so` (balík `libpython3.12t64`).
- **Whisper na CPU:** worker `asr` s `ASR_WHISPER_DEVICE=auto` přepne na CPU a int8
  (20 jader ARM). Rychlost se změří na testovacím dílu. Když bude moc pomalý, další krok je
  CTranslate2 sestavený ze zdroje s CUDA pro aarch64 (vlastní stage v `workers/asr/Dockerfile`).
- **pyannote:** po doplnění `HF_TOKEN` (odsouhlasené podmínky
  `pyannote/speaker-diarization-3.1` a `pyannote/wespeaker-voxceleb-resnet34-LM`) smoke zopakovat
  jen pro `MODELS=pyannote`.
