"""Shared plumbing of the dabing model workers.

Every worker is a stateless FastAPI wrapper of one model: paths on the shared
storage in, paths out. This module holds what all of them repeat — allowed
roots, the upload fallback (/v1/files), atomic writes, readable errors and
releasing an idle model from the GPU.

Environment, with <P> the worker prefix (SEPARATE, ASR, …):
  <P>_ROOTS          allowed path roots separated by ":" (default /mnt/dabing:$<P>_SCRATCH)
  <P>_SCRATCH        scratch for the upload fallback (default /scratch)
  <P>_SCRATCH_TTL_H  hours after which scratch jobs are deleted (24)
  <P>_IDLE_UNLOAD_S  release the model after idling (600, 0 = never)
"""

from __future__ import annotations

import json
import logging
import os
import shutil
import threading
import time
import uuid
from pathlib import Path
from typing import Any, Protocol

import numpy as np
from fastapi import FastAPI, HTTPException, UploadFile
from fastapi.responses import FileResponse, JSONResponse
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")


class FileRef(BaseModel):
    path: str
    dir: str
    size: int


class Unloadable(Protocol):
    def unload_if_idle(self, idle_s: float) -> None: ...


class Worker:
    def __init__(self, name: str, prefix: str, version: str = "0.1.0") -> None:
        env = lambda key, default: os.environ.get(f"{prefix}_{key}", default)  # noqa: E731
        self.log = logging.getLogger(name)
        self.scratch = Path(env("SCRATCH", "/scratch"))
        self.scratch_ttl_s = float(env("SCRATCH_TTL_H", "24")) * 3600
        self.idle_unload_s = float(env("IDLE_UNLOAD_S", "600"))
        self.roots = [Path(p).resolve() for p in env("ROOTS", f"/mnt/dabing:{self.scratch}").split(":") if p]
        # One model call at a time; the GPU is shared with other projects.
        self.gpu_lock = threading.Lock()
        self.app = FastAPI(title=f"dabing worker {name}", version=version)
        self._routes()

    # ------------------------------------------------------------------ paths

    def allowed(self, p: str) -> Path:
        path = Path(p).resolve()
        if not any(path == r or r in path.parents for r in self.roots):
            raise HTTPException(400, f"cesta {p} není pod povolenými kořeny {[str(r) for r in self.roots]}")
        return path

    def input_file(self, p: str) -> Path:
        path = self.allowed(p)
        if not path.is_file():
            raise HTTPException(404, f"{path} neexistuje")
        return path

    def output_dir(self, p: str) -> Path:
        path = self.allowed(p)
        path.mkdir(parents=True, exist_ok=True)
        return path

    # ------------------------------------------------------------------ model lifecycle

    def unload_when_idle(self, backend: Unloadable) -> None:
        def loop() -> None:
            while True:
                time.sleep(30)
                with self.gpu_lock:
                    backend.unload_if_idle(self.idle_unload_s)

        threading.Thread(target=loop, daemon=True).start()

    # ------------------------------------------------------------------ routes

    def _routes(self) -> None:
        app = self.app

        @app.exception_handler(Exception)
        def _unhandled(_, e: Exception) -> JSONResponse:
            # The orchestrator must get a readable error (jobs.error), not a bare 500.
            self.log.exception("unhandled error")
            return JSONResponse(status_code=500, content={"detail": f"{type(e).__name__}: {e}"})

        @app.post("/v1/files", response_model=FileRef)
        def upload_file(file: UploadFile) -> FileRef:
            self._clean_scratch()
            job_dir = self.scratch / uuid.uuid4().hex
            job_dir.mkdir(parents=True)
            dst = job_dir / Path(file.filename or "input.wav").name
            with dst.open("wb") as f:
                shutil.copyfileobj(file.file, f, length=1 << 20)
            return FileRef(path=str(dst), dir=str(job_dir), size=dst.stat().st_size)

        @app.get("/v1/files")
        def download_file(path: str) -> FileResponse:
            p = Path(path).resolve()
            if self.scratch.resolve() not in p.parents:
                raise HTTPException(400, "stahovat lze jen ze scratch adresáře")
            if not p.is_file():
                raise HTTPException(404, f"{p} neexistuje")
            return FileResponse(p, media_type="application/octet-stream")

    def _clean_scratch(self) -> None:
        if not self.scratch.is_dir():
            return
        now = time.time()
        for d in self.scratch.iterdir():
            try:
                if d.is_dir() and now - d.stat().st_mtime > self.scratch_ttl_s:
                    shutil.rmtree(d, ignore_errors=True)
            except OSError:
                pass


# ---------------------------------------------------------------------- atomic writes


def _tmp(path: Path) -> Path:
    return path.with_name(f".{path.name}.tmp")


def write_wav(path: Path, audio: np.ndarray, sr: int) -> None:
    import soundfile as sf

    tmp = _tmp(path)
    sf.write(tmp, np.clip(audio, -1.0, 1.0), sr, subtype="PCM_16", format="WAV")
    os.replace(tmp, path)


def write_json(path: Path, obj: Any) -> None:
    tmp = _tmp(path)
    tmp.write_text(json.dumps(obj, ensure_ascii=False, indent=1))
    os.replace(tmp, path)


def rms(a: np.ndarray) -> float:
    return float(np.sqrt(np.mean(np.square(a, dtype=np.float64)))) if a.size else 0.0
