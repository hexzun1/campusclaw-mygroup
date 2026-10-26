"""Configuration for the interface tests.

Every value comes from the environment (optionally seeded by apitest/.env).
Missing values are reported by name only; secrets are never echoed.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path

APITEST_DIR = Path(__file__).resolve().parent.parent
ENV_FILE = APITEST_DIR / ".env"

REQUIRED = (
    "APITEST_DB_HOST",
    "APITEST_DB_PORT",
    "APITEST_DB_USER",
    "APITEST_DB_PASSWORD",
    "APITEST_DB_NAME",
    "MCP_AUTH_TOKEN",
    "APITEST_TEACHER_A_PASSWORD",
    "APITEST_STUDENT_A1_PASSWORD",
    "APITEST_STUDENT_B1_PASSWORD",
)

DEFAULT_API_BASE_URL = "http://localhost:8080"


class ConfigError(RuntimeError):
    """The test configuration is unusable; the message lists variable names."""


@dataclass(frozen=True)
class Config:
    api_base_url: str
    db_host: str
    db_port: int
    db_user: str
    db_password: str
    db_name: str
    mcp_auth_token: str
    teacher_a_password: str
    student_a1_password: str
    student_b1_password: str


def load_env(path: Path = ENV_FILE) -> None:
    """Load KEY=VALUE lines from apitest/.env without overriding real env vars."""
    if not path.is_file():
        return
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[len("export ") :]
        key, sep, value = line.partition("=")
        if not sep:
            continue
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        if key:
            os.environ.setdefault(key, value)


def load_config() -> Config:
    load_env()

    missing = [name for name in REQUIRED if not os.environ.get(name)]
    if missing:
        raise ConfigError(
            "缺少必需的环境变量："
            + "、".join(missing)
            + "。请复制 apitest/.env.example 为 apitest/.env 并填写，"
            "或先在 shell 中导出这些变量。"
        )

    raw_port = os.environ["APITEST_DB_PORT"].strip()
    try:
        db_port = int(raw_port)
    except ValueError as exc:
        raise ConfigError(f"APITEST_DB_PORT 必须是整数，当前值为 {raw_port!r}") from exc
    if not 0 < db_port < 65536:
        raise ConfigError(f"APITEST_DB_PORT 超出端口范围：{db_port}")

    base_url = os.environ.get("API_BASE_URL", "").strip() or DEFAULT_API_BASE_URL

    return Config(
        api_base_url=base_url.rstrip("/"),
        db_host=os.environ["APITEST_DB_HOST"].strip(),
        db_port=db_port,
        db_user=os.environ["APITEST_DB_USER"].strip(),
        db_password=os.environ["APITEST_DB_PASSWORD"],
        db_name=os.environ["APITEST_DB_NAME"].strip(),
        mcp_auth_token=os.environ["MCP_AUTH_TOKEN"],
        teacher_a_password=os.environ["APITEST_TEACHER_A_PASSWORD"],
        student_a1_password=os.environ["APITEST_STUDENT_A1_PASSWORD"],
        student_b1_password=os.environ["APITEST_STUDENT_B1_PASSWORD"],
    )
