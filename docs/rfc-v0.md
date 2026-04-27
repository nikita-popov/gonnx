# RFC v0: Git-first ONNX Bundle Runtime

## Status

Draft.

## Purpose

This document describes a minimal architecture for a local and self-hostable inference runtime for ONNX models. The system is designed to avoid dependence on any central catalog by treating Git repositories as the canonical distribution mechanism for model bundles.

The design goal is to sit between a raw ONNX Runtime embedding and a heavyweight serving platform. ONNX Runtime provides a unified inference API and a pluggable Execution Provider model, which makes it suitable as the execution layer for heterogeneous hardware and model types.

## Design goals

- Git is the primary source of distribution, not a hosted registry.
- A model bundle is self-describing and reproducible through commit pinning.
- The core daemon is small and opinionated.
- Model-specific preprocessing and postprocessing live in a separate handler process.
- The system uses process isolation rather than in-process plugins.
- The external API is uniform even when model semantics differ.

## Non-goals

- No Kubernetes dependency.
- No mandatory OCI/container packaging in v0.
- No attempt to infer model semantics from `model.onnx` alone.
- No cross-model scheduling heuristics based on opaque GPU estimation in v0.
- No dependence on a public package index or central control plane.

## Terminology

- **Core daemon**: the long-lived process exposing the external API and supervising workers.
- **Worker**: a per-model process that loads a bundle, creates ONNX Runtime sessions, validates payloads, and runs inference.
- **Bundle**: a directory containing `manifest.yaml`, one or more ONNX artifacts, handler code, and optional assets.
- **Source identity**: Git URL, ref, and bundle subdirectory.
- **Resolved identity**: exact commit SHA plus bundle digest.

## High-level architecture

```
client
  │  HTTP
  ▼
gonnxd (core daemon)          ← registry, scheduler, supervisor
  │  HTTP over Unix socket
  ▼
worker process                ← handler + ONNX Runtime session
```

The worker process is the trust boundary for model-specific code. Handler code supplied by a bundle is not loaded into the core daemon address space.

## Technology stack

| Layer | Technology |
|---|---|
| Core daemon | Go 1.24+ |
| CLI | Go |
| Internal IPC | HTTP over Unix domain socket |
| Inference runtime | ONNX Runtime |
| Worker SDK v0 | Python 3.12 |
| Schema | JSON Schema 2020-12 + OpenAPI 3.1 |
| Local state | SQLite or bbolt |
| Logging | Structured JSON |
| Metrics | Prometheus text endpoint |
| Service | systemd |

## Git-first distribution

Git repositories are the canonical source of truth for bundles. A bundle may live at the repository root or inside a subdirectory of a monorepo.

The installer uses the system `git` binary with `--filter=blob:none` partial clone and sparse checkout to minimize transfer size.

### Accepted source forms

```
git+https://host/org/repo.git?ref=master&dir=models/resnet50
git+ssh://git@host/org/repo.git?ref=master&dir=models/resnet50
https://host/org/repo.git --ref master --dir models/resnet50
```

### Installation flow

1. Resolve source identity: URL, ref, and bundle directory.
2. Fetch or update a local mirror of the repository.
3. Materialize the requested directory using sparse checkout.
4. Resolve the exact commit SHA.
5. Verify bundle structure and schema.
6. Compute a bundle digest.
7. Store install metadata in the local registry.

### Update flow

1. Fetch remote changes for the configured source.
2. Resolve the configured ref to a new commit SHA.
3. Materialize the new revision side-by-side.
4. Run bundle verification.
5. Switch active revision only after verification succeeds.
6. Preserve the previous revision for rollback.

## Bundle layout

```
bundle/
  manifest.yaml           required
  model.onnx              required (or declared alternative name)
  handler.py              required (entrypoint declared in manifest)
  requirements.txt        optional
  assets/                 optional
    labels.txt
    tokenizer.json
  examples/               optional
    request.json
```

## Manifest structure

See `examples/resnet50/manifest.yaml` for a complete annotated example.

Required sections: `apiVersion`, `kind`, `name`, `version`, `runtime`, `handler`, `interface`, `policy`.

The `interface.inputSchema` and `interface.outputSchema` fields use JSON Schema 2020-12. The core daemon validates incoming requests against `inputSchema` before dispatching to the worker.

## Worker contract

Worker exposes HTTP over a Unix domain socket.

### Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | /health | Liveness |
| GET | /describe | Bundle metadata and schemas |
| POST | /predict | Inference |
| POST | /shutdown | Graceful stop |

## External API v0

| Method | Path | Purpose |
|---|---|---|
| GET | /healthz | Daemon health |
| GET | /v1/models | List installed bundles |
| GET | /v1/models/{id} | Bundle metadata |
| POST | /v1/models/{id}:load | Start worker |
| POST | /v1/models/{id}:unload | Stop worker |
| POST | /v1/models/{id}:predict | Run inference |
| POST | /v1/models:install | Install from Git source |
| POST | /v1/models/{id}:update | Update configured source |
| GET | /v1/models/{id}/logs | Worker logs |
| GET | /metrics | Prometheus metrics |

## Security model

### Threat model

Bundle authors may be untrusted. A bundle includes executable handler code and model artifacts fetched from Git. Installing a bundle is equivalent to importing third-party code and data.

Main risks:
- remote code execution through handler code
- supply-chain attacks through compromised Git origins
- malicious Python dependencies
- local file or network exfiltration by handlers
- downgrade attacks if installs track moving branches without pinning

### Trust levels

| Level | Description | Default restrictions |
|---|---|---|
| `local` | Local developer-controlled repo | Relaxed, but still separate process |
| `trusted` | Approved origin | Network disabled, read-only filesystem |
| `untrusted` | Unknown or new origin | As trusted, stricter timeouts, manual approval |

### Source verification

- Moving ref names (e.g. `master`) must be resolved to an immutable commit SHA before activation.
- Exact commit SHA and bundle digest are recorded permanently.
- Optional allowlist of approved hostnames.
- Warning on first install from a previously unseen origin.

### Execution isolation

- Handler code runs in a separate worker process.
- Core daemon communicates only through declared HTTP-over-UDS interface.
- Dedicated unprivileged user for the daemon.
- `NO_NEW_PRIVS` where available.
- cgroup or systemd resource limits for CPU and memory.

### Network policy

Network egress disabled for workers by default. Manifest may request network access, but operator approval required.

### Filesystem policy

- Bundle files are read-only.
- Workers receive an isolated writable scratch path for temporary files.

```
~/.local/share/gonnx/
  repos/
  bundles/
  venvs/
  scratch/<worker-id>/
  run/<worker-id>.sock
  logs/<bundle-name>.jsonl
  state/registry.db
```

### Dependency isolation

- Dependencies installed into per-bundle virtual environments.
- No global site-packages inheritance.
- Hash-locked requirements recommended.

### Audit log

Every activation event emits structured log with: bundle name, origin URL, resolved commit SHA, bundle digest, worker PID, trust level, security policy overrides.

## CLI

```
gonnxctl install <git-url> --ref master --dir models/resnet50
gonnxctl list
gonnxctl show resnet50
gonnxctl load resnet50
gonnxctl unload resnet50
gonnxctl run resnet50 -f request.json
gonnxctl update resnet50
gonnxctl pin resnet50 --commit <sha>
gonnxctl logs resnet50
gonnxctl verify resnet50
```

## Verification pipeline

On install or update, before activation:

1. `manifest.yaml` exists
2. Manifest matches schema
3. Handler entrypoint exists
4. ONNX model files exist
5. Input and output schemas parse
6. Bundle digest computed
7. Dependency environment created
8. Worker boots and answers `GET /health`
9. Worker returns valid `GET /describe`

## Rollback

Updates stage the new revision side-by-side. Active revision pointer switches only after verification. Previous revision retained for rollback. Rollback is a metadata operation.

## v0 implementation priorities

1. Go daemon: install, load, predict, logs
2. Python worker SDK
3. Git install via system `git`: ref and dir support
4. JSON Schema validation at ingress
5. ResNet-50 example bundle

## Open questions for v1

- Signed bundle manifests
- Signed commits / verified history
- OCI export/import as alternative transport
- Streaming and multipart request support
- Multi-instance workers per bundle
- GPU-aware scheduling
- Namespace / seccomp sandboxing
- Windows and macOS isolation parity
