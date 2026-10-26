"""Parsing for the tutor stream endpoint's SSE frames."""

from __future__ import annotations

import json
from typing import Any, Iterator

import requests


def iter_events(response: requests.Response) -> Iterator[tuple[str, Any]]:
    """Yield (event name, decoded data) for each SSE frame.

    The server writes one JSON object per frame:
    `event: <name>\\ndata: <json>\\n\\n`.
    """
    event: str | None = None
    data_lines: list[str] = []

    # Decode as UTF-8 ourselves: `decode_unicode=True` falls back to the
    # response's declared charset, and `text/event-stream` carries none, so
    # requests would decode it as ISO-8859-1 and mangle every Chinese delta.
    # Splitting on b"\n" is safe because UTF-8 never puts that byte inside a
    # multi-byte sequence.
    for raw in response.iter_lines(decode_unicode=False):
        line = "" if raw is None else raw.decode("utf-8").rstrip("\r")
        if line == "":
            if event is not None:
                payload = "\n".join(data_lines)
                yield event, json.loads(payload) if payload else None
            event, data_lines = None, []
            continue
        if line.startswith(":"):
            continue
        field, sep, value = line.partition(":")
        if not sep:
            continue
        if field == "event":
            event = value.strip()
        elif field == "data":
            data_lines.append(value.lstrip())

    if event is not None:
        payload = "\n".join(data_lines)
        yield event, json.loads(payload) if payload else None


def collect(response: requests.Response) -> list[tuple[str, Any]]:
    return list(iter_events(response))
