"""POST /api/conversations"""

from lib import actions, api, db


def test_create_conversation_and_read_it_back(cfg, conn, tokens, prefix):
    agent_id = actions.create_agent(cfg, tokens["teacher_a"], f"{prefix}_conv_agent")
    conv = actions.create_conversation(cfg, tokens["student_a1"], agent_id)

    assert set(conv) == {"id", "agent_id", "title", "created_at", "updated_at"}, conv
    assert conv["agent_id"] == agent_id

    row = db.fetch_one(
        conn,
        "SELECT class_id, user_id, agent_id, title FROM conversations WHERE id = %s",
        (conv["id"],),
    )
    assert row is not None, "会话未写入数据库"
    assert row["agent_id"] == agent_id
    assert row["title"] == conv["title"]

    student = db.fetch_one(conn, "SELECT id, class_id FROM users WHERE username = %s", ("student_a1",))
    assert student is not None
    assert row["user_id"] == student["id"]
    assert row["class_id"] == student["class_id"]

    listing = api.request(cfg, "GET", "/api/conversations", token=tokens["student_a1"])
    assert listing.status_code == 200, listing.text
    assert any(c["id"] == conv["id"] for c in listing.json()), listing.text

    detail = api.request(cfg, "GET", f"/api/conversations/{conv['id']}", token=tokens["student_a1"])
    assert detail.status_code == 200, detail.text
    body = detail.json()
    assert body["id"] == conv["id"]
    assert body["agent_id"] == agent_id
    assert body["title"] == conv["title"]
    assert body["messages"] == []


def test_missing_agent_id_returns_400(cfg, tokens):
    resp = api.request(cfg, "POST", "/api/conversations", token=tokens["student_a1"], json={})
    assert resp.status_code == 400, resp.text


def test_create_conversation_without_token_is_401(cfg):
    resp = api.request(cfg, "POST", "/api/conversations", json={"agent_id": 1})
    assert resp.status_code == 401, resp.text
