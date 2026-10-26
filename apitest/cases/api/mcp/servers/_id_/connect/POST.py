"""POST /api/mcp/servers/{id}/connect"""

import json

from lib import actions, api, data, db


def _server_row(conn, server_id):
    return db.fetch_one(
        conn,
        "SELECT status, last_error, tools_refreshed_at FROM mcp_servers WHERE id = %s",
        (server_id,),
    )


def _tool_rows(conn, server_id):
    return db.fetch_all(
        conn,
        "SELECT name, description, input_schema FROM mcp_tools "
        "WHERE server_id = %s ORDER BY name",
        (server_id,),
    )


def test_connect_discovers_tools_and_database_matches(cfg, conn, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-conn"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    resp = api.request(
        cfg, "POST", f"/api/mcp/servers/{server['id']}/connect", token=tokens["teacher_a"]
    )
    assert resp.status_code == 200, resp.text
    api.assert_no_secret(resp, cfg.mcp_auth_token)

    tools = resp.json()
    assert isinstance(tools, list), tools
    by_name = {tool["name"]: tool for tool in tools}
    assert set(by_name) == {"formula_hint", "unit_convert"}, tools
    for tool in tools:
        assert set(tool) == {"name", "description", "input_schema"}, tool
        assert tool["description"], tool
        assert tool["input_schema"]["type"] == "object", tool
    assert by_name["formula_hint"]["input_schema"]["required"] == ["topic"]

    row = _server_row(conn, server["id"])
    assert row is not None, "服务器已不在数据库中"
    assert row["status"] == "connected"
    assert row["last_error"] in (None, "")
    assert row["tools_refreshed_at"] is not None

    rows = _tool_rows(conn, server["id"])
    assert {r["name"] for r in rows} == set(by_name)
    for row in rows:
        tool = by_name[row["name"]]
        assert row["description"] == tool["description"]
        assert json.loads(row["input_schema"]) == tool["input_schema"]

    listing = api.request(cfg, "GET", "/api/mcp/servers", token=tokens["teacher_a"])
    assert listing.status_code == 200, listing.text
    api.assert_no_secret(listing, cfg.mcp_auth_token)
    item = next((s for s in listing.json() if s["id"] == server["id"]), None)
    assert item is not None
    assert item["status"] == "connected"
    assert item["tool_count"] == 2
    assert item["tools_refreshed_at"] is not None


def test_connect_with_wrong_token_marks_error(cfg, conn, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-bad"
    wrong_token = f"wrong-{prefix}"
    server = actions.register_server(cfg, tokens["teacher_a"], name, wrong_token)

    resp = api.request(
        cfg, "POST", f"/api/mcp/servers/{server['id']}/connect", token=tokens["teacher_a"]
    )
    assert resp.status_code == 502, resp.text
    api.assert_no_secret(resp, wrong_token)
    assert "令牌被拒绝" in resp.json()["error"], resp.text

    row = _server_row(conn, server["id"])
    assert row["status"] == "error"
    assert row["last_error"], "连接失败后 last_error 必须非空"
    assert wrong_token not in row["last_error"]
    assert row["tools_refreshed_at"] is None
    assert _tool_rows(conn, server["id"]) == []

    tools = api.request(
        cfg, "GET", f"/api/mcp/servers/{server['id']}/tools", token=tokens["teacher_a"]
    )
    assert tools.status_code == 200
    assert tools.json() == []


def test_student_cannot_connect(cfg, conn, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-stuc"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    resp = api.request(
        cfg, "POST", f"/api/mcp/servers/{server['id']}/connect", token=tokens["student_a1"]
    )
    assert resp.status_code == 403, resp.text
    assert _server_row(conn, server["id"])["status"] == "disconnected"


def test_other_class_connect_is_identical_404(cfg, conn, tokens, teacher_b, prefix):
    name = f"{data.server_prefix(prefix)}-cross"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    other = api.request(
        cfg, "POST", f"/api/mcp/servers/{server['id']}/connect", token=teacher_b["token"]
    )
    missing = api.request(
        cfg, "POST", "/api/mcp/servers/999999/connect", token=teacher_b["token"]
    )
    assert other.status_code == 404, other.text
    assert (other.status_code, other.content) == (missing.status_code, missing.content)
    assert _server_row(conn, server["id"])["status"] == "disconnected"
