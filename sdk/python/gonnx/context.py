"""WorkerContext — lazy ONNX Runtime session wrapper."""

from __future__ import annotations

import os
from typing import Sequence

# Engines that do not use onnxruntime at all.
# For these, ctx.session raises AttributeError with a clear message instead of
# trying to import onnxruntime (which would not be installed in the venv).
_NON_ONNX_ENGINES = frozenset({"torch", "torch_jit"})


class WorkerContext:
    """Provides access to the ONNX Runtime session and bundle metadata.

    An instance is created once at worker startup and passed to the
    handler callable on every predict call.

    Attributes
    ----------
    model_path:  absolute path to the model file (.onnx or .pt)
    bundle_dir:  absolute path to the materialized bundle directory
    providers:   ordered list of execution provider names
    engine:      value of runtime.engine from manifest.yaml
                 ("onnxruntime" by default, "torch" for TorchScript models)
    """

    def __init__(
        self,
        model_path: str,
        bundle_dir: str,
        providers: list[str],
        engine: str = "onnxruntime",
    ) -> None:
        self.model_path = model_path
        self.bundle_dir = bundle_dir
        self.providers = providers
        self.engine = engine
        self._session = None

    # ------------------------------------------------------------------
    # Lazy session access
    # ------------------------------------------------------------------

    @property
    def session(self):
        """Return the onnxruntime.InferenceSession, loading it on first access.

        Raises AttributeError if the bundle uses a non-ONNX engine (e.g. torch).
        Handlers for such bundles should use ctx.model_path directly and load
        the model themselves (e.g. torch.jit.load(ctx.model_path)).
        """
        if self.engine in _NON_ONNX_ENGINES:
            raise AttributeError(
                f"ctx.session is not available for engine={self.engine!r}. "
                "Load the model directly via ctx.model_path "
                "(e.g. torch.jit.load(ctx.model_path))."
            )
        if self._session is None:
            self._session = self._load_session()
        return self._session

    def _load_session(self):
        try:
            import onnxruntime as ort  # type: ignore[import]
        except ImportError as exc:
            raise RuntimeError(
                "onnxruntime is not installed. "
                "Add it to the bundle requirements.txt or install "
                "gonnx[cpu] / gonnx[gpu]."
            ) from exc

        return ort.InferenceSession(
            self.model_path,
            providers=self.providers,
        )

    # ------------------------------------------------------------------
    # Convenience helpers
    # ------------------------------------------------------------------

    def asset(self, rel_path: str) -> str:
        """Return the absolute path to a file inside the bundle directory."""
        return os.path.join(self.bundle_dir, rel_path)

    @classmethod
    def from_env(cls) -> "WorkerContext":
        """Build a WorkerContext from the standard GONNX_* environment variables."""
        model_path = os.environ["GONNX_MODEL_PATH"]
        bundle_dir = os.environ["GONNX_BUNDLE_DIR"]
        raw_providers = os.environ.get("GONNX_PROVIDERS", "CPUExecutionProvider")
        providers = [p.strip() for p in raw_providers.split(",") if p.strip()]
        engine = os.environ.get("GONNX_ENGINE", "onnxruntime")
        return cls(
            model_path=model_path,
            bundle_dir=bundle_dir,
            providers=providers,
            engine=engine,
        )
