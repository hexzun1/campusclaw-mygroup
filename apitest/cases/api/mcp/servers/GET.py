"""GET /api/mcp/servers"""

from lib import actions, api, data


def test_teacher_lists_servers_without_tokens(cfg, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-list"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    resp = api.request(cfg, "GET", "/api/mcp/servers", token=tokens["teacher_a"])
    assert resp.status_code == 200, resp.text
    api.assert_no_secret(resp, cfg.mcp_auth_token)

    item = next((s for s in resp.json() if s["id"] == server["id"]), None)
    assert item is not None
    assert set(item) == {
        "id",
        "name",
        "base_url",
        "has_token",
        "status",
        "tool_count",
        "last_error",
        "tools_refreshed_at",
    }, item
    assert item["name"] == name
    assert item["has_token"] is True
    assert item["status"] == "disconnected"
    assert "token" not in item


def test_student_cannot_list_servers(cfg, tokens):
    resp = api.request(cfg, "GET", "/api/mcp/servers", token=tokens["student_a1"])
    assert resp.status_code == 403, resp.text


def test_b_teacher_only_sees_b_class_servers(cfg, tokens, teacher_b, prefix):
    a_name = f"{data.server_prefix(prefix)}-list-a"
    b_name = f"{data.server_prefix(prefix)}-list-b"
    actions.register_server(cfg, tokens["teacher_a"], a_name, cfg.mcp_auth_token)
    actions.register_server(cfg, teacher_b["token"], b_name, "apitest-b-token")

    resp = api.request(cfg, "GET", "/api/mcp/servers", token=teacher_b["token"])
    assert resp.status_code == 200, resp.text
    names = {s["name"] for s in resp.json()}
    assert b_name in names
    assert a_name not in names
