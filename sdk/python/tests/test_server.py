"""Unit tests for the gonnx worker HTTP server.

All tests use a real Unix socket but a fake ONNX session,
so onnxruntime is not required to run the test suite.
"""

from __future__ import annotations

import json
import socket
import threading
import time
from http.client import HTTPConnection
from typing import Any
from unittest.mock import MagicMock, patch

import pytest

from gonnx.context import WorkerContext
from gonnx.server import serve
from gonnx.types import Request, Response


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

class _UnixHTTPConnection(HTTPConnection):
    """HTTPConnection that dials a Unix socket."""

    def __init__(self, sock_path: str) -> None:
        super().__init__("localhost")
        self._sock_path = sock_path

    def connect(self) -> None:
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.connect(self._sock_path)
        self.sock = s


def _request(sock: str, method: str, path: str, body: Any = None) -> tuple[int, Any]:
    conn = _UnixHTTPConnection(sock)
    headers = {"Content-Type": "application/json"} if body is not None else {}
    encoded = json.dumps(body).encode() if body is not None else None
    if encoded:
        headers["Content-Length"] = str(len(encoded))
    conn.request(method, path, body=encoded, headers=headers)
    resp = conn.getresponse()
    status = resp.status
    data = json.loads(resp.read())
    conn.close()
    return status, data


def _wait_connectable(sock_path: str, timeout: float = 5.0, interval: float = 0.05) -> None:
    """Block until the Unix socket actually accepts connections.

    Probing connect() is the only reliable signal that the server has called
    listen() and is ready.  Checking os.path.exists() is not sufficient because
    the socket file is created at bind() time, before listen().
    """
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            s.connect(sock_path)
            s.close()
            return
        except OSError:
            time.sleep(interval)
    raise RuntimeError(f"server on {sock_path} did not become ready within {timeout}s")


def _start_server(sock: str, handler_fn, manifest: dict | None = None) -> threading.Thread:
    """Start a gonnx worker server in a background thread."""
    ctx = WorkerContext(
        model_path="/fake/model.onnx",
        bundle_dir="/fake/bundle",
        providers=["CPUExecutionProvider"],
    )
    # Patch _load_session so onnxruntime is never imported.
    ctx._session = MagicMock()

    manifest = manifest or {"name": "test", "version": "0.1.0"}

    def _target():
        with patch("gonnx.server._load_manifest", return_value=manifest), \
             patch("gonnx.server._load_callable", return_value=handler_fn):
            serve(
                ctx=ctx,
                entrypoint="handler.py",
                callable_name="app",
                sock_path=sock,
            )

    t = threading.Thread(target=_target, daemon=True)
    t.start()
    _wait_connectable(sock)
    return t


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture
def sock_path(tmp_path):
    return str(tmp_path / "worker.sock")


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

def test_health(sock_path):
    _start_server(sock_path, lambda req, ctx: {})
    status, data = _request(sock_path, "GET", "/health")
    assert status == 200
    assert data["status"] == "ok"


def test_describe(sock_path):
    manifest = {"name": "my-model", "version": "1.2.3",
                "interface": {"inputSchema": {"type": "object"}, "outputSchema": {"type": "object"}}}
    _start_server(sock_path, lambda req, ctx: {}, manifest=manifest)
    status, data = _request(sock_path, "GET", "/describe")
    assert status == 200
    assert data["name"] == "my-model"
    assert data["version"] == "1.2.3"


def test_predict_plain_dict(sock_path):
    def handler(req: Request, ctx: WorkerContext):
        return {"echo": req.body}

    _start_server(sock_path, handler)
    status, data = _request(sock_path, "POST", "/predict", body={"x": 42})
    assert status == 200
    assert data["echo"]["x"] == 42


def test_predict_response_object(sock_path):
    def handler(req: Request, ctx: WorkerContext):
        return Response(body={"result": "ok"}, status=201)

    _start_server(sock_path, handler)
    status, data = _request(sock_path, "POST", "/predict", body={})
    assert status == 201
    assert data["result"] == "ok"


def test_predict_handler_exception(sock_path):
    def handler(req: Request, ctx: WorkerContext):
        raise ValueError("something went wrong")

    _start_server(sock_path, handler)
    status, data = _request(sock_path, "POST", "/predict", body={})
    assert status == 500
    assert "something went wrong" in data["error"]


def test_predict_invalid_json(sock_path):
    _start_server(sock_path, lambda req, ctx: {})
    conn = _UnixHTTPConnection(sock_path)
    conn.request("POST", "/predict", body=b"not-json",
                 headers={"Content-Type": "application/json", "Content-Length": "8"})
    resp = conn.getresponse()
    assert resp.status == 400
    conn.close()


def test_shutdown(sock_path):
    _start_server(sock_path, lambda req, ctx: {})
    # First confirm health.
    status, _ = _request(sock_path, "GET", "/health")
    assert status == 200
    # Send shutdown.
    conn = _UnixHTTPConnection(sock_path)
    conn.request("POST", "/shutdown")
    resp = conn.getresponse()
    assert resp.status == 204
    conn.close()


def test_unknown_route(sock_path):
    _start_server(sock_path, lambda req, ctx: {})
    status, data = _request(sock_path, "GET", "/no-such-path")
    assert status == 404
