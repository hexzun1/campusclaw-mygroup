# 迭代 5 验收记录：MCP 评审问题修复

对应 OpenSpec 变更：`openspec/changes/fix-mcp-review-findings/`
验收日期：2026-10-27

## 0. 环境与约定

本机开发编排（`docker-compose.dev.yml`）：

| 服务 | 宿主地址 | 说明 |
| --- | --- | --- |
| api | `127.0.0.1:8081` | |
| db | `127.0.0.1:3307` | MySQL 8.0 |
| qdrant | `127.0.0.1:6333` | |
| stubgateway | `127.0.0.1:8091` | 容器内 8090 |
| mcp-demo | `127.0.0.1:8090` | 自建 MCP 服务器 |

后续所有命令使用：

```bash
API=http://127.0.0.1:8081
STUB=http://127.0.0.1:8091
MCP=http://127.0.0.1:8090
MT=$(grep ^MCP_AUTH_TOKEN= .env | cut -d= -f2-)
TOKEN=$(curl -s -X POST $API/api/login -H 'Content-Type: application/json' \
  -d '{"username":"teacher_a","password":"teacher-a-pass"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
```

集成测试需要测试库 DSN：

```bash
export CAMPUSCLAW_TEST_DSN="$(grep ^DB_USER= .env | cut -d= -f2-):$(grep ^DB_PASSWORD= .env | cut -d= -f2-)\
@tcp(127.0.0.1:3307)/campusclaw?parseTime=true&multiStatements=true&charset=utf8mb4"
```

> 令牌值不出现在本文件中。下文以 `$MT` 指代，`$TOKEN` 亦不记录具体值。
> 开发编排把源码以 bind mount 挂进 api 容器，`go run` 只在容器重启时重新编译，所以每轮验证前都执行了
> `docker compose -f docker-compose.dev.yml restart api`。

---

## 1.3 本组验证：审计不丢失

单元与集成测试：

```bash
$ cd backend && go vet ./... && go test ./internal/tutor/... ./internal/db/...
ok  	campusclaw/backend/internal/tutor
ok  	campusclaw/backend/internal/db
```

端到端：网关桩用 `/control` 让模型先调用 `formula_hint__campus-tools`，再调用一个 100 字符的函数名。
被测技能为「工具：formula_hint@evi-tools」，服务器 `evi-tools` 指向 `http://mcp-demo:8090`。

```bash
$ curl -s -X POST $STUB/control -H 'Content-Type: application/json' -d "{\"tool_calls\":[
    {\"name\":\"formula_hint__evi-tools\",\"arguments\":\"{\\\"topic\\\":\\\"勾股定理\\\"}\"},
    {\"name\":\"$(python3 -c 'print("x"*100)')\",\"arguments\":\"{}\"}]}"
200
```

meta（截取）：

```
meta.status = ok
  formula_hint@evi-tools ok
  xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx... denied not_allowed
```

`GET /api/mcp/calls?limit=2`：

```
id=218 tool_len=64 server='' status=denied reason=not_allowed round=1
id=217 tool_len=12 server='evi-tools' status=ok reason= round=1
```

数据库（`SELECT id, CHAR_LENGTH(tool), CHAR_LENGTH(server), status, reason ...`）：

```
217	12	9	ok	
218	64	0	denied	not_allowed
```

两条都在，超长函数名那条的工具名恰为 64 个字符、`status=denied`、`reason=not_allowed`。**通过。**

---

## 2.3 本组验证：调用循环口径

```bash
$ cd backend && go test ./internal/tutor/... ./internal/mcp/...
ok  	campusclaw/backend/internal/tutor
ok  	campusclaw/backend/internal/mcp
```

开发栈上一次返回 25 个候选外调用（`unit_convert__evi-tools`，候选集里只有 `formula_hint__evi-tools`）：

```bash
$ curl -s -X POST $STUB/control -H 'Content-Type: application/json' \
    -d '{"tool_calls":[...25 × unit_convert__evi-tools...]}'
control 200 forced= 25
$ curl -sN -X POST $API/api/conversations/176/stream -H "Authorization: Bearer $STOKEN" \
    -H 'Content-Type: application/json' -d '{"question":"勾股定理怎么用？"}'
```

结果：

```
len(meta.tool_calls) = 11
last = {"tool": "*", "status": "denied", "reason": "limit", "round": 1, "duration_ms": 0}
statuses = ['denied'] reasons = ['limit', 'not_allowed']
```

审计：本轮新增 11 行，其中 10 行 `not_allowed`、1 行 `limit`，回答正常完成（SSE 有 `meta` 与 `done`，
中间 7 个 `delta`，无 `error`）：

```
$ SELECT COUNT(*), SUM(status='denied'), SUM(reason='limit')
    FROM mcp_tool_calls WHERE conversation_id=176;
11	11	1
```

**通过。**

---

## 3.3 本组验证：客户端协议细节

```bash
$ gofmt -l backend mcp-server
（无输出）

$ cd backend && go vet ./internal/mcp && go test ./internal/mcp
ok  	campusclaw/backend/internal/mcp
```

**通过。**

---

## 4.3 本组验证：防 SSRF

登记被禁止的目标，均为 400：

```bash
$ for u in http://localhost:8090 http://169.254.169.254 http://qdrant:6333; do
    curl -s -o /dev/null -w "$u → %{http_code}\n" -X POST $API/api/mcp/servers \
      -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
      -d "{\"name\":\"forbid-check\",\"base_url\":\"$u\",\"token\":\"x\"}"; done
http://localhost:8090 → 400
http://169.254.169.254 → 400
http://qdrant:6333 → 400
```

合法目标仍可登记并连接：

```bash
$ curl -s -o /dev/null -w 'connect=%{http_code}\n' -X POST $API/api/mcp/servers/<id>/connect \
    -H "Authorization: Bearer $TOKEN"
connect=200
```

绕过登记、直接改库后连接（覆盖变更前已存在的旧记录）：

```bash
$ UPDATE campusclaw.mcp_servers SET base_url='http://db:3306' WHERE id=1410;
$ curl -s -o /dev/null -w 'connect=%{http_code}\n' -X POST $API/api/mcp/servers/1410/connect \
    -H "Authorization: Bearer $TOKEN"
connect=502
{"error":"服务器地址不允许指向本机、元数据或内部服务"}

$ SELECT status, last_error FROM mcp_servers WHERE id=1410;
error	服务器地址不允许指向本机、元数据或内部服务
```

`status=error`，错误文案不含任何地址以外的敏感信息。**通过。**

---

## 5.4 本组验证：登记与发现的健壮性

```bash
$ cd backend && go test ./internal/mcp/...
ok  	campusclaw/backend/internal/mcp

$ curl -s $API/openapi.json | python3 -c 'import sys,json; json.load(sys.stdin)'
（无输出，解析成功）

$ python3 - <<'PY'  # 读取 openapi.json 中更新后的说明
POST 400: 名称不合法、地址不允许（本机、元数据或内部服务）、令牌长度不在 1–512 字节之间
connect 502: 无法连接（含连接超时、被禁止的地址、被重定向）或令牌被拒绝
connect 503: 工具缓存写入失败或服务暂不可用
```

`openapi 3.0.3 paths 22`。**通过。**

---

## 6.4 本组验证：删除与保存技能互斥

```bash
$ cd backend && go test ./internal/db/... ./internal/tutor/... ./internal/mcp/...
ok  	campusclaw/backend/internal/db
ok  	campusclaw/backend/internal/tutor
ok  	campusclaw/backend/internal/mcp

$ go test ./internal/db -run DeleteVsSaveRace -count=20
ok  	campusclaw/backend/internal/db
```

并发用例同时打印的 3×20 轮结果见 6.3：每一轮要么"保存成功 + 删除得到引用名单"，要么"删除成功 + 保存被拒"，
无悬空引用、无 1213 死锁。

apitest 中 skills 与 mcp servers 相关用例（与 7.1 同一次运行）：

```bash
$ python apitest/run_all.py
56 passed in 2.20s
退出码：0
```

**通过。**

---

## 7.1 apitest 新增用例

```bash
$ apitest/.venv/bin/python apitest/run_all.py
56 passed in 2.20s
报告：apitest/reports/report-20261027-134727.html
退出码：0
```

（同一提交的首次运行报告为 `report-20261027-134423.html`，56 passed。）

新增的 4 个用例名出现在报告中：

```bash
$ grep -o "test_forbidden_targets_return_400\|test_token_length_counts_bytes\|\
test_overlong_function_name_is_truncated_in_the_audit\|test_off_candidate_calls_do_not_use_the_budget" \
  apitest/reports/report-20261027-134423.html | sort -u
test_forbidden_targets_return_400
test_off_candidate_calls_do_not_use_the_budget
test_overlong_function_name_is_truncated_in_the_audit
test_token_length_counts_bytes
```

分组结果：

| 组 | 用例数 | 结果 |
| --- | --- | --- |
| 登录 / 身份（`cases/api/login` + `cases/api/me`） | 8 | 通过 |
| 检索（`cases/api/search`） | 3 | 通过 |
| 智能体 / 技能（`cases/api/agents` + `cases/api/skills`） | 6 | 通过 |
| 会话与流式（`cases/api/conversations`） | 6 | 通过 |
| MCP 管理（`cases/api/mcp`，含新增 4 个） | 31 | 通过 |
| 端到端（`cases/e2e`，含新增名额用例） | 2 | 通过 |
| 合计 | 56 | 通过 |

新增用例各自覆盖的规约场景：

| 用例 | 覆盖 |
| --- | --- |
| `test_forbidden_targets_return_400` | 「工具服务器登记与管理」Scenario 被禁止的地址（9 个地址 400，`mcp-demo` 与 `10.0.0.5` 201） |
| `test_token_length_counts_bytes` | Scenario 令牌按字节计长度（171 汉字 400、170 汉字 201，均非 503） |
| `test_overlong_function_name_is_truncated_in_the_audit` | 「调用审计」超长函数名不影响同轮审计（审计中 `CHAR_LENGTH(tool)=64`） |
| `test_off_candidate_calls_do_not_use_the_budget` | 「工具调用循环」候选集外的调用不占调用名额（2 条 `not_allowed` + 3 条 `ok`，无 `limit`） |

为支持前两个需要网关桩"按用例指定模型行为"的用例，桩新增了 `reset_tool_calls` 与"强制列表只生效一次"
（design.md Decision 13、Decision 14），否则同一进程内两次强制调用会互相污染，只能靠重启容器复位。

**通过。**

---

## 附：临时数据清理

证据采集用的服务器、技能、智能体、会话与审计行在记录后已删除：

```
$ SELECT (SELECT COUNT(*) FROM skills WHERE name='证据技能'),
         (SELECT COUNT(*) FROM tutor_agents WHERE id=209),
         (SELECT COUNT(*) FROM mcp_servers WHERE id=1412);
0	0	0
```

---

## 8.3 原有同伴验证场景回归（`mcp-tools`「逐班授权与令牌保护」①–④）

命令按 `docs/iteration-4.md` 的精神重跑：临时创建 B 班教师 `tmp_teacher_b_iter5`（复制 teacher_a 的
`password_hash`），对 A 班服务器编号 `379` 与不存在的 `999999` 逐一调用。

### ① 服务器隔离

```
connect     A班编号=404 不存在=404 identical -> {"error":"记录不存在"}
tools       A班编号=404 不存在=404 identical -> {"error":"记录不存在"}
disconnect  A班编号=404 不存在=404 identical -> {"error":"记录不存在"}
DELETE      A班编号=404 不存在=404 identical -> {"error":"记录不存在"}
保存引用他班技能 -> 400 {"error":"字段“工具”的取值“formula_hint@campus-tools”：服务器“campus-tools”不存在，请先登记并连接"}
```

A 班服务器状态无变化（后续场景仍能正常连接）。

### ② 令牌不回传

```
列表       HTTP 200 含令牌=False
登记       HTTP 201 含令牌=False
连接       HTTP 200 含令牌=False
工具清单     HTTP 200 含令牌=False
连接失败     HTTP 502 含令牌=False

$ docker compose -f docker-compose.dev.yml logs api | grep -c "$MT"
0
$ docker compose -f docker-compose.dev.yml logs mcp-demo | grep -c "$MT"
0
```

### ③ 断开即失效

```
建技能(连接时) -> 201
断开 -> 204
断开后保存 -> 400 {"error":"字段“工具”的取值“formula_hint@campus-tools”：服务器“campus-tools”未连接，请先在工具服务器页连接"}
meta.status = ok
meta.tool_calls = [{"tool": "formula_hint@campus-tools", "status": "denied", "reason": "not_connected", "round": 0, "duration_ms": 0}]
事件序列 = meta ... done 无 error
审计 = [{"id": 239, "tool": "formula_hint@campus-tools", "status": "denied", "reason": "not_connected",
        "username": "student_a1", "conversation_id": 193, "round": 0, "duration_ms": 0, ...}]
重新连接 -> 200
```

### ④ 审计只看本班

```
B班教师看到了 0 条
与 A 班记录的交集 = []
```

### 本次新增响应的令牌检查

```
被拒地址: (400, '{"error":"服务器地址不允许指向本机、元数据或内部服务"}')
连接失败: (502, '{"error":"无法连接到服务器：连接超时"}')
api 日志含令牌 = 0；mcp-demo 日志含令牌 = 0
```

**结论：①–④ 结果与 `docs/iteration-4.md` 一致，通过。**

回归结束后清理了临时教师、技能、智能体、会话与审计行，预置数据回到
`skills = 数学解题引导 / 语文解题引导 / 验收B班技能`、`mcp_servers = 1（campus-tools，connected）`。
