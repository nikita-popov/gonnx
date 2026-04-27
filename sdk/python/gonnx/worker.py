"""Base class for gonnx model workers.

A worker author subclasses ModelWorker and implements:
  - load(ctx)       — called once at startup; create ONNX Runtime sessions here
  - describe()      — return static metadata (optional override)
  - predict(req)    — called for every inference request
  - unload()        — called before shutdown (optional override)

The SDK starts a minimal HTTP server on the Unix socket path provided
via the GONNX_SOCKET environment variable.
"""
from __future__ import annotations

import abc
import dataclasses
import os
from typing import Any


@dataclasses.dataclass
class WorkerContext:
    model_path: str
    bundle_dir: str
    providers: list[str]
    scratch_dir: str

    def asset(self, relative_path: str) -> str:
        """Resolve a path relative to the bundle directory."""
        return os.path.join(self.bundle_dir, relative_path)


@dataclasses.dataclass
class Request:
    json: dict[str, Any]


@dataclasses.dataclass
class Response:
    json: dict[str, Any]
    status: int = 200


class ModelWorker(abc.ABC):
    """Base class for all gonnx workers."""

    @abc.abstractmethod
    def load(self, ctx: WorkerContext) -> None:
        """Load model assets. Called once at startup."""

    @abc.abstractmethod
    def predict(self, req: Request) -> Response | dict[str, Any]:
        """Run inference. Called for every request."""

    def unload(self) -> None:
        """Optional cleanup before shutdown."""

    def describe(self) -> dict[str, Any]:
        """Return static worker metadata."""
        return {}

    def run(self) -> None:
        """Start the worker HTTP server. Called by the SDK entry point."""
        raise NotImplementedError("worker HTTP server not yet implemented")
