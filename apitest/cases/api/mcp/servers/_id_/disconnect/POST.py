"""POST /api/mcp/servers/{id}/disconnect"""

from lib import actions, api, data, db


def _server_row(conn, server_id):
    return db.fetch_one(
        conn,
        "SELECT status, last_error, tools_refreshed_at FROM mcp_servers WHERE id = %s",
        (server_id,),
    )


def test_disconnect_clears_cache(cfg, conn, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-disc"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)
    actions.connect_server(cfg, tokens["teacher_a"], server["id"])

    resp = api.request(
        cfg, "POST", f"/api/mcp/servers/{server['id']}/disconnect", token=tokens["teacher_a"]
    )
    assert resp.status_code == 204, resp.text
    assert resp.content == b""

    row = _server_row(conn, server["id"])
    assert row["status"] == "disconnected"
    assert row["last_error"] in (None, "")
    assert row["tools_refreshed_at"] is None

    tools = api.request(
        cfg, "GET", f"/api/mcp/servers/{server['id']}/tools", token=tokens["teacher_a"]
    )
    assert tools.status_code == 200
    assert tools.json() == []

    listing = api.request(cfg, "GET", "/api/mcp/servers", token=tokens["teacher_a"])
    assert listing.status_code == 200
    item = next(s for s in listing.json() if s["id"] == server["id"])
    assert item["status"] == "disconnected"
    assert item["tool_count"] == 0


def test_student_cannot_disconnect(cfg, conn, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-stud"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    resp = api.request(
        cfg, "POST", f"/api/mcp/servers/{server['id']}/disconnect", token=tokens["student_a1"]
    )
    assert resp.status_code == 403, resp.text
    assert _server_row(conn, server["id"])["status"] == "disconnected"


def test_other_class_disconnect_is_identical_404(cfg, conn, tokens, teacher_b, prefix):
    name = f"{data.server_prefix(prefix)}-crossd"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)
    actions.connect_server(cfg, tokens["teacher_a"], server["id"])

    other = api.request(
        cfg, "POST", f"/api/mcp/servers/{server['id']}/disconnect", token=teacher_b["token"]
    )
    missing = api.request(
        cfg, "POST", "/api/mcp/servers/999999/disconnect", token=teacher_b["token"]
    )
    assert other.status_code == 404, other.text
    assert (other.status_code, other.content) == (missing.status_code, missing.content)
    assert _server_row(conn, server["id"])["status"] == "connected"
