"""Session-wide fixtures: config, database, tokens, temp B teacher, cleanup."""

from __future__ import annotations

import os
import sys
from types import SimpleNamespace

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import pytest  # noqa: E402

from lib import api, data as testdata, db  # noqa: E402
from lib.config import ConfigError, load_config  # noqa: E402


@pytest.fixture(scope="session", autouse=True)
def session_state():
    try:
        cfg = load_config()
    except ConfigError as exc:
        pytest.exit(f"配置错误：{exc}", returncode=4)

    conn = db.connect(cfg)
    state = SimpleNamespace(
        cfg=cfg,
        conn=conn,
        prefix=testdata.new_prefix(),
        snapshot=None,
        tokens={},
        teacher_b={},
    )
    try:
        # A previous interrupted run must not poison names or foreign keys.
        testdata.cleanup_test_data(conn)
        state.snapshot = testdata.snapshot_counts(conn)

        teacher_b_id, teacher_b_username = testdata.create_temp_teacher_b(conn, state.prefix)
        state.teacher_b = {
            "id": teacher_b_id,
            "username": teacher_b_username,
            "token": api.login(cfg, teacher_b_username, cfg.teacher_a_password),
        }
        state.tokens = {
            "teacher_a": api.login(cfg, "teacher_a", cfg.teacher_a_password),
            "student_a1": api.login(cfg, "student_a1", cfg.student_a1_password),
            "student_b1": api.login(cfg, "student_b1", cfg.student_b1_password),
        }
        yield state
    finally:
        try:
            testdata.cleanup_test_data(conn)
            testdata.assert_no_test_data(conn)
            if state.snapshot is not None:
                after = testdata.snapshot_counts(conn)
                diff = testdata.diff_counts(state.snapshot, after)
                assert not diff, f"预置数据行数发生变化（表: 运行前/运行后）：{diff}"
        finally:
            conn.close()


@pytest.fixture(scope="session")
def cfg(session_state):
    return session_state.cfg


@pytest.fixture(scope="session")
def conn(session_state):
    return session_state.conn


@pytest.fixture(scope="session")
def prefix(session_state):
    return session_state.prefix


@pytest.fixture(scope="session")
def tokens(session_state):
    return session_state.tokens


@pytest.fixture(scope="session")
def teacher_b(session_state):
    return session_state.teacher_b
