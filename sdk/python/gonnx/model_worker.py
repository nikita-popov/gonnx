"""ModelWorker — base class for class-based gonnx handlers.

Usage in handler.py::

    from gonnx import ModelWorker, Request, WorkerContext

    class MyWorker(ModelWorker):
        def load(self, ctx: WorkerContext) -> None:
            # called once at worker startup, before the first request
            ...

        def predict(self, req: Request) -> dict:
            ...

    app = MyWorker()

The ``app`` object is callable: ``app(req, ctx)`` routes to ``predict``.
The server calls ``app.startup(ctx)`` once at process start (before /health),
then ``app(req, ctx)`` for every incoming /predict request.
"""

from __future__ import annotations

from abc import ABC, abstractmethod

from .context import WorkerContext
from .types import Request


class ModelWorker(ABC):
    """Abstract base for class-based gonnx inference handlers."""

    def startup(self, ctx: WorkerContext) -> None:
        """Called once by the server at process start, before /health.

        Invokes ``load`` and marks the worker as ready.  Startup errors
        propagate immediately and crash the process so gonnxd can report
        a clean failure instead of a 502 on the first predict.
        """
        self.load(ctx)
        self._loaded = True

    def __call__(self, req: Request, ctx: WorkerContext) -> object:
        """Called by the server for every /predict request."""
        return self.predict(req)

    @abstractmethod
    def load(self, ctx: WorkerContext) -> None:
        """Load the model. Called exactly once at startup."""

    @abstractmethod
    def predict(self, req: Request) -> object:
        """Run inference and return a JSON-serialisable result."""
