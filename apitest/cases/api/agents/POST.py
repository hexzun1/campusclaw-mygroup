"""POST /api/agents"""

from lib import actions, api, db


def test_teacher_creates_agent_and_database_matches(cfg, conn, tokens, prefix):
    name = f"{prefix}_agent"
    prompt = "你是接口测试用的、有耐心的助教。"
    agent_id = actions.create_agent(cfg, tokens["teacher_a"], name, prompt=prompt)

    row = db.fetch_one(
        conn,
        "SELECT name, prompt, enabled, class_id FROM tutor_agents WHERE id = %s",
        (agent_id,),
    )
    assert row is not None, "智能体未写入数据库"
    assert row["name"] == name
    assert row["prompt"] == prompt
    assert bool(row["enabled"]) is True

    teacher = db.fetch_one(conn, "SELECT class_id FROM users WHERE username = %s", ("teacher_a",))
    assert teacher is not None
    assert row["class_id"] == teacher["class_id"]

    listing = api.request(cfg, "GET", "/api/agents", token=tokens["teacher_a"])
    assert listing.status_code == 200, listing.text
    item = next((a for a in listing.json() if a["id"] == agent_id), None)
    assert item is not None, listing.text
    assert item == {
        "id": agent_id,
        "name": name,
        "prompt": prompt,
        "enabled": True,
        "skill_ids": [],
        "material_ids": [],
    }


def test_student_cannot_create_agent(cfg, tokens, prefix):
    resp = api.request(
        cfg,
        "POST",
        "/api/agents",
        token=tokens["student_a1"],
        json={"name": f"{prefix}_student_agent", "prompt": "", "enabled": True},
    )
    assert resp.status_code == 403, resp.text
