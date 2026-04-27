"""ModelWorker — base class for class-based gonnx handlers.

Usage in handler.py::

    from gonnx import ModelWorker, Request, WorkerContext

    class MyWorker(ModelWorker):
        def load(self, ctx: WorkerContext) -> None:
            # called once before the first request
            ...

        def predict(self, req: Request) -> dict:
            ...

    app = MyWorker()

The ``app`` object is callable: ``app(req, ctx)`` routes to ``load`` + ``predict``.
The server calls ``app(req, ctx)`` for every incoming /predict request.
"""

from __future__ import annotations

from abc import ABC, abstractmethod

from .context import WorkerContext
from .types import Request


class ModelWorker(ABC):
    """Abstract base for class-based gonnx inference handlers."""

    _loaded: bool = False

    def __call__(self, req: Request, ctx: WorkerContext) -> object:
        """Called by the server for every /predict request.

        On the first call ``load`` is invoked, then ``predict`` is called for
        every subsequent request (including the first one).
        """
        if not self._loaded:
            self.load(ctx)
            self._loaded = True
        return self.predict(req)

    @abstractmethod
    def load(self, ctx: WorkerContext) -> None:
        """Load the model. Called exactly once before the first predict."""

    @abstractmethod
    def predict(self, req: Request) -> object:
        """Run inference and return a JSON-serialisable result."""
