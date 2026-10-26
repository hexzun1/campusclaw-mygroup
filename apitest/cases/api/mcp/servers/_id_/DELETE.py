"""DELETE /api/mcp/servers/{id}"""

from lib import actions, api, data, db


def _server_exists(conn, server_id):
    row = db.fetch_one(conn, "SELECT id FROM mcp_servers WHERE id = %s", (server_id,))
    return row is not None


def test_delete_unreferenced_server(cfg, conn, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-del"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    resp = api.request(
        cfg, "DELETE", f"/api/mcp/servers/{server['id']}", token=tokens["teacher_a"]
    )
    assert resp.status_code == 204, resp.text
    assert resp.content == b""
    assert not _server_exists(conn, server["id"])

    listing = api.request(cfg, "GET", "/api/mcp/servers", token=tokens["teacher_a"])
    assert listing.status_code == 200
    assert all(item["id"] != server["id"] for item in listing.json())


def test_referenced_server_requires_detaching_skill(cfg, conn, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-ref"
    skill_name = f"{prefix}_delskill"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)
    actions.connect_server(cfg, tokens["teacher_a"], server["id"])
    skill_id = actions.create_skill(
        cfg,
        tokens["teacher_a"],
        data.skill_source(skill_name, "删除引用测试", tools=f"formula_hint@{name}"),
    )

    resp = api.request(
        cfg, "DELETE", f"/api/mcp/servers/{server['id']}", token=tokens["teacher_a"]
    )
    assert resp.status_code == 409, resp.text
    body = resp.json()
    assert body["error"], resp.text
    assert skill_name in body["skills"], body
    assert _server_exists(conn, server["id"])

    # DELETE /api/skills/{id} answers 200 {"ok":true} (spec: 技能管理 only
    # requires success; swagger documents 200).
    removed = api.request(cfg, "DELETE", f"/api/skills/{skill_id}", token=tokens["teacher_a"])
    assert removed.status_code == 200, removed.text
    assert removed.json() == {"ok": True}, removed.text

    again = api.request(
        cfg, "DELETE", f"/api/mcp/servers/{server['id']}", token=tokens["teacher_a"]
    )
    assert again.status_code == 204, again.text
    assert not _server_exists(conn, server["id"])


def test_student_cannot_delete(cfg, conn, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-studel"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    resp = api.request(
        cfg, "DELETE", f"/api/mcp/servers/{server['id']}", token=tokens["student_a1"]
    )
    assert resp.status_code == 403, resp.text
    assert _server_exists(conn, server["id"])


def test_other_class_delete_is_identical_404(cfg, conn, tokens, teacher_b, prefix):
    name = f"{data.server_prefix(prefix)}-crossdel"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    other = api.request(
        cfg, "DELETE", f"/api/mcp/servers/{server['id']}", token=teacher_b["token"]
    )
    missing = api.request(cfg, "DELETE", "/api/mcp/servers/999999", token=teacher_b["token"])
    assert other.status_code == 404, other.text
    assert (other.status_code, other.content) == (missing.status_code, missing.content)
    assert _server_exists(conn, server["id"])
