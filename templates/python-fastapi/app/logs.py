"""Logs as JSON lines on stdout: one line per request, and whatever the app logs.

configure() sends every logger's records, uvicorn's own included, to stdout as
one JSON object per line, at LOG_LEVEL (INFO when unset), and turns uvicorn's
plain-text access log off: log_request writes that line instead.
"""

import json
import logging
import os
import re
import sys
import time
import uuid
from collections.abc import Awaitable, Callable
from datetime import UTC, datetime
from typing import TextIO, override

from fastapi import Request, Response

REQUEST_ID_HEADER = "X-Request-Id"

# An incoming X-Request-Id is kept only when it looks like an id; anything else
# is replaced, so a caller can't write arbitrary text into the log.
_REQUEST_ID = re.compile(r"[A-Za-z0-9._:-]{1,128}")

logger = logging.getLogger("app.request")


class JSONFormatter(logging.Formatter):
    """Format a record as one JSON object: time, level, logger, message, fields."""

    @override
    def format(self, record: logging.LogRecord) -> str:
        """Return the record as a JSON line, with the fields passed in extra."""
        line: dict[str, object] = {
            "time": datetime.fromtimestamp(record.created, UTC).isoformat(timespec="milliseconds"),
            "level": record.levelname,
            "logger": record.name,
            "message": record.getMessage(),
        }
        fields = getattr(record, "fields", None)
        if isinstance(fields, dict):
            line.update(fields)
        if record.exc_info:
            line["exception"] = self.formatException(record.exc_info)
        return json.dumps(line, ensure_ascii=False, default=str)


def configure(stream: TextIO | None = None) -> None:
    """Log JSON lines to stream (stdout) at LOG_LEVEL, and turn off uvicorn's access log."""
    handler = logging.StreamHandler(stream or sys.stdout)
    handler.setFormatter(JSONFormatter())
    level = os.environ.get("LOG_LEVEL", "INFO").upper()
    logging.basicConfig(handlers=[handler], level=level, force=True)
    # uvicorn configures its loggers before it imports the app; hand them to
    # the root logger so its startup and error lines are JSON too.
    for name in ("uvicorn", "uvicorn.error"):
        uvicorn_logger = logging.getLogger(name)
        uvicorn_logger.handlers.clear()
        uvicorn_logger.propagate = True
    access = logging.getLogger("uvicorn.access")
    access.handlers.clear()
    access.propagate = False
    access.disabled = True


def request_id(request: Request) -> str:
    """Return the caller's X-Request-Id when it looks like an id, else a new one."""
    given = request.headers.get(REQUEST_ID_HEADER, "")
    if _REQUEST_ID.fullmatch(given):
        return given
    return uuid.uuid4().hex


async def log_request(
    request: Request, call_next: Callable[[Request], Awaitable[Response]]
) -> Response:
    """Answer the request, echo its request id and log one line for it.

    The line has the method, the path without its query string, the status,
    the duration in milliseconds and the request id; never a body, a header
    value or the query string.
    """
    started = time.perf_counter()
    rid = request_id(request)
    try:
        response = await call_next(request)
    except Exception:
        _log(request, 500, started, rid)
        raise
    response.headers[REQUEST_ID_HEADER] = rid
    _log(request, response.status_code, started, rid)
    return response


def _log(request: Request, status: int, started: float, rid: str) -> None:
    logger.info(
        "%s %s %d",
        request.method,
        request.url.path,
        status,
        extra={
            "fields": {
                "method": request.method,
                "path": request.url.path,
                "status": status,
                "durationMs": round((time.perf_counter() - started) * 1000, 3),
                "requestId": rid,
            }
        },
    )
