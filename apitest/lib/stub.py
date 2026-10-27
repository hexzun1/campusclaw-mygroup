"""Control the stub gateway that every gateway-dependent case runs against.

The stub is a test double, not part of the API under test: these helpers only
set what the model "decides" so a case can drive the host's tool-stage rules
(design.md Decision 14). When the stub is unreachable the case is skipped, the
same way `_skip_if_gateway_unavailable` treats a stream that answers 503.
"""

from __future__ import annotations

import os
from typing import Any, Sequence

import pytest
import requests

DEFAULT_BASE_URL = "http://127.0.0.1:8091"
TIMEOUT = 5


def base_url() -> str:
    return (os.environ.get("STUB_BASE_URL") or DEFAULT_BASE_URL).rstrip("/")


def _post(payload: dict[str, Any]) -> dict[str, Any]:
    try:
        resp = requests.post(base_url() + "/control", json=payload, timeout=TIMEOUT)
    except requests.RequestException as exc:
        pytest.skip(f"网关桩不可用：{exc}")
    assert resp.status_code == 200, f"桩控制接口返回 HTTP {resp.status_code}：{resp.text}"
    return resp.json()


def set_tool_calls(calls: Sequence[dict[str, str]]) -> None:
    """Force the next tool-stage request to propose exactly these calls."""
    _post({"tool_calls": list(calls)})


def reset_tool_calls() -> None:
    """Restore the stub's default tool-call behaviour."""
    _post({"reset_tool_calls": True})


def stats() -> dict[str, Any]:
    try:
        resp = requests.get(base_url() + "/stats", timeout=TIMEOUT)
    except requests.RequestException as exc:
        pytest.skip(f"网关桩不可用：{exc}")
    assert resp.status_code == 200, f"桩统计接口返回 HTTP {resp.status_code}：{resp.text}"
    return resp.json()
