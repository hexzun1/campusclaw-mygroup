"""GET /api/me"""

from lib import api, db


def _me_row(conn, username):
    return db.fetch_one(
        conn,
        """
        SELECT u.username, u.role, u.class_id, c.name AS class_name
        FROM users u JOIN classes c ON c.id = u.class_id
        WHERE u.username = %s
        """,
        (username,),
    )


def test_me_matches_users_and_classes(cfg, conn, tokens):
    for username, token in (
        ("teacher_a", tokens["teacher_a"]),
        ("student_a1", tokens["student_a1"]),
    ):
        resp = api.request(cfg, "GET", "/api/me", token=token)
        assert resp.status_code == 200, resp.text
        body = resp.json()
        assert set(body) == {"username", "role", "class_id", "class_name"}, body

        row = _me_row(conn, username)
        assert row is not None
        assert body["username"] == row["username"]
        assert body["role"] == row["role"]
        assert body["class_id"] == row["class_id"]
        assert body["class_name"] == row["class_name"]


def test_me_without_token_is_401(cfg):
    resp = api.request(cfg, "GET", "/api/me")
    assert resp.status_code == 401, resp.text
    assert resp.json().get("error")


def test_me_with_invalid_token_is_401(cfg):
    resp = api.request(cfg, "GET", "/api/me", token="not-a-jwt")
    assert resp.status_code == 401, resp.text
