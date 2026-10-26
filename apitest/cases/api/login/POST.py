"""POST /api/login"""

from lib import api, db


def test_login_success_returns_token_and_identity_matching_db(cfg, conn):
    resp = api.request(
        cfg,
        "POST",
        "/api/login",
        json={"username": "teacher_a", "password": cfg.teacher_a_password},
    )
    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert set(body) == {"token", "username", "role"}, body
    assert isinstance(body["token"], str) and body["token"]
    assert body["username"] == "teacher_a"
    assert body["role"] == "teacher"

    row = db.fetch_one(conn, "SELECT username, role FROM users WHERE username = %s", ("teacher_a",))
    assert row is not None
    assert body["username"] == row["username"]
    assert body["role"] == row["role"]


def test_student_login_success(cfg):
    resp = api.request(
        cfg,
        "POST",
        "/api/login",
        json={"username": "student_a1", "password": cfg.student_a1_password},
    )
    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert body["username"] == "student_a1"
    assert body["role"] == "student"
    assert body["token"]


def test_wrong_password_returns_401_without_token(cfg):
    resp = api.request(
        cfg,
        "POST",
        "/api/login",
        json={"username": "teacher_a", "password": cfg.teacher_a_password + "-wrong"},
    )
    assert resp.status_code == 401, resp.text
    body = resp.json()
    assert "token" not in body
    assert body.get("error"), body

    # Reset the per-username failure counter so later runs cannot lock the
    # seeded teacher account.
    ok = api.request(
        cfg,
        "POST",
        "/api/login",
        json={"username": "teacher_a", "password": cfg.teacher_a_password},
    )
    assert ok.status_code == 200, ok.text


def test_unknown_user_uses_the_same_401(cfg):
    resp = api.request(
        cfg,
        "POST",
        "/api/login",
        json={"username": "no_such_user", "password": "whatever"},
    )
    assert resp.status_code == 401, resp.text
    assert "token" not in resp.json()


def test_missing_fields_return_400(cfg):
    for payload in ({"username": "teacher_a"}, {"password": "x"}, {}):
        resp = api.request(cfg, "POST", "/api/login", json=payload)
        assert resp.status_code == 400, f"{payload}: {resp.status_code} {resp.text}"
        assert resp.json().get("error"), resp.text
