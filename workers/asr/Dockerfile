# =============================================================================
# dabing worker asr — WhisperX + pyannote on GB10 (sm_121, aarch64)
# =============================================================================
#
# Same base as worker separate (CUDA 13 + torch 2.10 cu130 from pip). Built by a
# spark-broker job from the repo root (see docs/spark.md):
#   docker build -f workers/asr/Dockerfile -t dabing/asr:latest workers
#
# =============================================================================

ARG CUDA_TAG=13.0.1-runtime-ubuntu24.04
FROM nvidia/cuda:${CUDA_TAG}

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    PYTHONUNBUFFERED=1

RUN apt-get update && apt-get install -y --no-install-recommends \
        curl ca-certificates ffmpeg \
        python3.12 python3.12-venv \
        libsndfile1 \
    && rm -rf /var/lib/apt/lists/*

RUN python3.12 -m venv /opt/venv
ENV PATH=/opt/venv/bin:$PATH
RUN pip install --no-cache-dir --upgrade pip setuptools wheel

RUN pip install --no-cache-dir \
        --extra-index-url https://download.pytorch.org/whl/cu130 \
        torch==2.10.0+cu130 torchaudio==2.10.0+cu130

# Without this the system libcublas wins and even a trivial matmul fails with
# CUBLAS_STATUS_INVALID_VALUE (AiStack Dockerfile.sfx).
ENV NV_PIP_LIBS=/opt/venv/lib/python3.12/site-packages/nvidia
ENV LD_LIBRARY_PATH=${NV_PIP_LIBS}/cu13/lib:${NV_PIP_LIBS}/cudnn/lib:${NV_PIP_LIBS}/nccl/lib:${NV_PIP_LIBS}/cusparselt/lib:${LD_LIBRARY_PATH}

COPY asr/requirements.txt asr/constraints.txt /tmp/
RUN pip install --no-cache-dir \
        --extra-index-url https://download.pytorch.org/whl/cu130 \
        -c /tmp/constraints.txt -r /tmp/requirements.txt \
    && python3 -c "import torch; assert torch.__version__.startswith('2.10'), torch.__version__" \
    && python3 -c "import whisperx, pyannote.audio; print('asr OK')"

COPY common/dabing_worker.py /srv/
COPY asr/app /srv/app
WORKDIR /srv

# Weights download on first use into HF_HOME (volume /models). pyannote needs HF_TOKEN.
ENV ASR_BACKEND=whisperx \
    ASR_MODEL=large-v3 \
    ASR_SCRATCH=/scratch \
    HF_HOME=/models/hf \
    TORCH_HOME=/models/torch \
    TORCHDYNAMO_DISABLE=1 \
    PYTHONPATH=/srv

EXPOSE 8105
HEALTHCHECK --interval=30s --timeout=10s --retries=3 --start-period=60s \
    CMD curl -sf http://localhost:8105/healthz || exit 1

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8105"]
