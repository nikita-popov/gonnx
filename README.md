# gonnx

Git-first ONNX model runtime. Load any ONNX bundle from a Git repository and serve it over a unified HTTP API — without depending on a central catalog.

## Concept

- **Any ONNX model** — not just LLMs. CV, ASR, NLP, tabular — anything packaged as an ONNX bundle.
- **Git as distribution** — install a model directly from any Git repo, pinned to a commit SHA.
- **Handler = your code** — model authors ship a small handler alongside the ONNX artifact. The runtime loads it as an isolated subprocess.
- **One stable endpoint** — `POST /v1/models/{name}:predict` regardless of model domain.

## Architecture overview

```
client
  │  HTTP
  ▼
gonnxd (core daemon)          ← registry, scheduler, supervisor
  │  HTTP over Unix socket
  ▼
worker process                ← handler.py + ONNX Runtime session
```

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
  rfc-v0.md       architecture RFC
```

## Quickstart

```sh
# start daemon
gonnxd --addr :7860

# install a bundle directly from the examples repo
gonnxctl install https://github.com/nikita-popov/examples-gonnx.git --dir resnet50

# load worker
gonnxctl load resnet50

# run inference (reads JSON from stdin)
echo '{"image": "'$(base64 -w0 cat.jpg)'", "top_k": 3}' | gonnxctl run resnet50
```

## Bundle examples

Ready-to-use bundles live in a separate repository:
**[nikita-popov/examples-gonnx](https://github.com/nikita-popov/examples-gonnx)**

| Bundle | Task | Model |
|--------|------|-------|
| [resnet50](https://github.com/nikita-popov/examples-gonnx/tree/master/resnet50) | Image classification (ImageNet-1k) | ResNet-50 |

## Status

Early development. See [docs/rfc-v0.md](docs/rfc-v0.md) for the full design.

## License

MIT © Nikita Popov
