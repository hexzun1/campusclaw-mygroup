"""Small API actions shared by several test files; each asserts its own success."""

from __future__ import annotations

import os
from typing import Any, Sequence

from . import api
from .config import Config

DEFAULT_MCP_BASE_URL = "http://mcp-demo:8090"


def mcp_base_url() -> str:
    """Where the demo MCP server answers. The compose service name by default;
    override with MCP_SERVER_BASE_URL when the stack runs outside that network."""
    return (os.environ.get("MCP_SERVER_BASE_URL") or DEFAULT_MCP_BASE_URL).rstrip("/")


def create_agent(
    cfg: Config,
    token: str,
    name: str,
    *,
    prompt: str = "你是接口测试用的助教。",
    enabled: bool = True,
    skill_ids: Sequence[int] = (),
    material_ids: Sequence[int] = (),
) -> int:
    resp = api.request(
        cfg,
        "POST",
        "/api/agents",
        token=token,
        json={
            "name": name,
            "prompt": prompt,
            "enabled": enabled,
            "skill_ids": list(skill_ids),
            "material_ids": list(material_ids),
        },
    )
    assert resp.status_code == 201, f"创建智能体失败：HTTP {resp.status_code} {resp.text}"
    return int(resp.json()["id"])


def create_skill(cfg: Config, token: str, source: str) -> int:
    resp = api.request(cfg, "POST", "/api/skills", token=token, json={"source": source})
    assert resp.status_code == 201, f"创建技能失败：HTTP {resp.status_code} {resp.text}"
    return int(resp.json()["id"])


def create_conversation(cfg: Config, token: str, agent_id: int) -> dict[str, Any]:
    resp = api.request(cfg, "POST", "/api/conversations", token=token, json={"agent_id": agent_id})
    assert resp.status_code == 201, f"创建会话失败：HTTP {resp.status_code} {resp.text}"
    return resp.json()


def register_server(
    cfg: Config,
    token: str,
    name: str,
    token_value: str,
    *,
    base_url: str | None = None,
) -> dict[str, Any]:
    resp = api.request(
        cfg,
        "POST",
        "/api/mcp/servers",
        token=token,
        json={"name": name, "base_url": base_url or mcp_base_url(), "token": token_value},
    )
    assert resp.status_code == 201, f"登记服务器失败：HTTP {resp.status_code} {resp.text}"
    return resp.json()


def connect_server(cfg: Config, token: str, server_id: int) -> list[dict[str, Any]]:
    resp = api.request(cfg, "POST", f"/api/mcp/servers/{server_id}/connect", token=token)
    assert resp.status_code == 200, f"连接服务器失败：HTTP {resp.status_code} {resp.text}"
    return resp.json()


def disconnect_server(cfg: Config, token: str, server_id: int) -> None:
    resp = api.request(cfg, "POST", f"/api/mcp/servers/{server_id}/disconnect", token=token)
    assert resp.status_code == 204, f"断开服务器失败：HTTP {resp.status_code} {resp.text}"


def delete_server(cfg: Config, token: str, server_id: int) -> None:
    resp = api.request(cfg, "DELETE", f"/api/mcp/servers/{server_id}", token=token)
    assert resp.status_code == 204, f"删除服务器失败：HTTP {resp.status_code} {resp.text}"
