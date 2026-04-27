"""WorkerContext — lazy ONNX Runtime session wrapper."""

from __future__ import annotations

import os
from typing import Sequence


class WorkerContext:
    """Provides access to the ONNX Runtime session and bundle metadata.

    An instance is created once at worker startup and passed to the
    handler callable on every predict call.

    Attributes
    ----------
    model_path:  absolute path to the .onnx model file
    bundle_dir:  absolute path to the materialized bundle directory
    providers:   ordered list of ONNX execution provider names
    """

    def __init__(
        self,
        model_path: str,
        bundle_dir: str,
        providers: list[str],
    ) -> None:
        self.model_path = model_path
        self.bundle_dir = bundle_dir
        self.providers = providers
        self._session = None

    # ------------------------------------------------------------------
    # Lazy session access
    # ------------------------------------------------------------------

    @property
    def session(self):
        """Return the onnxruntime.InferenceSession, loading it on first access."""
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
        return cls(model_path=model_path, bundle_dir=bundle_dir, providers=providers)
