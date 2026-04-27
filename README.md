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
examples/
  resnet50/       image classification bundle example
docs/
  rfc-v0.md       architecture RFC
```

## Quickstart

```sh
# start daemon
gonnxd serve

# install a bundle from Git
gonnxctl install https://github.com/example/vision-models.git --ref master --dir models/resnet50

# run inference
gonnxctl run resnet50 -f examples/resnet50/examples/request.json
```

## Status

Early development. See [docs/rfc-v0.md](docs/rfc-v0.md) for the full design.

## License

MIT © Nikita Popov
