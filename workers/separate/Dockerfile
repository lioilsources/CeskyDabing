# =============================================================================
# dabing worker separate — Demucs htdemucs_ft na GB10 (sm_121, aarch64)
# =============================================================================
#
# Základ převzatý z AiStack services/audio/runtime (ověřené na SPARKu):
# CUDA 13 runtime + torch 2.10 cu130 z pip — jediný build s kernely pro sm_121.
# nvcr.io/nvidia/pytorch (DABING_PLAN §3) by šel také, ale tahle cesta je na
# SPARKu už vyzkoušená a image je o ~15 GB menší.
#
# Build (na SPARKu, z kořene repa):
#   docker build -f workers/separate/Dockerfile -t dabing/separate:latest workers/separate
#
# =============================================================================

ARG CUDA_TAG=13.0.1-runtime-ubuntu24.04
FROM nvidia/cuda:${CUDA_TAG}

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    PYTHONUNBUFFERED=1

RUN apt-get update && apt-get install -y --no-install-recommends \
        curl ca-certificates \
        python3.12 python3.12-venv \
        libsndfile1 \
    && rm -rf /var/lib/apt/lists/*

RUN python3.12 -m venv /opt/venv
ENV PATH=/opt/venv/bin:$PATH
RUN pip install --no-cache-dir --upgrade pip setuptools wheel

RUN pip install --no-cache-dir \
        --extra-index-url https://download.pytorch.org/whl/cu130 \
        torch==2.10.0+cu130 torchaudio==2.10.0+cu130

# Bez tohohle vyhraje systémový libcublas a i triviální matmul skončí na
# CUBLAS_STATUS_INVALID_VALUE (AiStack Dockerfile.sfx).
ENV NV_PIP_LIBS=/opt/venv/lib/python3.12/site-packages/nvidia
ENV LD_LIBRARY_PATH=${NV_PIP_LIBS}/cu13/lib:${NV_PIP_LIBS}/cudnn/lib:${NV_PIP_LIBS}/nccl/lib:${NV_PIP_LIBS}/cusparselt/lib:${LD_LIBRARY_PATH}
RUN test -f "${NV_PIP_LIBS}/cu13/lib/libcublas.so.13" \
    || (echo "CHYBA: torch nenainstaloval nvidia/cu13 knihovny — zkontroluj index cu130" && exit 1)

COPY requirements.txt constraints.txt /tmp/
RUN pip install --no-cache-dir \
        --extra-index-url https://download.pytorch.org/whl/cu130 \
        -c /tmp/constraints.txt -r /tmp/requirements.txt \
    && pip install --no-cache-dir --no-deps demucs==4.0.1 \
    && python3 -c "import torch; assert torch.__version__.startswith('2.10'), torch.__version__" \
    && python3 -c "from demucs.pretrained import get_model; from demucs.apply import apply_model; print('demucs OK')"

COPY app /srv/app
WORKDIR /srv

# Váhy Demucs se stáhnou při prvním použití do TORCH_HOME (volume /models).
ENV SEPARATE_BACKEND=demucs \
    SEPARATE_MODEL=htdemucs_ft \
    SEPARATE_SCRATCH=/scratch \
    TORCH_HOME=/models/torch \
    TORCHDYNAMO_DISABLE=1

EXPOSE 8101
HEALTHCHECK --interval=30s --timeout=10s --retries=3 --start-period=60s \
    CMD curl -sf http://localhost:8101/healthz || exit 1

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8101"]
