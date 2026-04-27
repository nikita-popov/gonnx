"""Public types shared between the SDK and handler code."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass
class Request:
    """A single prediction request forwarded from the daemon."""

    # Parsed JSON body as a plain Python dict.
    body: dict[str, Any] = field(default_factory=dict)
    # HTTP headers forwarded from the original client request.
    headers: dict[str, str] = field(default_factory=dict)

    @property
    def json(self) -> dict[str, Any]:
        """Alias for body — convenience for handler authors."""
        return self.body


@dataclass
class Response:
    """A prediction response returned by the handler."""

    # Must be JSON-serialisable.
    body: Any = None
    # HTTP status code (defaults to 200).
    status: int = 200
    # Optional extra response headers.
    headers: dict[str, str] = field(default_factory=dict)
