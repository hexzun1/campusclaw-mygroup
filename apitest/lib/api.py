"""HTTP helpers: bearer requests and login."""

from __future__ import annotations

from typing import Any

import requests

from .config import Config

DEFAULT_TIMEOUT = 30
STREAM_TIMEOUT = (10, 180)


def request(
    cfg: Config,
    method: str,
    path: str,
    *,
    token: str | None = None,
    timeout: Any = DEFAULT_TIMEOUT,
    **kwargs: Any,
) -> requests.Response:
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    return requests.request(
        method,
        cfg.api_base_url + path,
        headers=headers,
        timeout=timeout,
        **kwargs,
    )


def login(cfg: Config, username: str, password: str) -> str:
    resp = request(cfg, "POST", "/api/login", json={"username": username, "password": password})
    if resp.status_code != 200:
        raise AssertionError(f"登录 {username} 失败：HTTP {resp.status_code} {resp.text}")
    body = resp.json()
    token = body.get("token")
    if not isinstance(token, str) or not token:
        raise AssertionError(f"登录 {username} 的响应没有 token：{body}")
    return token


def assert_no_secret(resp: requests.Response, secret: str) -> None:
    """The response (body and headers) must never contain the raw credential."""
    assert secret not in resp.text, f"响应体出现令牌原文：{resp.text}"
    for name, value in resp.headers.items():
        assert secret not in value, f"响应头 {name} 出现令牌原文"
