"""POST /api/mcp/servers"""

from lib import actions, api, data, db


def _class_id(conn, name):
    row = db.fetch_one(conn, "SELECT id FROM classes WHERE name = %s", (name,))
    assert row is not None
    return int(row["id"])


def test_teacher_registers_server_and_database_matches(cfg, conn, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-reg"
    server = actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)

    assert set(server) == {
        "id",
        "name",
        "base_url",
        "has_token",
        "status",
        "tool_count",
        "last_error",
        "tools_refreshed_at",
    }, server
    assert server["name"] == name
    assert server["base_url"] == "http://mcp-demo:8090"
    assert server["has_token"] is True
    assert server["status"] == "disconnected"
    assert server["tool_count"] == 0
    assert "token" not in server
    api.assert_no_secret(
        api.request(cfg, "GET", "/api/mcp/servers", token=tokens["teacher_a"]),
        cfg.mcp_auth_token,
    )

    row = db.fetch_one(
        conn,
        "SELECT class_id, name, base_url, status, LENGTH(auth_token) AS token_len "
        "FROM mcp_servers WHERE id = %s",
        (server["id"],),
    )
    assert row is not None, "服务器未写入数据库"
    assert row["class_id"] == _class_id(conn, "A班")
    assert row["name"] == name
    assert row["base_url"] == "http://mcp-demo:8090"
    assert row["status"] == "disconnected"
    assert int(row["token_len"]) > 0

    listing = api.request(cfg, "GET", "/api/mcp/servers", token=tokens["teacher_a"])
    assert listing.status_code == 200, listing.text
    assert not any("token" in item for item in listing.json())
    item = next((s for s in listing.json() if s["id"] == server["id"]), None)
    assert item is not None
    assert item["has_token"] is True


def test_duplicate_name_returns_409(cfg, tokens, prefix):
    name = f"{data.server_prefix(prefix)}-dup"
    actions.register_server(cfg, tokens["teacher_a"], name, cfg.mcp_auth_token)
    resp = api.request(
        cfg,
        "POST",
        "/api/mcp/servers",
        token=tokens["teacher_a"],
        json={"name": name, "base_url": "http://mcp-demo:8090", "token": cfg.mcp_auth_token},
    )
    assert resp.status_code == 409, resp.text


def test_invalid_registration_returns_400(cfg, tokens):
    cases = (
        {"name": "Campus", "base_url": "http://mcp-demo:8090", "token": "x"},
        {"name": "1tools", "base_url": "http://mcp-demo:8090", "token": "x"},
        {"name": "a" * 33, "base_url": "http://mcp-demo:8090", "token": "x"},
        {"name": "okname", "base_url": "ftp://x", "token": "x"},
        {"name": "okname", "base_url": "http://mcp-demo:8090/mcp", "token": "x"},
        {"name": "okname", "base_url": "http://mcp-demo:8090", "token": ""},
    )
    for payload in cases:
        resp = api.request(cfg, "POST", "/api/mcp/servers", token=tokens["teacher_a"], json=payload)
        assert resp.status_code == 400, f"{payload['name']}: {resp.status_code} {resp.text}"
        assert resp.json().get("error"), resp.text


def test_student_cannot_register_server(cfg, tokens, prefix):
    resp = api.request(
        cfg,
        "POST",
        "/api/mcp/servers",
        token=tokens["student_a1"],
        json={
            "name": f"{data.server_prefix(prefix)}-stu",
            "base_url": "http://mcp-demo:8090",
            "token": "x",
        },
    )
    assert resp.status_code == 403, resp.text


def test_b_class_server_is_invisible_to_a_class(cfg, tokens, teacher_b, prefix):
    a_name = f"{data.server_prefix(prefix)}-aowner"
    b_name = f"{data.server_prefix(prefix)}-bowner"
    actions.register_server(cfg, tokens["teacher_a"], a_name, cfg.mcp_auth_token)
    actions.register_server(cfg, teacher_b["token"], b_name, "apitest-b-token")

    a_list = api.request(cfg, "GET", "/api/mcp/servers", token=tokens["teacher_a"])
    b_list = api.request(cfg, "GET", "/api/mcp/servers", token=teacher_b["token"])
    assert a_list.status_code == b_list.status_code == 200

    a_names = {s["name"] for s in a_list.json()}
    b_names = {s["name"] for s in b_list.json()}
    assert a_name in a_names and b_name not in a_names
    assert b_name in b_names and a_name not in b_names
