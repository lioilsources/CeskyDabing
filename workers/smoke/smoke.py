"""M0 smoke: import modelu + CUDA na sm_121. Vypíše jeden JSON řádek.

SMOKE_LOAD=1 navíc stáhne váhy a model načte na GPU (pomalé, GB dat).
"""

import json
import os
import sys
import time
import traceback

target = sys.argv[1]
load = os.environ.get("SMOKE_LOAD") == "1"
res = {"model": target, "ok": False, "why": "", "torch": None, "cap": None}


def install_log_failure():
    try:
        lines = open("/install.log").read().splitlines()
    except OSError:
        return None
    fails = [l for l in lines if l.startswith(("FAIL", "WARN"))]
    errs = [l for l in lines if "ERROR" in l or "error:" in l]
    return "; ".join(fails + errs[-2:]) or None


def cuda_check():
    import torch

    res["torch"] = torch.__version__
    assert torch.cuda.is_available(), "torch.cuda.is_available() = False"
    res["cap"] = "sm_%d%d" % torch.cuda.get_device_capability()
    a = torch.randn(512, 512, device="cuda")
    (a @ a).sum().item()


def whisperx():
    import ctranslate2
    import whisperx  # noqa: F401

    # faster-whisper stojí na CTranslate2; aarch64 wheely z PyPI bývají jen CPU.
    n = ctranslate2.get_cuda_device_count()
    res["ct2_cuda_devices"] = n
    if n == 0:
        raise RuntimeError(f"CTranslate2 {ctranslate2.__version__} nevidí CUDA (aarch64 wheel bez GPU) → ASR jen na CPU")
    if load:
        whisperx.load_model("large-v3", "cuda", compute_type="float16")


def pyannote():
    from pyannote.audio import Pipeline  # noqa: F401

    if load:
        Pipeline.from_pretrained("pyannote/speaker-diarization-3.1", use_auth_token=os.environ.get("HF_TOKEN"))


def xtts():
    from TTS.tts.models.xtts import Xtts  # noqa: F401


def demucs():
    from demucs.apply import apply_model  # noqa: F401
    from demucs.pretrained import get_model

    if load:
        get_model("htdemucs_ft")


def moss():
    import torch
    from transformers import AutoModel, AutoProcessor

    if load:
        mid = "OpenMOSS-Team/MOSS-TTS-v1.5"
        AutoProcessor.from_pretrained(mid, trust_remote_code=True)
        AutoModel.from_pretrained(mid, trust_remote_code=True, attn_implementation="sdpa", dtype=torch.bfloat16).to("cuda")
    else:
        # Jen config + remote code (pár kB), bez vah: ověří, že kód modelu jde
        # importovat s transformers 5 a torch 2.10.
        from transformers import AutoConfig

        AutoConfig.from_pretrained("OpenMOSS-Team/MOSS-TTS-v1.5", trust_remote_code=True)


t0 = time.monotonic()
try:
    cuda_check()
    globals()[target]()
    res["ok"] = True
    res["why"] = install_log_failure() or ""
except BaseException as e:  # noqa: BLE001
    res["why"] = f"{type(e).__name__}: {e}".splitlines()[0][:300]
    inst = install_log_failure()
    if inst:
        res["why"] = f"{inst} | {res['why']}"
    traceback.print_exc(file=sys.stderr)
res["elapsed_s"] = round(time.monotonic() - t0, 1)
print(json.dumps(res, ensure_ascii=False))
