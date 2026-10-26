"""Thin pymysql helpers used only for assertions and test-data cleanup."""

from __future__ import annotations

from typing import Any, Mapping, Sequence

import pymysql
from pymysql.cursors import DictCursor

from .config import Config


def connect(cfg: Config) -> pymysql.connections.Connection:
    try:
        conn = pymysql.connect(
            host=cfg.db_host,
            port=cfg.db_port,
            user=cfg.db_user,
            password=cfg.db_password,
            database=cfg.db_name,
            charset="utf8mb4",
            cursorclass=DictCursor,
            autocommit=True,
            connect_timeout=10,
        )
    except pymysql.MySQLError as exc:
        # Never include the password: pymysql's message only names host/user.
        raise RuntimeError(
            f"无法连接测试数据库 {cfg.db_host}:{cfg.db_port}/{cfg.db_name}：{exc}。"
            "请确认已用 docker-compose.dev.yml 启动开发编排（db 映射为 127.0.0.1:3307）。"
        ) from exc
    return conn


def fetch_one(
    conn: pymysql.connections.Connection,
    sql: str,
    params: Sequence[Any] = (),
) -> Mapping[str, Any] | None:
    with conn.cursor() as cur:
        cur.execute(sql, params)
        return cur.fetchone()


def fetch_all(
    conn: pymysql.connections.Connection,
    sql: str,
    params: Sequence[Any] = (),
) -> list[Mapping[str, Any]]:
    with conn.cursor() as cur:
        cur.execute(sql, params)
        return list(cur.fetchall())


def execute(
    conn: pymysql.connections.Connection,
    sql: str,
    params: Sequence[Any] = (),
) -> int:
    with conn.cursor() as cur:
        cur.execute(sql, params)
        return cur.rowcount
