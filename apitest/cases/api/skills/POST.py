"""POST /api/skills"""

import json

from lib import actions, api, data, db


def _tools(raw):
    if isinstance(raw, (bytes, bytearray)):
        raw = raw.decode("utf-8")
    return json.loads(raw)


def test_teacher_creates_skill_and_database_matches(cfg, conn, tokens, prefix):
    name = f"{prefix}_skill"
    trigger = "测试触发词，接口测试"
    body = "这是接口测试技能的说明正文。"
    source = data.skill_source(name, trigger, tools="knowledge_search", subject="数学", body=body)

    skill_id = actions.create_skill(cfg, tokens["teacher_a"], source)

    row = db.fetch_one(conn, "SELECT * FROM skills WHERE id = %s", (skill_id,))
    assert row is not None, "技能未写入数据库"
    assert row["name"] == name
    assert row["trigger_text"] == trigger
    assert row["subject_text"] == "数学"
    assert _tools(row["tools_json"]) == ["knowledge_search"]
    assert row["description"] == body
    assert row["source_text"] == source
    assert bool(row["enabled"]) is True

    listing = api.request(cfg, "GET", "/api/skills", token=tokens["teacher_a"])
    assert listing.status_code == 200, listing.text
    item = next((s for s in listing.json() if s["id"] == skill_id), None)
    assert item is not None, listing.text
    assert item["name"] == name
    assert item["trigger_text"] == trigger
    assert item["subject_text"] == "数学"
    assert item["tools"] == ["knowledge_search"]
    assert item["description"] == body
    assert item["source_text"] == source
    assert item["enabled"] is True


def test_invalid_skill_file_returns_400_and_writes_nothing(cfg, conn, tokens, prefix):
    name = f"{prefix}_bad"
    source = f"名称：{name}\n名称：{name}\n适用条件：测试\n\n正文\n"
    resp = api.request(cfg, "POST", "/api/skills", token=tokens["teacher_a"], json={"source": source})
    assert resp.status_code == 400, resp.text
    assert resp.json().get("error"), resp.text

    row = db.fetch_one(conn, "SELECT COUNT(*) AS n FROM skills WHERE name = %s", (name,))
    assert row is not None and int(row["n"]) == 0


def test_student_cannot_create_skill(cfg, tokens, prefix):
    source = data.skill_source(f"{prefix}_stu_skill", "测试")
    resp = api.request(cfg, "POST", "/api/skills", token=tokens["student_a1"], json={"source": source})
    assert resp.status_code == 403, resp.text


def test_skill_referencing_tool_on_disconnected_server_returns_400(cfg, conn, tokens, prefix):
    server_name = f"{data.server_prefix(prefix)}-off"
    server = actions.register_server(cfg, tokens["teacher_a"], server_name, "apitest-dummy-token")
    try:
        name = f"{prefix}_off"
        source = data.skill_source(name, "测试", tools=f"formula_hint@{server_name}")
        resp = api.request(cfg, "POST", "/api/skills", token=tokens["teacher_a"], json={"source": source})
        assert resp.status_code == 400, resp.text
        error = resp.json().get("error", "")
        assert "工具" in error and "未连接" in error, error

        row = db.fetch_one(conn, "SELECT COUNT(*) AS n FROM skills WHERE name = %s", (name,))
        assert row is not None and int(row["n"]) == 0
    finally:
        actions.delete_server(cfg, tokens["teacher_a"], server["id"])
