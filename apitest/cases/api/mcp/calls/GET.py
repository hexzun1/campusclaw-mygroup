"""GET /api/mcp/calls"""

import json

import pytest

from lib import actions, api, data, db, sse, stub

CALL_KEYS = {
    "id",
    "tool",
    "status",
    "reason",
    "username",
    "conversation_id",
    "round",
    "duration_ms",
    "created_at",
}


def _insert_call(conn, *, conversation_id, server_name, tool, status, reason=None):
    student = db.fetch_one(conn, "SELECT id, class_id FROM users WHERE username = %s", ("student_a1",))
    assert student is not None
    db.execute(
        conn,
        "INSERT INTO mcp_tool_calls "
        "(class_id, user_id, conversation_id, server_id, server, tool, status, reason, `round`, duration_ms) "
        "VALUES (%s, %s, %s, NULL, %s, %s, %s, %s, 1, 15)",
        (student["class_id"], student["id"], conversation_id, server_name, tool, status, reason),
    )
    row = db.fetch_one(
        conn,
        "SELECT id, class_id, user_id, conversation_id, server, tool, status, "
        "COALESCE(reason, '') AS reason, `round`, duration_ms "
        "FROM mcp_tool_calls WHERE conversation_id = %s AND tool = %s ORDER BY id DESC LIMIT 1",
        (conversation_id, tool),
    )
    assert row is not None, "审计记录未写入数据库"
    return row


def _test_conversation(cfg, tokens, prefix, suffix):
    # The agent name must be unique per test: class-scoped agent names are
    # unique, so two tests sharing one name would collide (spec: 测试数据隔离).
    agent_id = actions.create_agent(cfg, tokens["teacher_a"], f"{prefix}_calls_agent_{suffix}")
    return actions.create_conversation(cfg, tokens["student_a1"], agent_id)


def test_teacher_lists_class_calls_and_database_matches(cfg, conn, tokens, prefix):
    conversation = _test_conversation(cfg, tokens, prefix, "list")
    server_name = f"{data.server_prefix(prefix)}-audit"
    row = _insert_call(
        conn,
        conversation_id=conversation["id"],
        server_name=server_name,
        tool="formula_hint",
        status="ok",
    )

    resp = api.request(cfg, "GET", "/api/mcp/calls", token=tokens["teacher_a"])
    assert resp.status_code == 200, resp.text
    api.assert_no_secret(resp, cfg.mcp_auth_token)

    item = next((c for c in resp.json() if c["id"] == row["id"]), None)
    assert item is not None, "审计列表中没有数据库中的记录"
    expected = set(CALL_KEYS)
    if not item.get("reason"):
        expected.discard("reason")
    assert set(item) == expected, item
    assert item["tool"] == f"formula_hint@{server_name}"
    assert item["status"] == "ok"
    assert item["username"] == "student_a1"
    assert item["conversation_id"] == conversation["id"]
    assert item["round"] == 1
    assert item["duration_ms"] == 15
    assert item["created_at"]

    first = api.request(cfg, "GET", "/api/mcp/calls", token=tokens["teacher_a"], params={"limit": 1})
    assert first.status_code == 200, first.text
    assert len(first.json()) == 1, first.text
    assert first.json()[0]["id"] == row["id"], "新记录必须排在列表第一位"

    max_limit = api.request(
        cfg, "GET", "/api/mcp/calls", token=tokens["teacher_a"], params={"limit": 200}
    )
    assert max_limit.status_code == 200, max_limit.text


def test_invalid_limit_returns_400(cfg, tokens):
    for value in (0, 201, "abc"):
        resp = api.request(
            cfg, "GET", "/api/mcp/calls", token=tokens["teacher_a"], params={"limit": value}
        )
        assert resp.status_code == 400, f"limit={value}: {resp.status_code} {resp.text}"
        assert resp.json().get("error"), resp.text


def test_student_cannot_list_calls(cfg, tokens):
    resp = api.request(cfg, "GET", "/api/mcp/calls", token=tokens["student_a1"])
    assert resp.status_code == 403, resp.text


def test_requires_login(cfg):
    resp = api.request(cfg, "GET", "/api/mcp/calls")
    assert resp.status_code == 401, resp.text


def test_other_class_teacher_only_sees_own_class(cfg, conn, tokens, teacher_b, prefix):
    conversation = _test_conversation(cfg, tokens, prefix, "otherclass")
    server_name = f"{data.server_prefix(prefix)}-auditb"
    row = _insert_call(
        conn,
        conversation_id=conversation["id"],
        server_name=server_name,
        tool="unit_convert",
        status="denied",
        reason="not_connected",
    )

    resp = api.request(cfg, "GET", "/api/mcp/calls", token=teacher_b["token"])
    assert resp.status_code == 200, resp.text
    calls = resp.json()
    assert all(c["id"] != row["id"] for c in calls), "B 班教师看到了 A 班的审计记录"
    assert all(c["conversation_id"] != conversation["id"] for c in calls)

    b_usernames = {
        u["username"]
        for u in db.fetch_all(
            conn,
            "SELECT username FROM users WHERE class_id = (SELECT id FROM classes WHERE name = %s)",
            ("B班",),
        )
    }
    assert {c["username"] for c in calls} <= b_usernames, calls


@pytest.mark.gateway
def test_overlong_function_name_is_truncated_in_the_audit(cfg, conn, tokens, prefix):
    """spec 「调用审计」Scenario 超长函数名不影响同轮审计 (design D2).

    The model can name a function nobody declared. The record must still land,
    with the tool column cut to its 64-character width.
    """
    long_name = "x" * 100
    stub.set_tool_calls([{"name": long_name, "arguments": "{}"}])
    try:
        server = actions.register_server(
            cfg, tokens["teacher_a"], f"{data.server_prefix(prefix)}-trunc", cfg.mcp_auth_token
        )
        actions.connect_server(cfg, tokens["teacher_a"], server["id"])
        skill_id = actions.create_skill(
            cfg,
            tokens["teacher_a"],
            data.skill_source(f"{prefix}_trunc", "勾股定理", tools=f"formula_hint@{server['name']}"),
        )
        agent_id = actions.create_agent(
            cfg, tokens["teacher_a"], f"{prefix}_trunc", skill_ids=[skill_id]
        )
        conversation = actions.create_conversation(cfg, tokens["student_a1"], agent_id)
        conversation_id = conversation["id"]

        resp = api.request(
            cfg,
            "POST",
            f"/api/conversations/{conversation_id}/stream",
            token=tokens["student_a1"],
            json={"question": "勾股定理怎么用？"},
            stream=True,
            timeout=api.STREAM_TIMEOUT,
        )
        try:
            content_type = resp.headers.get("Content-Type", "")
            if resp.status_code == 503 and "text/event-stream" not in content_type:
                pytest.skip("对话网关不可用：流式接口在事件流开始前返回 503")
            assert resp.status_code == 200, resp.text
            events = sse.collect(resp)
        finally:
            resp.close()

        meta = events[0][1]
        names = {name for name, _ in events}
        assert "error" not in names, meta
        denied = [c for c in meta.get("tool_calls") or [] if c.get("tool") == long_name]
        assert denied, f"meta.tool_calls 中没有超长函数名的记录：{meta}"
        assert denied[0]["status"] == "denied", denied[0]
        assert denied[0]["reason"] == "not_allowed", denied[0]

        listing = api.request(
            cfg, "GET", "/api/mcp/calls", token=tokens["teacher_a"], params={"limit": 1}
        )
        assert listing.status_code == 200, listing.text
        first = listing.json()[0]
        assert first["conversation_id"] == conversation_id, first
        tool_part, _, server_part = first["tool"].partition("@")
        assert tool_part == "x" * 64, f"工具名未截断到 64 个字符：{first['tool']!r}"
        assert server_part == "", first
        assert first["status"] == "denied"
        assert first["reason"] == "not_allowed"

        row = db.fetch_one(
            conn,
            "SELECT CHAR_LENGTH(tool) AS tool_len, status, reason FROM mcp_tool_calls "
            "WHERE conversation_id = %s ORDER BY id DESC LIMIT 1",
            (conversation_id,),
        )
        assert row is not None, "超长函数名的记录未写入 mcp_tool_calls"
        assert int(row["tool_len"]) == 64, row
        assert row["status"] == "denied" and row["reason"] == "not_allowed", row
    finally:
        stub.reset_tool_calls()
