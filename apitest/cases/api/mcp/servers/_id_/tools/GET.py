"""GET /api/mcp/servers/{id}/tools"""

import json

from lib import actions, api, data, db


def test_connected_tools_match_database_and_hide_token(cfg, conn, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-tools"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)
    actions.connect_server(cfg, tokens["teacher_a"], server["id"])

    resp = api.request(
        cfg, "GET", f"/api/mcp/servers/{server['id']}/tools", token=tokens["teacher_a"]
    )
    assert resp.status_code == 200, resp.text
    api.assert_no_secret(resp, cfg.mcp_auth_token)

    tools = resp.json()
    by_name = {tool["name"]: tool for tool in tools}
    assert set(by_name) == {"formula_hint", "unit_convert"}, tools

    rows = db.fetch_all(
        conn,
        "SELECT name, description, input_schema FROM mcp_tools "
        "WHERE server_id = %s ORDER BY name",
        (server["id"],),
    )
    assert {r["name"] for r in rows} == set(by_name)
    for row in rows:
        tool = by_name[row["name"]]
        assert set(tool) == {"name", "description", "input_schema"}
        assert tool["description"] == row["description"]
        assert tool["input_schema"] == json.loads(row["input_schema"])


def test_disconnected_server_returns_empty_array(cfg, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-notools"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    resp = api.request(
        cfg, "GET", f"/api/mcp/servers/{server['id']}/tools", token=tokens["teacher_a"]
    )
    assert resp.status_code == 200, resp.text
    assert resp.json() == []


def test_student_cannot_list_tools(cfg, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-stut"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    resp = api.request(
        cfg, "GET", f"/api/mcp/servers/{server['id']}/tools", token=tokens["student_a1"]
    )
    assert resp.status_code == 403, resp.text


def test_other_class_tools_lookup_is_identical_404(cfg, tokens, teacher_b, prefix):
    name = f"{data.server_prefix(prefix)}-crosst"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    other = api.request(
        cfg, "GET", f"/api/mcp/servers/{server['id']}/tools", token=teacher_b["token"]
    )
    missing = api.request(
        cfg, "GET", "/api/mcp/servers/999999/tools", token=teacher_b["token"]
    )
    assert other.status_code == 404, other.text
    assert (other.status_code, other.content) == (missing.status_code, missing.content)
