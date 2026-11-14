"""Worker asr — WhisperX transcription, word alignment and pyannote diarization.

POST /v1/transcribe takes vocals.wav on the shared storage and writes
segments.json (schema Transcript in ../openapi.yaml): segments with words and a
speaker id, and per speaker an embedding plus F0 statistics. No pipeline logic.

Environment (plus the ASR_* variables of dabing_worker):
  ASR_BACKEND        whisperx | passthrough (passthrough = no model, for tests)
  ASR_MODEL          Whisper model, default large-v3
  ASR_BATCH_SIZE     transcription batch size (16)
  ASR_WHISPER_DEVICE auto | cuda | cpu — CTranslate2 aarch64 wheels may be CPU-only
  HF_TOKEN           Hugging Face token with the pyannote 3.1 terms accepted
"""

from __future__ import annotations

import os
import time
from typing import Any

import numpy as np
from fastapi import HTTPException
from pydantic import BaseModel, Field

from dabing_worker import Worker, write_json

BACKEND = os.environ.get("ASR_BACKEND", "whisperx")
MODEL = os.environ.get("ASR_MODEL", "large-v3")
BATCH_SIZE = int(os.environ.get("ASR_BATCH_SIZE", "16"))
SR = 16000  # WhisperX and pyannote both work at 16 kHz
F0_MAX_S = 60.0  # seconds of speech per speaker used for F0 statistics

worker = Worker("asr", "ASR")
log = worker.log
app = worker.app


class TranscribeRequest(BaseModel):
    audio_path: str
    output_dir: str
    language: str | None = None
    min_speakers: int | None = Field(default=None, ge=1)
    max_speakers: int | None = Field(default=None, ge=1)


class TranscribeResult(BaseModel):
    segments_path: str
    language: str
    duration_s: float
    segments: int
    speakers: int
    model: str
    device: str
    elapsed_s: float


class Health(BaseModel):
    status: str
    backend: str
    model: str
    device: str
    loaded: bool
    torch: str | None = None


# --------------------------------------------------------------------------- helpers


def speaker_id(label: str | None) -> str:
    """pyannote "SPEAKER_03" → "SPK_03"."""
    if not label:
        return ""
    return "SPK_" + label.rsplit("_", 1)[-1].zfill(2)


def normalise(v: np.ndarray) -> list[float]:
    n = float(np.linalg.norm(v))
    return [float(x) for x in (v / n if n > 0 else v)]


def speaker_audio(audio: np.ndarray, segments: list[dict], spk: str, limit_s: float) -> np.ndarray:
    """Concatenate one speaker's segments, longest first, at most limit_s seconds."""
    parts, total = [], 0.0
    for s in sorted((s for s in segments if s["speaker"] == spk), key=lambda s: s["start"] - s["end"]):
        a = audio[int(s["start"] * SR): int(s["end"] * SR)]
        parts.append(a)
        total += len(a) / SR
        if total >= limit_s:
            break
    return np.concatenate(parts) if parts else np.zeros(0, dtype=np.float32)


def f0_stats(a: np.ndarray) -> tuple[float | None, float | None]:
    if len(a) < SR // 2:
        return None, None
    import librosa

    # Kids' cartoons: from deep adult voices up to squeaky characters.
    f0, voiced, _ = librosa.pyin(a, fmin=60, fmax=700, sr=SR, frame_length=1024)
    f0 = f0[voiced & np.isfinite(f0)]
    if f0.size == 0:
        return None, None
    return float(np.mean(f0)), float(np.std(f0))


def build_speakers(audio: np.ndarray, segments: list[dict], embeddings: dict[str, np.ndarray],
                   f0: dict[str, tuple[float, float]] | None = None) -> list[dict]:
    out = []
    for spk in sorted({s["speaker"] for s in segments if s["speaker"]}):
        speech = sum(s["end"] - s["start"] for s in segments if s["speaker"] == spk)
        f0_mean, f0_std = f0[spk] if f0 else f0_stats(speaker_audio(audio, segments, spk, F0_MAX_S))
        emb = embeddings.get(spk)
        out.append({
            "id": spk, "speech_s": round(speech, 3),
            "embedding": normalise(emb) if emb is not None else [],
            "f0_mean": f0_mean, "f0_std": f0_std,
        })
    return out


# --------------------------------------------------------------------------- backends


class Passthrough:
    """No model: a fixed transcript of alternating speakers. For pipeline tests only."""

    device = "cpu"
    torch_version = None

    def loaded(self) -> bool:
        return True

    def unload_if_idle(self, idle_s: float) -> None:
        pass

    def transcribe(self, audio: np.ndarray, language: str, min_spk: int | None, max_spk: int | None) -> dict:
        dur = len(audio) / SR
        segments, t, i = [], 0.2, 0
        while t + 1.5 <= dur:
            end = min(t + 4.0, dur)
            text = f"Line number {i}."
            words, w0 = [], t
            for w in text.split():
                words.append({"word": w, "start": round(w0, 3), "end": round(w0 + 0.3, 3), "score": 1.0})
                w0 += 0.35
            segments.append({"start": round(t, 3), "end": round(end, 3), "text": text,
                             "speaker": f"SPK_{i % 2:02d}", "words": words})
            t, i = end + 0.5, i + 1
        rng = np.random.default_rng(7)
        embeddings = {f"SPK_{k:02d}": rng.standard_normal(192) for k in range(2)}
        # pyin on a test tone is meaningless; fixed values keep tests stable.
        f0 = {"SPK_00": (110.0, 15.0), "SPK_01": (240.0, 30.0)}
        speakers = build_speakers(audio, segments, embeddings, f0)
        return {"language": language, "segments": segments, "speakers": speakers}


class WhisperX:
    def __init__(self) -> None:
        import torch

        self.torch = torch
        self.torch_version = torch.__version__
        self.device = "cuda" if torch.cuda.is_available() else "cpu"
        self.whisper_device = self._whisper_device()
        self.models: dict[str, Any] = {}
        self.last_used = time.monotonic()

    def _whisper_device(self) -> str:
        want = os.environ.get("ASR_WHISPER_DEVICE", "auto")
        if want != "auto":
            return want
        import ctranslate2

        if self.device == "cuda" and ctranslate2.get_cuda_device_count() > 0:
            return "cuda"
        log.warning("CTranslate2 %s does not see CUDA, Whisper runs on CPU", ctranslate2.__version__)
        return "cpu"

    def loaded(self) -> bool:
        return bool(self.models)

    def unload_if_idle(self, idle_s: float) -> None:
        if self.models and idle_s > 0 and time.monotonic() - self.last_used > idle_s:
            log.info("releasing the models after %.0f s idle", idle_s)
            self.models = {}
            if self.device == "cuda":
                self.torch.cuda.empty_cache()

    def _get(self, key: str, load):
        if key not in self.models:
            log.info("loading %s", key)
            self.models[key] = load()
        return self.models[key]

    def transcribe(self, audio: np.ndarray, language: str, min_spk: int | None, max_spk: int | None) -> dict:
        import whisperx

        compute = "float16" if self.whisper_device == "cuda" else "int8"
        asr = self._get(f"whisper:{MODEL}", lambda: whisperx.load_model(
            MODEL, self.whisper_device, compute_type=compute, language=language))
        result = asr.transcribe(audio, batch_size=BATCH_SIZE, language=language)

        align_model, meta = self._get(f"align:{language}", lambda: whisperx.load_align_model(
            language_code=language, device=self.device))
        result = whisperx.align(result["segments"], align_model, meta, audio, self.device,
                                return_char_alignments=False)

        diarize = self._get("diarize", self._diarization_pipeline)
        try:
            dia, raw_emb = diarize(audio, min_speakers=min_spk, max_speakers=max_spk, return_embeddings=True)
        except TypeError:  # older whisperx without speaker embeddings
            dia, raw_emb = diarize(audio, min_speakers=min_spk, max_speakers=max_spk), None
        result = whisperx.assign_word_speakers(dia, result)

        segments = []
        for s in result["segments"]:
            words = [{k: w[k] for k in ("word", "start", "end", "score") if k in w} for w in s.get("words", [])]
            segments.append({"start": float(s["start"]), "end": float(s["end"]), "text": s["text"].strip(),
                             "speaker": speaker_id(s.get("speaker")), "words": words})

        embeddings = {speaker_id(k): np.asarray(v, dtype=np.float32) for k, v in (raw_emb or {}).items()}
        missing = {s["speaker"] for s in segments if s["speaker"]} - embeddings.keys()
        if missing:
            embeddings.update(self._embed(audio, segments, missing))
        self.last_used = time.monotonic()
        return {"language": language, "segments": segments, "speakers": build_speakers(audio, segments, embeddings)}

    def _diarization_pipeline(self):
        try:
            from whisperx.diarize import DiarizationPipeline
        except ImportError:
            from whisperx import DiarizationPipeline
        return DiarizationPipeline(use_auth_token=os.environ.get("HF_TOKEN"), device=self.device)

    def _embed(self, audio: np.ndarray, segments: list[dict], speakers: set[str]) -> dict[str, np.ndarray]:
        from pyannote.audio import Inference, Model

        model = self._get("embedding", lambda: Model.from_pretrained(
            "pyannote/wespeaker-voxceleb-resnet34-LM", use_auth_token=os.environ.get("HF_TOKEN")))
        infer = Inference(model, window="whole", device=self.torch.device(self.device))
        out = {}
        for spk in speakers:
            a = speaker_audio(audio, segments, spk, 30.0)
            if len(a) >= SR:
                wav = self.torch.from_numpy(a)[None]
                out[spk] = np.asarray(infer({"waveform": wav, "sample_rate": SR}), dtype=np.float32)
        return out


backend = Passthrough() if BACKEND == "passthrough" else WhisperX()
worker.unload_when_idle(backend)


def load_audio(path) -> np.ndarray:
    """Mono float32 at 16 kHz (ffmpeg, like whisperx.load_audio)."""
    import subprocess

    cmd = ["ffmpeg", "-nostdin", "-loglevel", "error", "-i", str(path), "-f", "s16le", "-ac", "1", "-ar", str(SR), "-"]
    try:
        raw = subprocess.run(cmd, capture_output=True, check=True).stdout
    except subprocess.CalledProcessError as e:
        raise HTTPException(400, f"nečitelné audio {path}: {e.stderr.decode(errors='replace').strip()}") from e
    return np.frombuffer(raw, np.int16).astype(np.float32) / 32768.0


# --------------------------------------------------------------------------- API


@app.get("/healthz", response_model=Health)
def healthz() -> Health:
    return Health(status="ok", backend=BACKEND, model=MODEL if BACKEND != "passthrough" else "passthrough",
                  device=getattr(backend, "whisper_device", backend.device), loaded=backend.loaded(),
                  torch=backend.torch_version)


@app.post("/v1/transcribe", response_model=TranscribeResult)
def transcribe(req: TranscribeRequest) -> TranscribeResult:
    src = worker.input_file(req.audio_path)
    out_dir = worker.output_dir(req.output_dir)
    language = req.language or "en"

    t0 = time.monotonic()
    audio = load_audio(src)
    duration = len(audio) / SR
    log.info("transcribing %s (%.1f s, %s)", src, duration, language)
    with worker.gpu_lock:
        tr = backend.transcribe(audio, language, req.min_speakers, req.max_speakers)
    tr["duration_s"] = duration

    path = out_dir / "segments.json"
    write_json(path, tr)
    res = TranscribeResult(
        segments_path=str(path), language=tr["language"], duration_s=duration,
        segments=len(tr["segments"]), speakers=len(tr["speakers"]),
        model=MODEL if BACKEND != "passthrough" else "passthrough",
        device=getattr(backend, "whisper_device", backend.device), elapsed_s=time.monotonic() - t0,
    )
    log.info("done in %.1f s: %d segments, %d speakers", res.elapsed_s, res.segments, res.speakers)
    return res
