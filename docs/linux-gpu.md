# Linux + GPU deployment (CachyOS / 32 GiB / GTX 1080 Ti class)

Athanor is local-first and does not require a particular GPU, but a Linux host
with a discrete NVIDIA GPU is a first-class target. This guide is the
32 GiB / 1080 Ti / Ryzen 7 3700X shape: enough RAM and VRAM for a single
resident 7B–13B model with a large context, not enough to keep several large
models resident at once.

## 1. Host prerequisites

- CachyOS/Arch-based Linux with the NVIDIA proprietary driver and CUDA
  userspace installed (`nvidia-smi` works).
- Rootless Podman (`podman info` works as your user; no `sudo`).
- Ollama on the host, built with CUDA support. Verify:
  ```bash
  nvidia-smi
  ollama --version
  ollama run <model> "Reply with one word: ok"
  ollama ps                     # while generating: should show GPU/CPU split
  ```
- For the Core Pod to reach Ollama, the host must expose it on the Podman host
  gateway. `host.containers.internal:11434` is the usual URL on Linux.
- Git and the usual build toolchain for `make core-image` / `make jobpod-image`.

## 2. Ollama settings for one resident model

The default is deliberately serialized so one model stays resident and its
context is not divided:

```bash
export OLLAMA_MAX_LOADED_MODELS=1
export OLLAMA_NUM_PARALLEL=1
export OLLAMA_KEEP_ALIVE=10m
```

On 11 GiB VRAM, a 7B Q4 model with a 16K context is comfortable; a 13B Q4 model
with 8K–16K may partially offload to system RAM. Prefer a smaller model with a
larger context over a larger model that spills. Let `ollama ps` tell you the
GPU/CPU split rather than guessing.

## 3. Config for the Core Pod

`~/.config/athanor/config.yaml` (the installer writes a baseline):

```yaml
inference:
  ollama_url: "http://host.containers.internal:11434"
  keep_alive: "10m"

limits:
  max_concurrent_jobs: 1          # one job at a time on this machine
  max_concurrent_llm_calls: 1     # one model call; keep the model resident

job_pod:
  image: "localhost/athanor-jobpod:latest"
  memory_mb: 4096
  cpus: 2
  tmpfs_mb: 2048
  pids_limit: 256
```

Run the installer (or `athanor start`) and open the welcome wizard:

```bash
sh scripts/install.sh --no-open    # headless-friendly
# or:
./bin/athanor start -config ~/.config/athanor/config.yaml
```

The onboarding **Environment** step runs `doctor` inside the Core. The Core Pod
cannot see the host GPU (the image has no `nvidia-smi` and no GPU device), so
the host probe is passed into the Core by `athanor start` (ADR-0107). The
**Resources** step shows the detected GPU, its VRAM, and the full-GPU model
budget; the **Models** step uses VRAM (not host RAM) for a discrete GPU and
warns when a model would spill. Run `athanor doctor` on the host to see the
same accelerator row. The `Resources` step still proposes Job Pod limits from
host RAM; keep the Job Pod at 4 GiB unless a test suite needs more. Ollama's
memory is on the host and is not part of the Job Pod limit. Job Pods are
CPU-only by default; GPU passthrough is a future opt-in (HITL-gated).

## 4. Running the benchmark / soak

The benchmark runs inside the Core Pod and reads the named state volume:

```bash
make bench-verify
make bench-image
make bench-config BENCH_MODEL=<model> BENCH_JUDGE=<judge>
make bench-full
```

The 1080 Ti is slower than Apple Silicon unified memory. Use the 6-task smoke
first, then the full slice; expect the full run to take the full 12-hour
budget on a 13B model.

## 5. What to capture if something is wrong

- `nvidia-smi` and `nvidia-smi --query-gpu=name,memory.total --format=csv`
- `ollama --version` and `ollama ps` during a generation
- `podman info` (rootless mode) and `podman ps -a`
- `athanor doctor -config ~/.config/athanor/config.yaml`
- The Core log: `podman logs athanor-core`

The most common Linux failures are the Podman socket path, a Core Pod that
cannot resolve `host.containers.internal`, and an Ollama build without CUDA
(so it silently runs on CPU). The host doctor's GPU row and `ollama ps` make
those visible.

## 6. When to raise concurrency

A 32 GiB / 11 GiB machine should stay at one job and one model call. If you add
RAM/VRAM later, raise `limits.max_concurrent_jobs` first (overlap pod work with
planning), and only then `max_concurrent_llm_calls` after setting
`OLLAMA_MAX_LOADED_MODELS`/`OLLAMA_NUM_PARALLEL` consistently. The power profile
must be **autonomous** (AC power and idle) for more than one job; the
interactive profile throttles back to one job. See
[ADR-0096](adr/0096-capacity-aware-concurrency.md).
