"""POST /api/search"""

from lib import api, db

# 桩的向量检索按字符二元组哈希，相似度低于 vector.ScoreThreshold (0.35) 会被丢弃。
# 「教学材料」与 A 班种子切片的余弦是 0.3244，故用能过阈值的完整短语（仍含子串「教学材料」，
# keyword 与 hybrid 不受影响）。
QUERY = "示例教学材料"


def _class_id(conn, name):
    row = db.fetch_one(conn, "SELECT id FROM classes WHERE name = %s", (name,))
    assert row is not None, f"预置班级 {name} 不存在"
    return int(row["id"])


def _assert_hits_match_chunks(conn, class_id, hits):
    assert hits, "检索结果为空，无法核对"
    for hit in hits:
        assert set(hit) == {
            "material_id",
            "material_title",
            "chunk_id",
            "chunk_index",
            "char_start",
            "char_end",
            "excerpt",
            "score",
        }, hit
        row = db.fetch_one(
            conn,
            """
            SELECT kc.id, kc.material_id, kc.class_id, kc.chunk_index,
                   kc.char_start, kc.char_end, kc.chunk_text, m.title
            FROM knowledge_chunks kc JOIN materials m ON m.id = kc.material_id
            WHERE kc.id = %s
            """,
            (hit["chunk_id"],),
        )
        assert row is not None, f"切片 {hit['chunk_id']} 不在数据库中"
        assert hit["material_id"] == row["material_id"]
        assert hit["material_title"] == row["title"]
        assert hit["chunk_index"] == row["chunk_index"]
        assert hit["char_start"] == row["char_start"]
        assert hit["char_end"] == row["char_end"]
        assert row["class_id"] == class_id

        text = row["chunk_text"]
        expected = text if len(text) <= 300 else text[:300] + "…"
        assert hit["excerpt"] == expected
        assert isinstance(hit["score"], (int, float))


def test_three_modes_match_database(cfg, conn, tokens):
    class_a = _class_id(conn, "A班")
    for mode in ("keyword", "vector", "hybrid"):
        resp = api.request(
            cfg,
            "POST",
            "/api/search",
            token=tokens["student_a1"],
            json={"query": QUERY, "mode": mode, "top_k": 5},
        )
        assert resp.status_code == 200, f"{mode}: {resp.status_code} {resp.text}"
        body = resp.json()
        assert body["mode"] == mode
        _assert_hits_match_chunks(conn, class_a, body["hits"])


def test_search_without_token_is_401(cfg):
    resp = api.request(cfg, "POST", "/api/search", json={"query": QUERY})
    assert resp.status_code == 401, resp.text


def test_other_class_content_is_invisible(cfg, tokens):
    query = "天文观测"

    resp = api.request(
        cfg,
        "POST",
        "/api/search",
        token=tokens["student_a1"],
        json={"query": query, "mode": "keyword"},
    )
    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert body["hits"] == [], body
    assert body.get("message"), "无命中时应带提示文案"

    own = api.request(
        cfg,
        "POST",
        "/api/search",
        token=tokens["student_b1"],
        json={"query": query, "mode": "keyword"},
    )
    assert own.status_code == 200, own.text
    titles = {hit["material_title"] for hit in own.json()["hits"]}
    assert titles == {"B 班天文观测安排"}, own.json()
