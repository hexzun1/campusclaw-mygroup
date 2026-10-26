"""Test-data prefixes, fixtures helpers and FK-safe cleanup."""

from __future__ import annotations

from datetime import datetime
from typing import Any, Mapping, Sequence

import pymysql

from . import db

# Users, agents and skills may contain underscores; MCP server names may not
# (^[a-z][a-z0-9-]{0,31}$), so they use the hyphenated form of the same prefix.
DATA_PREFIXES = ("apitest_",)
SERVER_PREFIXES = ("apitest-",)

PRESET_TABLES = (
    "classes",
    "users",
    "materials",
    "knowledge_entries",
    "knowledge_chunks",
    "tutor_agents",
    "skills",
    "mcp_servers",
    "mcp_tools",
)


def new_prefix() -> str:
    return "apitest_" + datetime.now().strftime("%Y%m%d%H%M%S")


def server_prefix(prefix: str) -> str:
    return prefix.replace("_", "-", 1)


def skill_source(
    name: str,
    trigger: str,
    *,
    tools: str = "knowledge_search",
    subject: str = "数学",
    enabled: str = "是",
    body: str = "用于接口测试的技能说明。",
) -> str:
    return (
        f"名称：{name}\n"
        f"适用条件：{trigger}\n"
        f"资料：{subject}\n"
        f"工具：{tools}\n"
        f"启用：{enabled}\n"
        "\n"
        f"{body}\n"
    )


def _ids_matching(
    conn: pymysql.connections.Connection,
    table: str,
    column: str,
    prefixes: Sequence[str],
) -> list[int]:
    clauses: list[str] = []
    params: list[Any] = []
    for prefix in prefixes:
        clauses.append(f"SUBSTRING({column}, 1, %s) = %s")
        params.extend([len(prefix), prefix])
    rows = db.fetch_all(
        conn,
        f"SELECT id FROM {table} WHERE " + " OR ".join(clauses),
        tuple(params),
    )
    return [int(row["id"]) for row in rows]


def _placeholders(values: Sequence[Any]) -> str:
    return ",".join(["%s"] * len(values))


def _delete_by_ids(
    conn: pymysql.connections.Connection,
    table: str,
    ids: Sequence[int],
) -> None:
    if ids:
        db.execute(conn, f"DELETE FROM {table} WHERE id IN ({_placeholders(ids)})", tuple(ids))


def cleanup_test_data(conn: pymysql.connections.Connection) -> None:
    """Remove every row this suite can create, in foreign-key-safe order.

    Conversations are identified through their agent: every test that creates
    one uses an agent with the test prefix, so seeded agents are never touched.
    """
    user_ids = _ids_matching(conn, "users", "username", DATA_PREFIXES)
    agent_ids = _ids_matching(conn, "tutor_agents", "name", DATA_PREFIXES)
    skill_ids = _ids_matching(conn, "skills", "name", DATA_PREFIXES)
    server_ids = _ids_matching(conn, "mcp_servers", "name", SERVER_PREFIXES)

    conversation_ids: list[int] = []
    conv_clauses: list[str] = []
    conv_params: list[Any] = []
    if agent_ids:
        conv_clauses.append(f"agent_id IN ({_placeholders(agent_ids)})")
        conv_params.extend(agent_ids)
    if user_ids:
        conv_clauses.append(f"user_id IN ({_placeholders(user_ids)})")
        conv_params.extend(user_ids)
    if conv_clauses:
        rows = db.fetch_all(
            conn,
            "SELECT id FROM conversations WHERE " + " OR ".join(conv_clauses),
            tuple(conv_params),
        )
        conversation_ids = [int(row["id"]) for row in rows]

    call_clauses: list[str] = []
    call_params: list[Any] = []
    if conversation_ids:
        call_clauses.append(f"conversation_id IN ({_placeholders(conversation_ids)})")
        call_params.extend(conversation_ids)
    if server_ids:
        call_clauses.append(f"server_id IN ({_placeholders(server_ids)})")
        call_params.extend(server_ids)
    if user_ids:
        call_clauses.append(f"user_id IN ({_placeholders(user_ids)})")
        call_params.extend(user_ids)
    if call_clauses:
        db.execute(
            conn,
            "DELETE FROM mcp_tool_calls WHERE " + " OR ".join(call_clauses),
            tuple(call_params),
        )

    _delete_by_ids(conn, "conversations", conversation_ids)
    _delete_by_ids(conn, "tutor_agents", agent_ids)
    _delete_by_ids(conn, "skills", skill_ids)
    _delete_by_ids(conn, "mcp_servers", server_ids)
    _delete_by_ids(conn, "users", user_ids)


def assert_no_test_data(conn: pymysql.connections.Connection) -> None:
    leftovers: dict[str, int] = {}
    for table, column, prefixes, label in (
        ("users", "username", DATA_PREFIXES, "账号"),
        ("tutor_agents", "name", DATA_PREFIXES, "智能体"),
        ("skills", "name", DATA_PREFIXES, "技能"),
        ("mcp_servers", "name", SERVER_PREFIXES, "服务器"),
    ):
        clauses: list[str] = []
        params: list[Any] = []
        for prefix in prefixes:
            clauses.append(f"SUBSTRING({column}, 1, %s) = %s")
            params.extend([len(prefix), prefix])
        row = db.fetch_one(conn, f"SELECT COUNT(*) AS n FROM {table} WHERE " + " OR ".join(clauses), tuple(params))
        count = int(row["n"]) if row else 0
        if count:
            leftovers[label] = count
    assert not leftovers, f"测试数据未清理干净：{leftovers}"


def snapshot_counts(conn: pymysql.connections.Connection) -> dict[str, int]:
    counts: dict[str, int] = {}
    for table in PRESET_TABLES:
        row = db.fetch_one(conn, f"SELECT COUNT(*) AS n FROM {table}")
        counts[table] = int(row["n"]) if row else 0
    return counts


def diff_counts(before: Mapping[str, int], after: Mapping[str, int]) -> dict[str, tuple[int, int]]:
    return {
        table: (before[table], after[table])
        for table in before
        if before[table] != after[table]
    }


def create_temp_teacher_b(
    conn: pymysql.connections.Connection,
    prefix: str,
) -> tuple[int, str]:
    teacher = db.fetch_one(
        conn,
        "SELECT id, password_hash FROM users WHERE username = %s",
        ("teacher_a",),
    )
    assert teacher is not None, "预置账号 teacher_a 不存在"
    class_b = db.fetch_one(conn, "SELECT id FROM classes WHERE name = %s", ("B班",))
    assert class_b is not None, "预置班级 B班 不存在"

    username = f"{prefix}_teacher_b"
    db.execute(
        conn,
        "INSERT INTO users (username, password_hash, role, class_id) VALUES (%s, %s, 'teacher', %s)",
        (username, teacher["password_hash"], class_b["id"]),
    )
    row = db.fetch_one(conn, "SELECT id FROM users WHERE username = %s", (username,))
    assert row is not None
    return int(row["id"]), username
