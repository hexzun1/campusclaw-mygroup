"""POST /api/conversations/{id}/stream"""

import json

import pytest

from lib import actions, api, db, sse


def _skip_if_gateway_unavailable(resp):
    content_type = resp.headers.get("Content-Type", "")
    if resp.status_code == 503 and "text/event-stream" not in content_type:
        resp.close()
        pytest.skip("对话网关不可用：流式接口在事件流开始前返回 503")


def _create_conversation(cfg, tokens, prefix, suffix):
    agent_id = actions.create_agent(cfg, tokens["teacher_a"], f"{prefix}_{suffix}_agent")
    conv = actions.create_conversation(cfg, tokens["student_a1"], agent_id)
    return conv["id"]


def _db_messages(conn, conversation_id):
    return db.fetch_all(
        conn,
        "SELECT id, role, content, citations_json, skill_name FROM messages "
        "WHERE conversation_id = %s ORDER BY id",
        (conversation_id,),
    )


@pytest.mark.gateway
def test_stream_event_order_and_persisted_consistency(cfg, conn, tokens, prefix):
    conversation_id = _create_conversation(cfg, tokens, prefix, "stream")
    question = "请简单介绍一下这个测试会话。"

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
        headers = resp.headers
        assert headers.get("Content-Type", "").startswith("text/event-stream")
        assert headers.get("Cache-Control") == "no-cache"
        assert headers.get("Connection") == "keep-alive"
        assert headers.get("X-Accel-Buffering") == "no"

        events = sse.collect(resp)
    finally:
        resp.close()

    names = [name for name, _ in events]
    assert names[0] == "meta", names
    assert names[-1] == "done", names
    assert "error" not in names, names
    middle = names[1:-1]
    assert middle, names
    assert set(middle) == {"delta"}, names

    meta = events[0][1]
    assert meta["status"] == "ok"
    assert meta["skill"] is None
    assert meta["citations"] == []
    assert "tool_calls" not in meta, meta

    text = "".join(payload["text"] for name, payload in events if name == "delta")
    message_id = events[-1][1]["message_id"]

    row = db.fetch_one(conn, "SELECT role, content FROM messages WHERE id = %s", (message_id,))
    assert row is not None, f"done.message_id={message_id} 不在 messages 表中"
    assert row["role"] == "assistant"
    assert row["content"] == text

    detail = api.request(cfg, "GET", f"/api/conversations/{conversation_id}", token=tokens["student_a1"])
    assert detail.status_code == 200, detail.text
    api_messages = detail.json()["messages"]
    db_messages = _db_messages(conn, conversation_id)
    assert len(api_messages) == len(db_messages) == 2

    for api_message, db_message in zip(api_messages, db_messages):
        assert api_message["id"] == db_message["id"]
        assert api_message["role"] == db_message["role"]
        assert api_message["content"] == db_message["content"]
        assert api_message["skill_name"] == (db_message["skill_name"] or "")
        citations = db_message["citations_json"]
        if isinstance(citations, (bytes, bytearray)):
            citations = citations.decode("utf-8")
        if isinstance(citations, str):
            citations = json.loads(citations)
        assert api_message["citations"] == (citations or [])

    assert db_messages[0]["role"] == "user"
    assert db_messages[0]["content"] == question


def test_stream_without_token_is_401(cfg):
    resp = api.request(
        cfg,
        "POST",
        "/api/conversations/999999/stream",
        json={"question": "你好"},
    )
    assert resp.status_code == 401, resp.text


def test_stream_rejects_short_question(cfg, tokens, prefix):
    conversation_id = _create_conversation(cfg, tokens, prefix, "short")
    resp = api.request(
        cfg,
        "POST",
        f"/api/conversations/{conversation_id}/stream",
        token=tokens["student_a1"],
        json={"question": "a"},
    )
    assert resp.status_code == 400, resp.text
    assert resp.headers.get("Content-Type", "").startswith("application/json")
