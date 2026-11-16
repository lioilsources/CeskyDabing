"""Worker separate — Demucs vocal/background separation.

Stateless model wrapper: POST /v1/separate takes paths on the shared storage,
writes vocals.wav + background.wav and returns their paths. No pipeline logic —
that lives in the Go orchestrator. Contract: ../openapi.yaml. Shared plumbing
(roots, scratch, /v1/files, idle unload): ../../common/dabing_worker.py.

Environment (plus the SEPARATE_* variables of dabing_worker):
  SEPARATE_BACKEND   demucs | passthrough (passthrough = no model, for tests)
  SEPARATE_MODEL     default model, htdemucs_ft
"""

from __future__ import annotations

import os
import time
from typing import Any

import numpy as np
import soundfile as sf
from fastapi import HTTPException
from pydantic import BaseModel, Field

from dabing_worker import Worker, rms, write_wav

BACKEND = os.environ.get("SEPARATE_BACKEND", "demucs")
DEFAULT_MODEL = os.environ.get("SEPARATE_MODEL", "htdemucs_ft")

worker = Worker("separate", "SEPARATE")
log = worker.log
app = worker.app


class SeparateRequest(BaseModel):
    input_path: str
    output_dir: str
    model: str | None = None
    shifts: int | None = Field(default=None, ge=0, le=10)
    keep_stems: bool | None = None


class SeparateResult(BaseModel):
    vocals_path: str
    background_path: str
    stems: dict[str, str] | None = None
    sample_rate: int
    duration_s: float
    model: str
    device: str
    elapsed_s: float
    vocals_rms: float
    background_rms: float


class Health(BaseModel):
    status: str
    backend: str
    model: str
    device: str
    loaded: bool
    torch: str | None = None


# --------------------------------------------------------------------------- backends


class Passthrough:
    """No model: vocals = input, background = silence. For pipeline tests only."""

    device = "cpu"
    torch_version = None

    def loaded(self) -> bool:
        return True

    def unload_if_idle(self, idle_s: float) -> None:
        pass

    def separate(self, audio: np.ndarray, sr: int, model: str, shifts: int) -> dict[str, np.ndarray]:
        return {"vocals": audio, "other": np.zeros_like(audio)}


class Demucs:
    def __init__(self) -> None:
        import torch

        self.torch = torch
        self.torch_version = torch.__version__
        self.device = "cuda" if torch.cuda.is_available() else "cpu"
        self.models: dict[str, Any] = {}
        self.last_used = time.monotonic()

    def loaded(self) -> bool:
        return bool(self.models)

    def _model(self, name: str):
        if name not in self.models:
            from demucs.pretrained import get_model

            log.info("loading %s on %s", name, self.device)
            m = get_model(name)
            m.eval()
            self.models = {name: m}  # keep a single model in memory
        return self.models[name]

    def unload_if_idle(self, idle_s: float) -> None:
        if self.models and idle_s > 0 and time.monotonic() - self.last_used > idle_s:
            log.info("releasing the model after %.0f s idle", idle_s)
            self.models = {}
            if self.device == "cuda":
                self.torch.cuda.empty_cache()

    def separate(self, audio: np.ndarray, sr: int, model: str, shifts: int) -> dict[str, np.ndarray]:
        import julius
        from demucs.apply import apply_model

        torch = self.torch
        m = self._model(model)
        x = torch.from_numpy(audio.T.copy())  # (channels, samples)
        if x.shape[0] == 1:
            x = x.repeat(2, 1)
        elif x.shape[0] > 2:
            x = x[:2]
        if sr != m.samplerate:
            x = julius.resample_frac(x, sr, m.samplerate)
        ref = x.mean(0)
        mean, std = ref.mean(), ref.std() + 1e-8
        with torch.no_grad():
            out = apply_model(
                m, ((x - mean) / std)[None], device=self.device,
                shifts=shifts, split=True, overlap=0.25, progress=False,
            )[0]
        out = out * std + mean
        stems = {}
        for name, s in zip(m.sources, out):
            if sr != m.samplerate:
                s = julius.resample_frac(s, m.samplerate, sr)
            stems[name] = s.cpu().numpy().T
        self.last_used = time.monotonic()
        return stems


backend = Passthrough() if BACKEND == "passthrough" else Demucs()
worker.unload_when_idle(backend)

# --------------------------------------------------------------------------- API


@app.get("/healthz", response_model=Health)
def healthz() -> Health:
    return Health(
        status="ok", backend=BACKEND, model=DEFAULT_MODEL, device=backend.device,
        loaded=backend.loaded(), torch=backend.torch_version,
    )


@app.post("/v1/separate", response_model=SeparateResult)
def separate(req: SeparateRequest) -> SeparateResult:
    src = worker.input_file(req.input_path)
    out_dir = worker.output_dir(req.output_dir)
    model = req.model or DEFAULT_MODEL
    shifts = 1 if req.shifts is None else req.shifts

    t0 = time.monotonic()
    try:
        audio, sr = sf.read(src, dtype="float32", always_2d=True)  # (samples, channels)
    except sf.LibsndfileError as e:
        raise HTTPException(400, f"nečitelné audio {src}: {e}") from e
    log.info("separating %s (%.1f s, %d Hz) with %s", src, len(audio) / sr, sr, model)
    with worker.gpu_lock:
        stems = backend.separate(audio, sr, model, shifts)

    vocals = stems.pop("vocals")
    background = sum(stems.values())
    vocals_path, background_path = out_dir / "vocals.wav", out_dir / "background.wav"
    write_wav(vocals_path, vocals, sr)
    write_wav(background_path, background, sr)
    stem_paths = None
    if req.keep_stems:
        (out_dir / "stems").mkdir(exist_ok=True)
        stem_paths = {}
        for name, a in stems.items():
            p = out_dir / "stems" / f"{name}.wav"
            write_wav(p, a, sr)
            stem_paths[name] = str(p)

    res = SeparateResult(
        vocals_path=str(vocals_path), background_path=str(background_path), stems=stem_paths,
        sample_rate=sr, duration_s=len(audio) / sr, model=model if BACKEND != "passthrough" else "passthrough",
        device=backend.device, elapsed_s=time.monotonic() - t0,
        vocals_rms=rms(vocals), background_rms=rms(background),
    )
    log.info("done in %.1f s", res.elapsed_s)
    return res
