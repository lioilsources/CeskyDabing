"""Worker separate — Demucs separace hlasu od pozadí.

Stateless obal modelu: POST /v1/separate dostane cesty na sdíleném úložišti
(/mnt/dabing), zapíše vocals.wav + background.wav a vrátí cesty. Žádná logika
pipeline — ta je v Go orchestrátoru. Smlouva: ../openapi.yaml.

Prostředí:
  SEPARATE_BACKEND   demucs | passthrough (passthrough = bez modelu, pro testy)
  SEPARATE_MODEL     výchozí model, htdemucs_ft
  SEPARATE_ROOTS     povolené kořeny cest oddělené ":" (výchozí /mnt/dabing:$SEPARATE_SCRATCH)
  SEPARATE_SCRATCH   scratch pro upload fallback (výchozí /scratch)
  SEPARATE_SCRATCH_TTL_H  po kolika hodinách se scratch úlohy maže (24)
  SEPARATE_IDLE_UNLOAD_S  uvolnit model z GPU po nečinnosti (600, 0 = nikdy)
"""

from __future__ import annotations

import logging
import os
import shutil
import threading
import time
import uuid
from pathlib import Path
from typing import Any

import numpy as np
import soundfile as sf
from fastapi import FastAPI, HTTPException, UploadFile
from fastapi.responses import FileResponse, JSONResponse
from pydantic import BaseModel, Field

log = logging.getLogger("separate")
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")

BACKEND = os.environ.get("SEPARATE_BACKEND", "demucs")
DEFAULT_MODEL = os.environ.get("SEPARATE_MODEL", "htdemucs_ft")
SCRATCH = Path(os.environ.get("SEPARATE_SCRATCH", "/scratch"))
SCRATCH_TTL_S = float(os.environ.get("SEPARATE_SCRATCH_TTL_H", "24")) * 3600
IDLE_UNLOAD_S = float(os.environ.get("SEPARATE_IDLE_UNLOAD_S", "600"))
ROOTS = [
    Path(p).resolve()
    for p in os.environ.get("SEPARATE_ROOTS", f"/mnt/dabing:{SCRATCH}").split(":")
    if p
]


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


class FileRef(BaseModel):
    path: str
    dir: str
    size: int


class Health(BaseModel):
    status: str
    backend: str
    model: str
    device: str
    loaded: bool
    torch: str | None = None


# --------------------------------------------------------------------------- backends


class Passthrough:
    """Bez modelu: vocals = vstup, pozadí = ticho. Jen pro testy pipeline."""

    device = "cpu"
    torch_version = None

    def loaded(self) -> bool:
        return True

    def unload_if_idle(self) -> None:
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

            log.info("načítám %s na %s", name, self.device)
            m = get_model(name)
            m.eval()
            self.models = {name: m}  # jen jeden model v paměti
        return self.models[name]

    def unload_if_idle(self) -> None:
        if self.models and IDLE_UNLOAD_S > 0 and time.monotonic() - self.last_used > IDLE_UNLOAD_S:
            log.info("uvolňuji model po %.0f s nečinnosti", IDLE_UNLOAD_S)
            self.models = {}
            if self.device == "cuda":
                self.torch.cuda.empty_cache()

    def separate(self, audio: np.ndarray, sr: int, model: str, shifts: int) -> dict[str, np.ndarray]:
        import julius
        from demucs.apply import apply_model

        torch = self.torch
        m = self._model(model)
        x = torch.from_numpy(audio.T.copy())  # (kanály, vzorky)
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
gpu_lock = threading.Lock()


def _idle_loop() -> None:
    while True:
        time.sleep(30)
        with gpu_lock:
            backend.unload_if_idle()


threading.Thread(target=_idle_loop, daemon=True).start()

# --------------------------------------------------------------------------- helpers


def _allowed(p: str) -> Path:
    path = Path(p).resolve()
    if not any(path == r or r in path.parents for r in ROOTS):
        raise HTTPException(400, f"cesta {p} není pod povolenými kořeny {[str(r) for r in ROOTS]}")
    return path


def _write(path: Path, audio: np.ndarray, sr: int) -> None:
    tmp = path.with_name(f".{path.name}.tmp")
    sf.write(tmp, np.clip(audio, -1.0, 1.0), sr, subtype="PCM_16", format="WAV")
    os.replace(tmp, path)


def _rms(a: np.ndarray) -> float:
    return float(np.sqrt(np.mean(np.square(a, dtype=np.float64)))) if a.size else 0.0


def _clean_scratch() -> None:
    if not SCRATCH.is_dir():
        return
    now = time.time()
    for d in SCRATCH.iterdir():
        try:
            if d.is_dir() and now - d.stat().st_mtime > SCRATCH_TTL_S:
                shutil.rmtree(d, ignore_errors=True)
        except OSError:
            pass


# --------------------------------------------------------------------------- API

app = FastAPI(title="dabing worker separate", version="0.1.0")


@app.exception_handler(Exception)
def _unhandled(_, e: Exception) -> JSONResponse:
    # Chyba musí dojít orchestrátoru čitelně (jobs.error), ne jako holé 500.
    log.exception("neošetřená chyba")
    return JSONResponse(status_code=500, content={"detail": f"{type(e).__name__}: {e}"})


@app.get("/healthz", response_model=Health)
def healthz() -> Health:
    return Health(
        status="ok", backend=BACKEND, model=DEFAULT_MODEL, device=backend.device,
        loaded=backend.loaded(), torch=backend.torch_version,
    )


@app.post("/v1/separate", response_model=SeparateResult)
def separate(req: SeparateRequest) -> SeparateResult:
    src = _allowed(req.input_path)
    out_dir = _allowed(req.output_dir)
    if not src.is_file():
        raise HTTPException(404, f"{src} neexistuje")
    out_dir.mkdir(parents=True, exist_ok=True)
    model = req.model or DEFAULT_MODEL
    shifts = 1 if req.shifts is None else req.shifts

    t0 = time.monotonic()
    try:
        audio, sr = sf.read(src, dtype="float32", always_2d=True)  # (vzorky, kanály)
    except sf.LibsndfileError as e:
        raise HTTPException(400, f"nečitelné audio {src}: {e}") from e
    log.info("separuji %s (%.1f s, %d Hz) modelem %s", src, len(audio) / sr, sr, model)
    with gpu_lock:
        try:
            stems = backend.separate(audio, sr, model, shifts)
        except Exception as e:  # noqa: BLE001 — chybu vrátit orchestrátoru
            log.exception("separace selhala")
            raise HTTPException(500, f"{type(e).__name__}: {e}") from e

    vocals = stems.pop("vocals")
    background = sum(stems.values())
    vocals_path, background_path = out_dir / "vocals.wav", out_dir / "background.wav"
    _write(vocals_path, vocals, sr)
    _write(background_path, background, sr)
    stem_paths = None
    if req.keep_stems:
        (out_dir / "stems").mkdir(exist_ok=True)
        stem_paths = {}
        for name, a in stems.items():
            p = out_dir / "stems" / f"{name}.wav"
            _write(p, a, sr)
            stem_paths[name] = str(p)

    res = SeparateResult(
        vocals_path=str(vocals_path), background_path=str(background_path), stems=stem_paths,
        sample_rate=sr, duration_s=len(audio) / sr, model=model if BACKEND != "passthrough" else "passthrough",
        device=backend.device, elapsed_s=time.monotonic() - t0,
        vocals_rms=_rms(vocals), background_rms=_rms(background),
    )
    log.info("hotovo za %.1f s", res.elapsed_s)
    return res


@app.post("/v1/files", response_model=FileRef)
def upload_file(file: UploadFile) -> FileRef:
    _clean_scratch()
    job_dir = SCRATCH / uuid.uuid4().hex
    job_dir.mkdir(parents=True)
    dst = job_dir / Path(file.filename or "input.wav").name
    with dst.open("wb") as f:
        shutil.copyfileobj(file.file, f, length=1 << 20)
    return FileRef(path=str(dst), dir=str(job_dir), size=dst.stat().st_size)


@app.get("/v1/files")
def download_file(path: str) -> FileResponse:
    p = Path(path).resolve()
    if SCRATCH.resolve() not in p.parents:
        raise HTTPException(400, "stahovat lze jen ze scratch adresáře")
    if not p.is_file():
        raise HTTPException(404, f"{p} neexistuje")
    return FileResponse(p, media_type="application/octet-stream")
