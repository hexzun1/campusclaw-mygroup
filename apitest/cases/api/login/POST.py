"""POST /api/login"""

from urllib.parse import urlsplit

import pytest

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


def test_login_lock_is_isolated_between_source_addresses(cfg, teacher_b):
    """spec auth-upload「可信代理后的两个客户端计数互不影响」等 Scenario.

    Needs two entries that reach the API from different source addresses, so it
    skips unless API_BASE_URL_ALT is configured. The temporary teacher shares
    teacher_a's password hash, which keeps the seeded account's bucket clean.
    """
    if not cfg.api_base_url_alt:
        pytest.skip("未配置 API_BASE_URL_ALT，无法制造第二个真实源地址")

    username = teacher_b["username"]
    password = cfg.teacher_a_password
    attacker = cfg.api_base_url
    victim = cfg.api_base_url_alt
    # The address the attacker pretends to be. Forging it must not help: this
    # client is not a trusted proxy, so the header is ignored.
    forged = {"X-Real-IP": urlsplit(victim).hostname or ""}

    def login(base_url, *, secret=password, headers=None):
        return api.request(
            cfg,
            "POST",
            "/api/login",
            base_url=base_url,
            extra_headers=headers,
            json={"username": username, "password": secret},
        )

    for attempt in range(5):
        resp = login(attacker, secret=password + "-wrong")
        assert resp.status_code == 401, f"第 {attempt + 1} 次错误口令：{resp.status_code} {resp.text}"

    locked = login(attacker)
    assert locked.status_code == 401, f"锁定后正确口令仍应 401：{locked.text}"

    # The same username from another source address is a different bucket.
    other = login(victim)
    assert other.status_code == 200, f"另一个客户端被连带封禁：{other.status_code} {other.text}"
    assert other.json().get("token"), other.text

    # A direct client cannot move into the victim's bucket by claiming its IP.
    forged_locked = login(attacker, headers=forged)
    assert forged_locked.status_code == 401, (
        f"伪造 X-Real-IP 绕过了锁定：{forged_locked.status_code} {forged_locked.text}"
    )
    for attempt in range(5):
        resp = login(attacker, secret=password + "-wrong", headers=forged)
        assert resp.status_code == 401, f"伪造头第 {attempt + 1} 次：{resp.status_code} {resp.text}"

    still_ok = login(victim)
    assert still_ok.status_code == 200, f"伪造头封禁了受害者：{still_ok.status_code} {still_ok.text}"

