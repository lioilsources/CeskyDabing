# =============================================================================
# M0 smoke test of the models on GB10 (sm_121): works / fails / why.
#
# Each model has its own target (their dependencies clash). All share the base
# of the separate worker: CUDA 13 + torch 2.10 cu130. Run through smoke.sh.
# =============================================================================

ARG CUDA_TAG=13.0.1-runtime-ubuntu24.04
FROM nvidia/cuda:${CUDA_TAG} AS base
ENV DEBIAN_FRONTEND=noninteractive LANG=C.UTF-8 PYTHONUNBUFFERED=1
RUN apt-get update && apt-get install -y --no-install-recommends \
        git curl ca-certificates build-essential ffmpeg \
        python3.12 python3.12-dev python3.12-venv libsndfile1 \
    && rm -rf /var/lib/apt/lists/*
RUN python3.12 -m venv /opt/venv
ENV PATH=/opt/venv/bin:$PATH
RUN pip install --no-cache-dir --upgrade pip setuptools wheel \
    && pip install --no-cache-dir --extra-index-url https://download.pytorch.org/whl/cu130 \
        torch==2.10.0+cu130 torchaudio==2.10.0+cu130
ENV NV_PIP_LIBS=/opt/venv/lib/python3.12/site-packages/nvidia
ENV LD_LIBRARY_PATH=${NV_PIP_LIBS}/cu13/lib:${NV_PIP_LIBS}/cudnn/lib:${NV_PIP_LIBS}/nccl/lib:${NV_PIP_LIBS}/cusparselt/lib:${LD_LIBRARY_PATH}
ENV PIP_EXTRA_INDEX_URL=https://download.pytorch.org/whl/cu130 \
    TORCHDYNAMO_DISABLE=1 HF_HOME=/models/hf
RUN printf 'torch==2.10.0+cu130\ntorchaudio==2.10.0+cu130\n' > /constraints.txt
COPY smoke.py /smoke.py
# Installs may fail; the failure is logged and smoke.py reports it.
SHELL ["/bin/bash", "-c"]

FROM base AS whisperx
RUN pip install --no-cache-dir -c /constraints.txt whisperx > /install.log 2>&1 \
    || echo "FAIL pip install whisperx" >> /install.log

FROM base AS pyannote
RUN pip install --no-cache-dir -c /constraints.txt "pyannote.audio>=3.1" > /install.log 2>&1 \
    || echo "FAIL pip install pyannote.audio" >> /install.log

# MOSS-TTS pins torch 2.9.1+cu128 only in the [torch-runtime] extra (cu128 has
# no sm_121 kernels), so it is installed without it and the runtime stack is
# added on torch 2.10 cu130. The model loads via transformers trust_remote_code.
FROM base AS moss
RUN git clone --depth 1 https://github.com/OpenMOSS/MOSS-TTS.git /opt/moss \
    && (pip install --no-cache-dir -c /constraints.txt -e /opt/moss \
            "transformers==5.0.0" "accelerate>=1.10.1" "torchcodec==0.10.0+cu130" soundfile > /install.log 2>&1 \
        || echo "FAIL pip install MOSS-TTS" >> /install.log)
