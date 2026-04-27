"""Entry point for gonnx worker processes.

Invoked by the Go supervisor as:
    python3 -m gonnx.worker --entrypoint <path> --callable <name>

All other configuration is read from environment variables:
    GONNX_SOCKET       Unix socket path to listen on (required)
    GONNX_MODEL_PATH   Path to the .onnx model file (required)
    GONNX_BUNDLE_DIR   Bundle root directory (required)
    GONNX_PROVIDERS    Comma-separated ONNX execution providers
"""

from __future__ import annotations

import argparse
import logging
import os
import sys


def _parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(prog="python3 -m gonnx.worker")
    p.add_argument("--entrypoint", required=True, help="Path to handler script")
    p.add_argument("--callable", required=True, dest="callable_name",
                   help="Callable name inside the handler script")
    p.add_argument("--log-level", default="INFO",
                   choices=["DEBUG", "INFO", "WARNING", "ERROR"])
    return p.parse_args()


def main() -> None:
    args = _parse_args()
    logging.basicConfig(
        level=getattr(logging, args.log_level),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
        stream=sys.stderr,
    )

    sock = os.environ.get("GONNX_SOCKET")
    if not sock:
        sys.exit("GONNX_SOCKET is not set")

    from .context import WorkerContext
    from .server import serve

    ctx = WorkerContext.from_env()
    serve(
        ctx=ctx,
        entrypoint=args.entrypoint,
        callable_name=args.callable_name,
        sock_path=sock,
    )


if __name__ == "__main__":
    main()
