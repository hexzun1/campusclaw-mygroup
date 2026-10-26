"""End-to-end MCP flow: connect, route, call, audit, disconnect then deny."""

import json

import pytest

from lib import actions, api, data, db, sse

SERVER_NAME = "campus-tools"
TOOL_REF = f"formula_hint@{SERVER_NAME}"
TRIGGER = "勾股定理"


def _skip_if_gateway_unavailable(resp):
    content_type = resp.headers.get("Content-Type", "")
    if resp.status_code == 503 and "text/event-stream" not in content_type:
        resp.close()
        pytest.skip("对话网关不可用：流式接口在事件流开始前返回 503")


def _stream(cfg, tokens, conversation_id, question):
    resp = api.request(
        cfg,
        "POST",
        f"/api/conversations/{conversation_id}/stream",
        token=tokens["student_a1"],
        json={"question": question},
        stream=True,
        timeout=api.STREAM_TIMEOUT,
    )
    try:
        _skip_if_gateway_unavailable(resp)
        assert resp.status_code == 200, resp.text
        events = sse.collect(resp)
    finally:
        resp.close()

    names = [name for name, _ in events]
    assert names, "没有收到任何 SSE 事件"
    assert names[0] == "meta", names
    assert names[-1] == "done", names
    assert "error" not in names, names
    assert set(names[1:-1]) == {"delta"}, names

    meta = events[0][1]
    text = "".join(payload["text"] for name, payload in events if name == "delta")
    return meta, text


def _resolve_campus_tools(cfg, conn, tokens):
    listing = api.request(cfg, "GET", "/api/mcp/servers", token=tokens["teacher_a"])
    assert listing.status_code == 200, listing.text
    for item in listing.json():
        if item["name"] == SERVER_NAME:
            row = db.fetch_one(
                conn,
                "SELECT id, status, last_error FROM mcp_servers WHERE id = %s",
                (item["id"],),
            )
            assert row is not None
            return item, dict(row), False

    server = actions.register_server(
        cfg, tokens["teacher_a"], SERVER_NAME, cfg.mcp_auth_token
    )
    return server, {"status": "disconnected", "last_error": None}, True


def _restore_campus_tools(cfg, conn, tokens, server, original, created, skill_id):
    if created:
        if skill_id is not None:
            api.request(cfg, "DELETE", f"/api/skills/{skill_id}", token=tokens["teacher_a"])
        api.request(
            cfg, "DELETE", f"/api/mcp/servers/{server['id']}", token=tokens["teacher_a"]
        )
        return

    row = db.fetch_one(conn, "SELECT status FROM mcp_servers WHERE id = %s", (server["id"],))
    current = row["status"] if row else None
    if original["status"] == "connected":
        if current != "connected":
            actions.connect_server(cfg, tokens["teacher_a"], server["id"])
    elif original["status"] == "error":
        if current != "error":
            db.execute(
                conn,
                "UPDATE mcp_servers SET status = 'error', last_error = %s, "
                "tools_refreshed_at = NULL WHERE id = %s",
                (original["last_error"], server["id"]),
            )
            db.execute(conn, "DELETE FROM mcp_tools WHERE server_id = %s", (server["id"],))
    elif current != "disconnected":
        actions.disconnect_server(cfg, tokens["teacher_a"], server["id"])


def _audit_rows(conn, conversation_id):
    return db.fetch_all(
        conn,
        "SELECT tool, server, status, COALESCE(reason, '') AS reason, `round`, duration_ms "
        "FROM mcp_tool_calls WHERE conversation_id = %s ORDER BY id",
        (conversation_id,),
    )


@pytest.mark.gateway
def test_mcp_tool_flow_and_disconnect_denies(cfg, conn, tokens, prefix):
    server, original, created = _resolve_campus_tools(cfg, conn, tokens)
    skill_id = None
    agent_id = None
    try:
        actions.connect_server(cfg, tokens["teacher_a"], server["id"])

        skill_name = f"{prefix}_e2e_skill"
        skill_id = actions.create_skill(
            cfg,
            tokens["teacher_a"],
            data.skill_source(skill_name, TRIGGER, tools=TOOL_REF),
        )
        agent_id = actions.create_agent(
            cfg, tokens["teacher_a"], f"{prefix}_e2e_tool_agent", skill_ids=[skill_id]
        )
        conversation = actions.create_conversation(cfg, tokens["student_a1"], agent_id)
        conversation_id = conversation["id"]

        meta, text = _stream(cfg, tokens, conversation_id, "请给我一个勾股定理的公式提示。")
        calls = meta.get("tool_calls")
        if not calls:
            pytest.fail(
                "模型未调用工具，本轮 meta 为：" + json.dumps(meta, ensure_ascii=False)
            )
        ok = [c for c in calls if c.get("tool") == TOOL_REF]
        assert ok, f"meta.tool_calls 中没有 {TOOL_REF}：{meta}"
        assert ok[0]["status"] == "ok", f"工具调用状态不是 ok：{meta}"
        assert ok[0]["round"] == 1, meta
        assert isinstance(ok[0].get("duration_ms"), int), meta
        assert meta["status"] == "ok", meta
        assert meta["skill"] == skill_name, meta
        assert text, "回答正文为空"

        listing = api.request(
            cfg, "GET", "/api/mcp/calls", token=tokens["teacher_a"], params={"limit": 200}
        )
        assert listing.status_code == 200, listing.text
        item = next(
            (
                c
                for c in listing.json()
                if c["conversation_id"] == conversation_id
                and c["tool"] == TOOL_REF
            ),
            None,
        )
        assert item is not None, "审计接口中没有本次工具调用"
        assert item["status"] == "ok", item
        assert item["username"] == "student_a1", item
        assert item["round"] == 1, item

        db_ok = [r for r in _audit_rows(conn, conversation_id) if r["status"] == "ok"]
        assert db_ok, "数据库 mcp_tool_calls 中没有 ok 记录"
        assert db_ok[0]["tool"] == "formula_hint"
        assert db_ok[0]["server"] == SERVER_NAME
        assert db_ok[0]["round"] == 1

        actions.disconnect_server(cfg, tokens["teacher_a"], server["id"])
        meta2, text2 = _stream(cfg, tokens, conversation_id, "再给我一个勾股定理的提示。")
        denied = [c for c in meta2.get("tool_calls") or [] if c.get("tool") == TOOL_REF]
        assert denied, f"断开后 meta.tool_calls 中没有 {TOOL_REF}：{meta2}"
        assert denied[0]["status"] == "denied", meta2
        assert denied[0].get("reason") == "not_connected", meta2
        assert denied[0]["round"] == 0, meta2
        assert meta2["status"] == "ok", meta2
        assert text2, "断开后回答正文为空"

        listing2 = api.request(
            cfg, "GET", "/api/mcp/calls", token=tokens["teacher_a"], params={"limit": 200}
        )
        assert listing2.status_code == 200, listing2.text
        audit_denied = [
            c
            for c in listing2.json()
            if c["conversation_id"] == conversation_id
            and c["tool"] == TOOL_REF
            and c["status"] == "denied"
        ]
        assert audit_denied, "审计接口中没有 denied 记录"
        assert audit_denied[0]["reason"] == "not_connected", audit_denied[0]

        db_denied = [r for r in _audit_rows(conn, conversation_id) if r["status"] == "denied"]
        assert db_denied, "数据库 mcp_tool_calls 中没有 denied 记录"
        assert db_denied[0]["reason"] == "not_connected", db_denied[0]
    finally:
        _restore_campus_tools(cfg, conn, tokens, server, original, created, skill_id)
