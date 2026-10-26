"""GET /api/mcp/calls"""

from lib import actions, api, data, db

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
