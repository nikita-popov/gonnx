# gonnx

**ollama for ONNX.**  
Install any ONNX model from a Git repo with one command. Serve it over HTTP with one endpoint. Done.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8.svg)](go.mod)
[![Status: Early](https://img.shields.io/badge/Status-Early%20Development-orange.svg)](#status)

---

## The problem

Every time you want to serve a non-LLM model — an image classifier, a TTS voice, an ASR system, anything in ONNX — you write the same boilerplate:

- Load `onnxruntime`
- Wrap it in FastAPI / Flask / Gin
- Handle preprocessing and postprocessing
- Pin model versions somehow
- Repeat for every new model

`ollama run llama3` made this a one-liner for LLMs. **gonnx does the same for any ONNX model.**

```bash
gonnxctl install https://github.com/you/your-model.git
gonnxctl run your-model
```

That's it.

---

## Three commands to inference

```bash
# 1. Start the daemon
gonnxd --addr :7860

# 2. Install a model directly from any Git repo
gonnxctl install https://github.com/nikita-popov/examples-gonnx.git --dir resnet50

# 3. Run inference
echo '{"image": "'$(base64 -w0 cat.jpg)'", "top_k": 3}' | gonnxctl run resnet50
```

Or hit it over plain HTTP:

```bash
curl -X POST http://localhost:7860/v1/models/resnet50:predict \
  -H 'Content-Type: application/json' \
  -d @request.json
```

---

## Why Git, not another hub?

Most ML tooling assumes you'll fetch models from a central registry — HuggingFace Hub, ONNX Model Zoo, a private artifactory. That's a single point of failure: it can be paywalled, geoblocked, taken down, or rate-limited.

**gonnx treats Git the same way Go modules do — Git is the source of truth, no central index required.**

- ✅ **No central registry.** Any Git host works: GitHub, GitLab, Gitea, self-hosted.
- ✅ **Reproducible by commit SHA.** A model pinned to `a3f5b2c…` is bit-identical forever.
- ✅ **You already know the tools.** `git push` is your release pipeline.
- ✅ **Self-hostable.** A bundle in a private repo stays private.

---

## What's a "bundle"?

A bundle is a directory in a Git repo containing:

```
your-model/
├── manifest.yaml      # what this model is, inputs, outputs
├── handler.py         # your preprocessing/postprocessing code
└── (model.onnx)       # weights — fetched separately by sha256
```

Weights are **not committed to Git**. They're fetched via the manifest's `assets` section, verified by SHA-256, and cached locally. Big binaries don't bloat the repo; small manifests stay diff-able.

```yaml
# manifest.yaml (excerpt)
name: resnet50
version: 1.0.0
assets:
  - id: model
    url: https://example.com/resnet50.onnx
    sha256: 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
    dest: ./model.onnx
```

See [examples-gonnx](https://github.com/nikita-popov/examples-gonnx) for full working bundles.

---

## Use cases

**You should probably use gonnx if you:**
- Run multiple ONNX models on one box and want a uniform HTTP API for all of them
- Want reproducible model deployments without Docker
- Need to share inference setups across a team without a central hub
- Are tired of writing the same FastAPI wrapper over and over

**You probably don't need gonnx if you:**
- Only ever run one model from one Python script — `onnxruntime.InferenceSession` is fine
- Already have Triton Inference Server or BentoML in production and they work
- Need sub-millisecond latency on tiny CPU models — the daemon hop costs you 1-2ms

---

## Architecture

```
client
  │  HTTP
  ▼
gonnxd (core daemon)          ← registry, scheduler, supervisor
  │  HTTP over Unix socket
  ▼
worker process                ← handler.py + ONNX Runtime session
```

The worker process is the trust boundary. **Bundle code never runs in the daemon's address space** — it lives in an isolated subprocess with its own venv. If a model crashes or misbehaves, the daemon stays up.

For the full design — including threat model, asset verification, trust levels, and rollback semantics — see [`docs/rfc-v0.md`](docs/rfc-v0.md).

---

## Installation

### One-liner (Linux / macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/nikita-popov/gonnx/master/install.sh | sh
```

This installs `gonnxd` and `gonnxctl` to `~/.local/bin` (or `/usr/local/bin` if run as root). Pin a specific version with `-s -- --version v0.1.0`.

### From source

```bash
git clone https://github.com/nikita-popov/gonnx.git
cd gonnx
make build
export PATH="$PWD/bin:$PATH"
```

Requirements: **Go ≥ 1.22**.

---

## Bundle examples

Ready-to-run bundles live at [**nikita-popov/examples-gonnx**](https://github.com/nikita-popov/examples-gonnx):

| Bundle | Task | Model |
|---|---|---|
| [resnet50](https://github.com/nikita-popov/examples-gonnx/tree/master/resnet50) | Image classification (ImageNet-1k) | ResNet-50 |
| [kokoro-tts](https://github.com/nikita-popov/examples-gonnx/tree/master/kokoro-tts) | Multilingual TTS (9 languages) | Kokoro-82M |
| [piper-ru](https://github.com/nikita-popov/examples-gonnx/tree/master/piper-ru) | Russian TTS | Piper Irina medium |
| [silero-ru](https://github.com/nikita-popov/examples-gonnx/tree/master/silero-ru) | Russian TTS (5 speakers) | Silero v4 |

Want to add your own? Fork `examples-gonnx`, copy a bundle, swap the model — that's the workflow.

---

## CLI cheat sheet

```bash
gonnxctl install <git-url>        # install bundle from Git
gonnxctl pull <name>              # fetch / verify model assets
gonnxctl list                     # list installed bundles
gonnxctl load <name>              # start worker
gonnxctl unload <name>            # stop worker
gonnxctl run <name> -f request.json
gonnxctl update <name>            # pull latest revision
gonnxctl pin <name> --commit <sha>
gonnxctl logs <name>
```

---

## Repository layout

```
cmd/
  gonnxd/         core daemon entry point
  gonnxctl/       CLI client
internal/
  api/            external HTTP router
  bundle/         manifest parsing and validation
  registry/       local installed-bundle database
  runtime/        worker supervision and IPC
  scheduler/      load/unload/eviction policy
  schema/         JSON Schema validation
  source/         Git install and update
  telemetry/      structured logging and metrics
sdk/
  python/         Python worker SDK
docs/
  rfc-v0.md       full architecture RFC
```

---

## Development

| Tool | Min version |
|---|---|
| Go | 1.22 |
| Python | 3.10 |
| git | 2.25 (sparse-checkout) |
| make | any |

```bash
make check          # go fmt + vet + tests + Python SDK tests
make test           # Go tests only (with race detector)
make test-py        # Python SDK tests only
```

To add an internal package:
1. Create `internal/<pkg>/`
2. Write `*_test.go` alongside the code
3. Run `make check` before opening a PR

---

## Status

**Early development.** v0.1.0 is the first tagged release. Core install/load/predict path works on Linux and macOS. Expect rough edges. Issues and PRs welcome.

Roadmap highlights from [`docs/rfc-v0.md`](docs/rfc-v0.md):
- Binary transport (msgpack / octet-stream) for large tensors
- Native Go worker SDK alongside Python
- GPU execution provider configuration via manifest
- Batching and multi-instance workers
- Signed manifests and OCI export

---

## License

MIT © Nikita Popov
