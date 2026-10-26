> Apply 约定：一个 task 一轮 —— 读 task → 实现 → 对照 spec 审查 diff → 运行 verify → 通过后勾选 `[x]` → 提交。verify 未通过不得勾选，不得带入下一项。每组最后一项是该组的整体验证。
> 环境：本机 `.env` 已含 `MCP_AUTH_TOKEN`（随机串）。开发编排 `docker compose -f docker-compose.dev.yml up -d`：api `127.0.0.1:8081`（`$API`）、db `127.0.0.1:3307`、网关桩 `127.0.0.1:8091`（`$STUB`）、`mcp-demo` `127.0.0.1:8090`（`$MCP`）。生产编排 `docker compose up --build -d`：web `http://localhost:8080`（`$WEB`）。
> 取 token：`TOKEN=$(curl -s -H 'Content-Type: application/json' -d '{"username":"teacher_a","password":"…"}' $API/api/login | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')`。B 班教师：用 SQL 临时创建 `tmp_teacher_b`（复制 teacher_a 的 `password_hash`，`role='teacher'`，`class_id` 为 B 班），用完删除。`MT=$(grep ^MCP_AUTH_TOKEN= .env | cut -d= -f2-)`。
> 证据统一记入 `docs/iteration-4.md`：命令、关键输出、"通过 / 不通过"。

## 1. mcp-server

- [x] 1.1 新建独立 Go 模块 `mcp-server/`（只依赖标准库）：`MCP_AUTH_TOKEN` 为空拒绝启动、`GET /health`、`POST /mcp` 的 64KB 上限与常量时间 Bearer 校验 — verify: `cd mcp-server && go vet ./... && go test ./...` 通过；`MCP_AUTH_TOKEN= go run .` 以非零退出且输出可读原因；`curl -s $MCP/health` 得 `{"status":"ok"}`；不带令牌与错误令牌 `curl -s -o /dev/null -w '%{http_code}' -X POST $MCP/mcp -d '{}'` 均为 401
- [x] 1.2 手写 JSON-RPC 分发与错误码：`initialize`、`notifications/initialized`（202 无体）、`tools/list`、`tools/call`；`-32700`/`-32600`/`-32601`/`-32602` — verify: `go test ./... -run RPC` 表驱动覆盖非法 JSON、缺 `method`、`tools/unknown`、未知工具、缺参数五种错误码与可读 `message`；65KB 请求体得 413；`initialize` 响应含 `protocolVersion`、`capabilities.tools`、`serverInfo.name/version`
- [x] 1.3 实现 `formula_hint`（主题表关键词包含匹配，只给提示）与 `unit_convert`（长度 / 质量 / 时间）— verify: `go test ./... -run Tools` 断言「勾股定理」与整句「勾股定理怎么求斜边」都返回含公式的提示、未知主题返回「暂无该主题的提示」且非 `isError`、`1.5 km→m` 得含 `1500` 的文本、`kg→s` 得 `isError:true`；`tools/list` 中两个工具名都满足 `^[a-z][a-z0-9_]{0,63}$` 且 `inputSchema.type=="object"`
- [x] 1.4 日志不含令牌与参数；多阶段 Dockerfile 不含令牌 — verify: 用错误与正确令牌各调一次后 `docker compose logs mcp-demo | grep -c "$MT"` 为 0；`docker history --no-trunc <mcp-demo 镜像> | grep -c "$MT"` 为 0；`grep -n MCP_AUTH_TOKEN mcp-server/Dockerfile` 无输出
- [x] 1.5 编排：`docker-compose.yml` 增加 `mcp-demo`（构建 `mcp-server/`，环境变量注入 `MCP_AUTH_TOKEN`，端口 `127.0.0.1:8090:8090`）；`docker-compose.dev.yml` 增加 `mcp-demo`（同端口），网关桩宿主端口改为 `127.0.0.1:8091`，db 宿主端口改为 `127.0.0.1:3307` — verify: 两套编排分别 `up -d` 后 `docker compose port mcp-demo 8090` 为 `127.0.0.1:8090`；`lsof -iTCP:8090 -sTCP:LISTEN -P` 只出现 `127.0.0.1`；开发编排下 `docker compose -f docker-compose.dev.yml port db 3306` 为 `127.0.0.1:3307`、`port stubgateway 8090` 为 `127.0.0.1:8091`；生产编排下 `docker compose port db 3306` 与 `port api 8081` 无输出
- [x] 1.6 本组验证 — verify: 依次执行并记录输出：`curl -s $MCP/health`；`curl -s -o /dev/null -w '%{http_code}\n' -X POST $MCP/mcp -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'`（401）；带 `-H "Authorization: Bearer $MT"` 的同一请求返回两个工具；`tools/call formula_hint {"topic":"勾股定理"}` 返回文本；`{"jsonrpc":"2.0","id":2,"method":"nope"}` 返回 `-32601`

## 2. 迁移与数据层

- [x] 2.1 迁移 `004_mcp.sql`：`mcp_servers`（含 `seed_key`、`UNIQUE(class_id,name)`、`UNIQUE(class_id,seed_key)`）、`mcp_tools`（`PRIMARY KEY(server_id,name)`、级联删除）、`mcp_tool_calls`（`server_id` `ON DELETE SET NULL`，无参数 / 结果 / 令牌列）— verify: 连续重启 api 两次均成功，`schema_migrations` 中 `004` 恰一行；`SHOW CREATE TABLE` 三表与 design Decision 6 一致；删除一台服务器后其 `mcp_tools` 行数为 0、其 `mcp_tool_calls` 的 `server_id` 为 NULL 且 `server` 仍保留名称
- [x] 2.2 `internal/config` 集中读取 `MCP_AUTH_TOKEN`（可选）与 `MCP_CONNECT_TIMEOUT`、`MCP_CALL_TIMEOUT`、`MCP_TOOL_STAGE_TIMEOUT`、`MCP_MAX_RESPONSE_BYTES`（有默认值），非法值启动失败 — verify: `go test ./internal/config` 覆盖缺省值、非法时长报错、错误文本不含令牌值；不设 `MCP_AUTH_TOKEN` 时 api 正常启动
- [x] 2.3 `internal/db` 增加服务器、工具缓存、审计的读写：按编号读取一律带 `class_id`；读取服务器的默认查询不选 `auth_token`，只有 `GetServerToken(id, classID)` 取令牌；"整体替换缓存 + 更新状态"在同一事务 — verify: 集成测试：A 班身份读 B 班服务器为 `ErrNotFound`；列表结构体无令牌字段；替换缓存在中途注入错误时回滚，旧缓存保持不变
- [x] 2.4 种子：配置了 `MCP_AUTH_TOKEN` 时为 A 班按 `seed_key` 幂等预置 `campus-tools`（`http://mcp-demo:8090`、`disconnected`）— verify: 全新库启动后 A 班恰有一条 `campus-tools`；连续重启两次仍为一条；教师改其 `base_url` 后重启不被覆盖；不设 `MCP_AUTH_TOKEN` 启动时不创建
- [x] 2.5 本组验证 — verify: `cd backend && go test ./internal/db/... ./internal/config/... ./seed/...` 通过；`mysql -h127.0.0.1 -P3307 … -e "SELECT name,status,seed_key,LENGTH(auth_token)>0 FROM mcp_servers"` 显示预置行且不打印令牌

## 3. 登记、连接、发现接口

- [x] 3.1 实现 MCP 客户端 `internal/mcp.Client`：Bearer、`Accept`、`MCP-Protocol-Version`、回带 `Mcp-Session-Id`、256KB 响应上限、非 `application/json` 视为协议错误、错误分类与令牌脱敏 `redact` — verify: `go test ./internal/mcp` 用 `httptest` 覆盖 401→「令牌被拒绝」、拨号失败→「无法连接到服务器」、超时、非 JSON、SSE 响应、超大响应，且所有错误文本中不含令牌（远端在错误体中回显令牌时也被替换为 `***`）
- [x] 3.2 `GET/POST /api/mcp/servers`、`DELETE /api/mcp/servers/{id}`（仅教师，被技能引用时 409 并列出技能名）— verify: teacher_a 合法登记得 201、列表含 `has_token:true`、`status:"disconnected"`、`tool_count:0` 且 `grep "$MT"` 无输出；名称 `Campus`、`1tools`、33 字、地址 `ftp://x`、`http://u:p@x`、以 `/mcp` 结尾、201 字、空令牌、513 字令牌各得 400；重名得 409；student_a1 调用得 403
- [x] 3.3 `POST /connect`、`GET /tools`、`POST /disconnect`：握手三步、整体替换缓存、过滤非法名称、说明截断 1024 字、schema 超 8KB 丢弃；失败 502、`status=error`、清空缓存 — verify: 连接 `campus-tools` 得 200 与两个工具；把 `mcp-demo` 换成带"非法名 / 超长说明 / 9KB schema"工具的测试服务（`httptest` 集成测试）验证过滤；以错误令牌登记的服务器连接得 502「令牌被拒绝」、连接 `http://127.0.0.1:1` 得 502「无法连接到服务器」，两者 `status` 为 `error`、工具为空数组；断开后 `status=disconnected`、工具为空数组
- [x] 3.4 他班一致 404 与主链路不受影响 — verify: `tmp_teacher_b` 对 A 班服务器编号调用 `connect`、`tools`、`disconnect`、`DELETE`，响应与对 999999 的响应 `diff` 无输出；在连接失败的同时，student_a1 的 `/api/search` 与一次会话流式提问均正常完成
- [x] 3.5 本组验证 — verify: 依次 `curl` 登记 → 列表 → 连接 → 工具 → 断开 → 删除，记录每步响应；对全部响应体执行 `grep -c "$MT"` 均为 0；`docker compose -f docker-compose.dev.yml logs api | grep -c "$MT"` 为 0

## 4. 技能字段与校验

- [x] 4.1 扩展 `tutor.ParseSkill` 的「工具」字段：1–3 个取值、全角 / 半角逗号、`knowledge_search` 或 `工具名@服务器名`、不重复，错误指明具体取值 — verify: `go test ./internal/tutor -run ParseSkill` 新增用例：三个合法取值、4 个取值、重复取值、`Formula@campus-tools`、`formula_hint@`、`formula_hint@Campus_Tools`、`web_search` 均按 spec 通过或报错；原有用例全部仍通过
- [x] 4.2 保存时校验 MCP 引用（创建 / 修改）：服务器属于本班、`connected`、工具在缓存中、映射函数名 ≤64 字符；他班服务器与不存在的服务器报错措辞相同 — verify: `campus-tools` 断开时保存引用 `formula_hint@campus-tools` 得 400「未连接」；连接后引用 `nope@campus-tools` 得 400「不在工具清单中」；`tmp_teacher_b` 引用 `formula_hint@campus-tools` 与引用 `formula_hint@no-such` 的错误文本 `diff` 后只差服务器名本身；合法引用保存成功且 `tools_json` 为 `["knowledge_search","formula_hint@campus-tools"]`
- [x] 4.3 删除服务器时的引用检查 — verify: 有技能引用 `campus-tools` 时 `DELETE` 得 409 且响应列出该技能名；把技能的工具改回 `knowledge_search` 后删除得 204（随后重启 api 由种子恢复 `campus-tools`）
- [x] 4.4 本组验证 — verify: `cd backend && go test ./internal/tutor/...` 通过；预置技能的 `tools_json` 仍为 `["knowledge_search"]`（SQL 查询）

## 5. 调用循环与 meta

- [x] 5.1 网关 `ChatWithTools`（非流式、带 `tools` 与 `tool_choice:"auto"`、返回 `content` 与 `tool_calls`）；网关桩支持带 `tools` 的确定性工具调用与 `/control` 的"指定工具调用"开关 — verify: `go test ./internal/gateway ./cmd/stubgateway` 断言桩在首轮返回对第一个工具的调用、参数按 `required` 生成、带工具消息时返回文本、指定开关可产生多条 / 非法 / 越权调用；`/api/ask` 请求体仍不含 `tools`（`$STUB/stats`）
- [x] 5.2 候选集解析与 `denied/not_connected`（`round:0`）；函数名映射表 `工具名__服务器名` 查表反解 — verify: `go test -run Candidates` 覆盖：服务器断开、工具不在缓存、他班服务器（视为不可用）、`knowledge_search` 被跳过；函数名不在表中返回 `not_allowed`
- [x] 5.3 宿主校验参数（JSON、`required`、基本类型）、远端调用（每次轻量握手、10 秒）、结果清洗（只取 text、去控制字符、2000 字）、失败回传工具消息、上限（2 轮 / 3 次 / 20 秒 → `denied/limit`）、工具阶段网关失败时降级 — verify: `go test -run ToolLoop` 用假网关与假 MCP 覆盖：正常一次调用、非 JSON 参数、缺 `topic`、越权函数名、第 4 次调用、20 秒截止（注入时钟）、远端 `isError`、远端超时、工具阶段网关报错后仍进入正文生成；每种情况的记录状态与原因符合 spec
- [x] 5.4 最终生成：重组消息（资料 → 以「〔工具结果 …〕」开头的工具结果 → 提问；系统消息追加第 ⑥ 段声明与"解题引导"优先级）、流式且不带 `tools`；`meta.tool_calls`（`omitempty`）；工具阶段在写出 `meta` 前完成，审计在写出 `meta` 前落库 — verify: `go test -run BuildMessages` 新增快照用例；集成：命中含 `formula_hint@campus-tools` 的技能提问后，`$STUB/stats` 记录的**最后一次**请求不含 `tools` 且用户消息中工具结果位于资料之后、提问之前
- [x] 5.5 不含 MCP 工具的会话保持第 5 课行为 — verify: 对只含 `knowledge_search` 的技能与未命中技能各提问一次，`curl -N` 得到的 `meta` 不含 `tool_calls`，网关桩只收到一次 `stream:true` 请求且不含 `tools`；`go test ./internal/tutor/...` 中第 5 课的全部用例仍通过
- [x] 5.6 本组验证 — verify: 新建绑定"工具：formula_hint@campus-tools"技能（适用条件含「公式」）的智能体与会话，`curl -N -H "Authorization: Bearer $TOKEN" -d '{"question":"勾股定理的公式怎么用"}' $API/api/conversations/<id>/stream` 的首个事件为 `meta` 且含 `{"tool":"formula_hint@campus-tools","status":"ok","round":1,…}`，`tool_calls` 中无参数与结果原文；断开 `campus-tools` 后再问同一问题，`tool_calls` 为 `denied`/`not_connected`，回答正常完成并有 `done`

## 6. 审计接口

- [x] 6.1 `GET /api/mcp/calls?limit=`：仅教师、本班、倒序、`limit` 缺省 50 且 1–200、每条含 `tool@server`、状态、原因、用户名、会话编号、轮次、时间 — verify: 第 5 组产生的记录在 teacher_a 的列表首位且字段齐全；`limit=0` 与 `limit=201` 得 400；student_a1 得 403；`SELECT * FROM mcp_tool_calls` 的列中没有参数、结果或令牌
- [x] 6.2 本组验证 — verify: `curl -s -H "Authorization: Bearer $TOKEN" "$API/api/mcp/calls?limit=5"` 与 `SELECT tool,server,status,reason,conversation_id,round FROM mcp_tool_calls WHERE class_id=<A> ORDER BY id DESC LIMIT 5` 逐条一致；`tmp_teacher_b` 的结果中没有 A 班记录

## 7. 前端两处

- [x] 7.1 「工具服务器」页 `/admin/mcp`（`TeacherOnly`）与导航教师标签：列表、登记（令牌输入框 `type=password`、提交后清空）、连接、断开、删除、查看工具（名称、说明、`input_schema` 以纯文本格式化）、本班审计表 — verify: `cd frontend && npm run build && npm run lint` 通过；浏览器中 teacher_a 完成登记 → 连接 → 查看工具 → 断开 → 删除，每步列表状态正确，连接失败时显示「令牌被拒绝」等提示；页面文本与网络响应中检索不到令牌；student_a1 导航无该入口，直接访问 `/admin/mcp` 回到 `/materials`
- [x] 7.2 助手页工具标签：回答上方渲染 `meta.tool_calls`，`denied` 与 `error` 醒目 — verify: 浏览器中先触发一次 `ok`、再断开服务器触发一次 `denied`，两次回答上方分别出现对应标签，`denied` 标签样式与 `ok` 明显不同；不含工具的技能提问时不出现标签
- [x] 7.3 本组验证 — verify: 生产编排 `$WEB` 下用 teacher_a 与 student_a1 各走一遍上述页面，浏览器控制台无 CSP 违规；`grep -rnE "dangerouslySetInnerHTML|innerHTML" frontend/src/pages` 无输出

## 8. Swagger 与 openapi.json

- [x] 8.1 为全部 MCP 管理接口与审计接口添加 swag 注解（入参、出参、400/401/403/404/409/502、`@Security BearerAuth`），重新生成 `backend/docs/` — verify: 重新执行生成命令后 `git diff --exit-code backend/docs` 无差异；`swagger.json` 中含 `/api/mcp/servers`、`/api/mcp/servers/{id}/connect`、`/api/mcp/servers/{id}/tools`、`/api/mcp/servers/{id}/disconnect`、`/api/mcp/calls`
- [x] 8.2 引入 `github.com/getkin/kin-openapi`，启动时用 `openapi2conv` 转换并缓存，`GET /openapi.json` 在业务路由之前注册、不经认证；nginx 增加 `location = /openapi.json`（不写 `add_header`）；`swagger-initializer.js` 改为加载 `/openapi.json` — verify: `curl -s $WEB/openapi.json | python3 -c 'import sys,json;d=json.load(sys.stdin);assert d["openapi"].startswith("3.0");[d["paths"][p] for p in ["/api/login","/api/mcp/servers/{id}/connect","/api/skills","/api/conversations/{id}/stream"]];print("ok")'` 输出 `ok`；`curl -s -o /dev/null -w '%{http_code}' $WEB/swagger/doc.json` 仍为 200
- [x] 8.3 文档不含密钥与页面试调 — verify: 对 `/openapi.json` 与 `backend/docs/` 检索本机 `.env` 中各密钥与 `MCP_AUTH_TOKEN` 的值均无输出；浏览器在 `$WEB/swagger/` 登录 teacher_a、Authorize 后依次试调 `connect`（200）、`tools`（200）、`POST /api/skills`（引用已发现的工具，201），控制台无 CSP 违规
- [x] 8.4 本组验证 — verify: `cd backend && go build ./... && go test ./...` 通过；记录 `curl -sI $WEB/openapi.json` 与上面的断言输出

## 9. apitest

- [x] 9.1 骨架：`apitest/requirements.txt`、`pytest.ini`（`python_files=*.py`、`--import-mode=importlib`、注册 `gateway` 标记、`testpaths=cases`）、`conftest.py`、`lib/`（配置读取与缺项报错、登录、`pymysql` 连接、SSE 解析、前缀 `apitest_<ts>`（服务器名为 `apitest-<ts>`）、清理与临时 B 班教师 fixture）、`.env.example`（只有变量名） — verify: `python3 -m venv .venv && .venv/bin/pip install -r apitest/requirements.txt` 成功；缺少 `APITEST_DB_PASSWORD` 时 `python apitest/run_all.py` 非零退出并提示该变量；`grep -E '=.+' apitest/.env.example` 无输出
- [x] 9.2 登录与身份用例：`cases/api/login/POST.py`、`cases/api/me/GET.py`（成功、401、缺字段 400、`/api/me` 与 `users`/`classes` 一致）— verify: `cd apitest && pytest cases/api/login cases/api/me -q` 全部通过
- [x] 9.3 检索用例：`cases/api/search/POST.py`（三种模式与 `knowledge_chunks` 核对、无 token 401、student_b1 检索 A 班独有内容 200 空）— verify: `pytest cases/api/search -q` 通过
- [x] 9.4 智能体与技能用例：`cases/api/agents/POST.py`、`cases/api/skills/POST.py`（字段与库一致、非法文件 400、学生 403、引用未连接服务器工具 400）— verify: `pytest cases/api/agents cases/api/skills -q` 通过
- [x] 9.5 会话与流式用例：`cases/api/conversations/POST.py`、`cases/api/conversations/_id_/stream/POST.py`（事件顺序、拼接等于库中正文、详情与库一致；`gateway` 标记，503 时 skip 并写原因）— verify: `pytest cases/api/conversations -q` 通过；`docker compose -f docker-compose.dev.yml stop stubgateway` 后再跑，流式用例显示为 skipped 且原因含「对话网关不可用」
- [x] 9.6 MCP 管理用例：`cases/api/mcp/servers/POST.py`、`GET.py`、`_id_/connect/POST.py`、`_id_/tools/GET.py`、`_id_/disconnect/POST.py`、`cases/api/mcp/calls/GET.py`（与 `mcp_servers`/`mcp_tools` 核对、学生 403、B 班教师 404、响应无令牌）— verify: `pytest cases/api/mcp -q` 通过
- [x] 9.7 端到端与断开即失效：`cases/e2e/test_mcp_flow.py`（连接 → 建技能并绑定 → 会话提问 → `meta.tool_calls` 含 `formula_hint@campus-tools` → 审计接口与 `mcp_tool_calls` 有 `ok` 记录 → 断开再问 → `denied` 与审计记录；未调用工具时失败并打印 `meta`；结束时恢复 `campus-tools` 的原状态）— verify: `pytest cases/e2e -q` 通过；临时让网关桩不发起工具调用（`/control`），该用例失败且输出含完整 `meta`
- [x] 9.8 `run_all.py` 与清理 — verify: `python apitest/run_all.py` 退出码为 0，生成 `apitest/reports/report-<ts>.html` 且可单独打开；运行后 `SELECT COUNT(*)` 统计名称以 `apitest_`（服务器为 `apitest-`）开头的智能体、技能、服务器、用户均为 0，预置数据行数与运行前一致；`pytest --collect-only -q` 无模块名冲突
- [x] 9.9 本组验证 — verify: `cd apitest && python run_all.py; echo "exit=$?"` 记录汇总行与退出码；`git status --porcelain | grep -c apitest/reports` 为 0

## 10. README 与 .env.example

- [x] 10.1 `.env.example` 增加 `MCP_AUTH_TOKEN=`（无值）；`.gitignore` 增加 `apitest/reports/`、`apitest/.env`、`__pycache__/`、`.pytest_cache/` — verify: `grep -n '^MCP_AUTH_TOKEN=$' .env.example` 有输出；`git check-ignore apitest/reports/x.html apitest/.env` 两行均输出
- [ ] 10.2 README 增加两节：MCP（`mcp-demo` 的作用、主机与 `mcp-demo` 共用 `MCP_AUTH_TOKEN`、`127.0.0.1:8090` 自查命令、连接 / 审阅 / 绑定流程、技能「工具」写法、`/openapi.json`）与接口测试（安装依赖、配置 `apitest/.env`——开发编排下 `API_BASE_URL=http://localhost:8081`、DB `127.0.0.1:3307`——运行 `run_all.py`、查看报告）；同步更新开发端口（db 3307、网关桩 8091）— verify: 同伴只按 README 从零完成"登记连接 campus-tools 并跑通 `run_all.py`"
- [x] 10.3 本组验证 — verify: `git grep -n "MCP_AUTH_TOKEN=" -- ':!*.md'` 只出现 `.env.example` 且无值；`git ls-files | grep -xE '\.env|apitest/\.env'` 无输出

## 11. 全量验收

- [x] 11.1 运行 `openspec validate add-mcp-tools-and-api-tests --strict` — verify: 退出码 0 且无 error
- [x] 11.2 全量自动化 — verify: `cd backend && go vet ./... && go test ./...`、`cd mcp-server && go test ./...`、`cd frontend && npm run build && npm run lint && node --test src/api/*.test.ts`、`python apitest/run_all.py` 全部通过并记录报告文件名
- [x] 11.3 第 5 课回归 — verify: 不含 MCP 工具的会话、`/api/search`、`/api/ask`、助手页与两个管理页按第 5 课 `docs/iteration-3.md` 的关键命令重跑，结果不变
- [ ] 11.4 同伴交叉验证 ① 服务器隔离 — verify: 同伴用 `tmp_teacher_b` 对 A 班服务器调用 `connect`、`tools`、`disconnect`、`DELETE`，均与 999999 的 404 逐字节相同；保存引用 `formula_hint@campus-tools` 的技能得 400；`docs/iteration-4.md` 记录同伴姓名、命令、输出与结论
- [ ] 11.5 同伴交叉验证 ② 令牌不回传 — verify: 同伴对列表、登记、连接（含错误令牌）、工具接口的响应体，以及 `docker compose logs api mcp-demo` 检索 `$MT` 与登记时使用的测试令牌，均无匹配；记录同上
- [ ] 11.6 同伴交叉验证 ③ 断开即失效 — verify: 同伴断开 `campus-tools` 后保存引用其工具的技能得 400；触发已绑定该工具的技能，`meta.tool_calls` 中为 `denied`/`not_connected`，`GET /api/mcp/calls` 与 `mcp_tool_calls` 均有对应记录，回答正常完成；记录同上
- [ ] 11.7 同伴交叉验证 ④ 审计只看本班 — verify: A 班产生调用记录后，同伴用 `tmp_teacher_b` 调用 `GET /api/mcp/calls`，结果中没有 A 班的会话编号或用户名；记录同上
- [x] 11.8 清理与机密检查 — verify: 删除 `tmp_teacher_b`；`git ls-files | grep -x .env` 无输出；`git grep -n "Bearer ey"` 无输出；对仓库检索 `$MT` 无输出
- [ ] 11.9 归档本 change（需使用者明确指示后执行 `/opsx:archive`）— verify: `openspec list` 中无本 change；`openspec/specs/mcp-tools/spec.md` 与 `openspec/specs/api-testing/spec.md` 已生成；`tutor-chat`、`auth-upload`、`knowledge-qa-ui` 中被修改的 Requirement 已是新内容
