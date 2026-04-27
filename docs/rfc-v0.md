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
- **Bundle**: a directory containing `manifest.yaml`, handler code, optional assets, and any files materialized by the asset fetch phase.
- **Source identity**: Git URL, ref, and bundle subdirectory.
- **Resolved identity**: exact commit SHA plus bundle digest.
- **Asset**: a large binary file (ONNX model weights, voice pack, tokenizer vocabulary, etc.) declared in `manifest.yaml` and fetched separately from Git during `pull`.

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

Note: `install` does **not** fetch assets. Assets are fetched by `pull`.

### Pull flow

`gonnxctl pull <name>` (also called automatically before `load` if assets are missing):

1. Read `manifest.yaml` from the installed bundle directory.
2. For each entry in `assets[]`:
   a. Resolve destination path relative to the bundle directory.
   b. If the file exists and `sha256` matches — skip.
   c. Fetch the URL (with optional auth from environment).
   d. Verify `sha256` of the downloaded content.
   e. If `unpack` is set — extract to destination.
   f. Rename temp file to destination (atomic on same filesystem).
3. Emit a structured log entry for each asset action.

### Update flow

1. Fetch remote changes for the configured source.
2. Resolve the configured ref to a new commit SHA.
3. Materialize the new revision side-by-side.
4. Run bundle verification (including schema check of new manifest).
5. Re-run `pull` for assets declared in the new manifest.
6. Switch active revision only after verification succeeds.
7. Preserve the previous revision for rollback.

## Bundle layout

```
bundle/
  manifest.yaml           required
  handler.py              required (entrypoint declared in manifest)
  requirements.txt        optional
  examples/               optional
    request.json
  <asset dest paths>      materialized by `pull`, not committed to Git
```

Asset destination files (e.g. `model.onnx`, `voices.bin`) are written into the
bundle directory by the asset fetch phase. They are **not** tracked by Git.
The `.gitignore` of the bundle should list all asset `dest` paths.

## Manifest structure

See `examples/resnet50/manifest.yaml` for a complete annotated example.

Required top-level fields: `apiVersion`, `kind`, `name`, `version`, `runtime`, `handler`, `interface`, `policy`.

The `interface.inputSchema` and `interface.outputSchema` fields use JSON Schema 2020-12. The core daemon validates incoming requests against `inputSchema` before dispatching to the worker.

### `assets` section

`assets` is an optional top-level list. Each entry declares one binary file to
download during `pull`.

```yaml
assets:
  - id: model                    # symbolic name, referenced in validation messages
    url: https://...             # https://, s3://, gs://
    sha256: <64 hex chars>       # mandatory — integrity check and cache key
    size: 311296512              # optional — bytes, for progress reporting only
    dest: ./model.onnx           # path relative to bundle_dir where file is placed
    auth:                        # optional
      env: HF_TOKEN              # name of the env var carrying the bearer token
    unpack:                      # optional — only if the URL serves an archive
      format: tar.gz             # "tar.gz", "tar.bz2", "zip"
      strip: 1                   # strip N leading path components (like tar --strip)
```

**Rules:**

- `id` must be unique within the manifest.
- `sha256` is mandatory and is the sole cache key. If `sha256` matches the
  on-disk file, the file is not re-downloaded even if `url` changes.
- `dest` is relative to the bundle directory. Directory traversal (`../`) is
  rejected at parse time.
- `auth.env` names an environment variable. The token value is never stored in
  the manifest or the registry. If the variable is unset and the server returns
  401/403, the pull fails with an actionable error message.
- Asset files are written to a temporary path first, verified, then renamed
  atomically. A partial download never replaces a valid existing file.
- If any asset fails verification, `pull` aborts and the error is reported. No
  partially-updated state is left behind.

**Impact on `load`:** If any declared `dest` file is absent when `load` is
called, the daemon returns an error suggesting `gonnxctl pull <name>`.

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
| POST | /v1/models/{id}:pull | Fetch / verify assets |
| POST | /v1/models/{id}:update | Update configured source |
| GET | /v1/models/{id}/logs | Worker logs |
| GET | /metrics | Prometheus metrics |

## Security model

### Threat model

Bundle authors may be untrusted. A bundle includes executable handler code and
binary model artifacts fetched from external URLs. Installing a bundle is
equivalent to importing third-party code and data.

Main risks:
- remote code execution through handler code
- supply-chain attacks through compromised Git origins or asset URLs
- malicious Python dependencies
- local file or network exfiltration by handlers
- downgrade attacks if installs track moving branches without pinning
- asset substitution if `sha256` is omitted or truncated

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

### Asset verification

- `sha256` is mandatory. Manifests without `sha256` on an asset entry are
  rejected at parse time.
- The hash is verified server-side after download, before the file is placed at
  `dest`. A mismatch aborts pull and the temp file is deleted.
- Asset URLs are not subject to the worker network policy (pull runs in the
  daemon, not the worker). The operator may restrict asset fetch hosts in daemon
  configuration (allowlist by domain or CIDR) — planned for v1.
- Auth tokens come from environment variables only. They are never logged.

### Execution isolation

- Handler code runs in a separate worker process.
- Core daemon communicates only through declared HTTP-over-UDS interface.
- Dedicated unprivileged user for the daemon.
- `NO_NEW_PRIVS` where available.
- cgroup or systemd resource limits for CPU and memory.

### Network policy

Network egress disabled for workers by default. Manifest may request network
access, but operator approval required. Asset fetch runs in the daemon process
and is not subject to worker network restrictions.

### Filesystem policy

- Bundle files are read-only after installation.
- Workers receive an isolated writable scratch path for temporary files.
- Asset `dest` paths must be within the bundle directory. Paths containing `..`
  are rejected.

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

Every activation event emits structured log with: bundle name, origin URL,
resolved commit SHA, bundle digest, worker PID, trust level, security policy
overrides.

Every asset fetch event emits structured log with: bundle name, asset id, url
(without auth token), expected sha256, outcome (hit/fetch/error).

## CLI

```
gonnxctl install <git-url> --ref master --dir models/resnet50
gonnxctl pull <name>           # fetch / verify assets
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
4. Asset `dest` paths are within bundle directory (no traversal)
5. Asset `sha256` fields are present and 64-char hex
6. Input and output schemas parse
7. Bundle digest computed
8. Dependency environment created
9. Worker boots and answers `GET /health`
10. Worker returns valid `GET /describe`

Asset file presence is checked at `load` time, not at install time, because
assets may be large and are fetched separately by `pull`.

## Rollback

Updates stage the new revision side-by-side. Active revision pointer switches
only after verification. Previous revision retained for rollback. Rollback is a
metadata operation.

## v0 implementation priorities

1. Go daemon: install, load, predict, logs
2. Python worker SDK
3. Git install via system `git`: ref and dir support
4. JSON Schema validation at ingress
5. Asset fetch (`internal/assets`): plan, fetch, verify, unpack
6. ResNet-50 example bundle
7. Kokoro-TTS example bundle (uses assets for model weights)

## Open questions for v1

- Signed bundle manifests
- Signed commits / verified history
- Asset host allowlist in daemon config
- OCI export/import as alternative transport
- Streaming and multipart request support
- Multi-instance workers per bundle
- GPU-aware scheduling
- Namespace / seccomp sandboxing
- Windows and macOS isolation parity
