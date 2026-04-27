"""Minimal HTTP server over a Unix domain socket for gonnx workers.

Protocol
--------
GET  /health    -> 200 {"status": "ok", "model": "<path>"}
GET  /describe  -> 200 {"name": ..., "version": ..., "inputs": ..., "outputs": ...}
POST /predict   -> 200 <handler response body>
POST /shutdown  -> 204 (then process exits)

All request and response bodies are JSON.
The server listens exclusively on a Unix domain socket whose path is
taken from the GONNX_SOCKET environment variable.
"""

from __future__ import annotations

import importlib.util
import json
import logging
import os
import socket
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Any, Callable

from .context import WorkerContext, _NON_ONNX_ENGINES
from .types import Request, Response

log = logging.getLogger(__name__)

# Sentinel used by the shutdown handler to stop the serve loop.
_SHUTDOWN = False


class _UnixHTTPServer(HTTPServer):
	"""HTTPServer bound to a Unix domain socket instead of a TCP address."""

	address_family = socket.AF_UNIX

	def server_bind(self) -> None:
		"""Bind and set SO_REUSEADDR equivalent (remove stale socket file)."""
		if os.path.exists(self.server_address):
			os.remove(self.server_address)
		super().server_bind()
		os.chmod(self.server_address, 0o600)



def _make_handler(
	ctx: WorkerContext,
	callable_: Callable,
	manifest: dict[str, Any],
) -> type:
	"""Return a BaseHTTPRequestHandler subclass closed over ctx and callable_."""

	class _Handler(BaseHTTPRequestHandler):
		def log_message(self, fmt: str, *args: Any) -> None:  # silence default access log
			log.debug(fmt, *args)

		# --- routing ---------------------------------------------------

		def do_GET(self) -> None:
			if self.path == "/health":
				self._json(200, {"status": "ok", "model": ctx.model_path})
			elif self.path == "/describe":
				self._describe()
			else:
				self._json(404, {"error": "not found"})

		def do_POST(self) -> None:
			if self.path == "/predict":
				self._predict()
			elif self.path == "/shutdown":
				self._shutdown()
			else:
				self._json(404, {"error": "not found"})

		# --- handlers --------------------------------------------------

		def _describe(self) -> None:
			iface = manifest.get("interface", {})
			self._json(200, {
				"name":    manifest.get("name", ""),
				"version": manifest.get("version", ""),
				"inputs":  iface.get("inputSchema", {}),
				"outputs": iface.get("outputSchema", {}),
			})

		def _predict(self) -> None:
			length = int(self.headers.get("Content-Length", 0))
			raw = self.rfile.read(length) if length else b"{}"
			try:
				body = json.loads(raw)
			except json.JSONDecodeError as exc:
				self._json(400, {"error": f"invalid JSON: {exc}"})
				return

			req = Request(
				body=body,
				headers=dict(self.headers),
			)
			try:
				result = callable_(req, ctx)
			except Exception as exc:  # pylint: disable=broad-except
				log.exception("handler raised an exception")
				self._json(500, {"error": str(exc)})
				return

			if isinstance(result, Response):
				for k, v in result.headers.items():
					self.send_header(k, v)
				self._json(result.status, result.body)
			else:
				# Plain dict / list shorthand.
				self._json(200, result)

		def _shutdown(self) -> None:
			global _SHUTDOWN
			_SHUTDOWN = True
			self.send_response(204)
			self.end_headers()

		# --- helpers ---------------------------------------------------

		def _json(self, status: int, data: Any) -> None:
			payload = json.dumps(data).encode()
			self.send_response(status)
			self.send_header("Content-Type", "application/json")
			self.send_header("Content-Length", str(len(payload)))
			self.end_headers()
			self.wfile.write(payload)

	return _Handler


def _load_callable(entrypoint: str, callable_name: str) -> Callable:
	"""Import a Python module from a file path and return the named callable."""
	spec = importlib.util.spec_from_file_location("_gonnx_handler", entrypoint)
	if spec is None or spec.loader is None:
		raise ImportError(f"cannot load handler from {entrypoint!r}")
	mod = importlib.util.module_from_spec(spec)
	spec.loader.exec_module(mod)  # type: ignore[union-attr]
	callable_ = getattr(mod, callable_name, None)
	if callable_ is None:
		raise AttributeError(
			f"handler module {entrypoint!r} has no attribute {callable_name!r}"
		)
	if not callable(callable_):
		raise TypeError(f"{callable_name!r} in {entrypoint!r} is not callable")
	return callable_


def _load_manifest(bundle_dir: str) -> dict[str, Any]:
	path = os.path.join(bundle_dir, "manifest.yaml")
	try:
		import yaml  # type: ignore[import]
		with open(path) as f:
			return yaml.safe_load(f) or {}
	except Exception:
		return {}


def serve(
	ctx: WorkerContext,
	entrypoint: str,
	callable_name: str,
	sock_path: str,
) -> None:
	"""Load the handler and serve requests until /shutdown is called."""
	global _SHUTDOWN
	_SHUTDOWN = False

	log.info("loading handler %s:%s", entrypoint, callable_name)
	callable_ = _load_callable(entrypoint, callable_name)

	# Eagerly load the ONNX session so startup errors surface before /health.
	# Skip for non-ONNX engines (e.g. torch) — their handlers load the model
	# themselves inside ModelWorker.load().
	if ctx.engine not in _NON_ONNX_ENGINES:
		_ = ctx.session

	manifest = _load_manifest(ctx.bundle_dir)

	handler_cls = _make_handler(ctx, callable_, manifest)
	server = _UnixHTTPServer(sock_path, handler_cls)

	log.info("worker listening on %s", sock_path)

	while not _SHUTDOWN:
		server.handle_request()

	server.server_close()
	log.info("worker shutdown complete")
