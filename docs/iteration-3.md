# 迭代 3 说明（add-tutor-agents-skills-streaming）

> 编号说明：本文件名与本文正文的「迭代 3」按 `openspec/changes/add-tutor-agents-skills-streaming/tasks.md` 的字面约定书写；README 中同一变更列为「迭代 4（v0.4.0-tutor-agents-skills-streaming）」（「迭代 3」已被 knowledge-qa-ui 占用，见 `docs/knowledge-qa-ui.md`）。两处指同一变更，以版本号 v0.4.0 为准。

规约：`openspec/changes/add-tutor-agents-skills-streaming/`

## 验收环境

- 自动验收：`docker compose -f docker-compose.dev.yml up -d`（db、qdrant、网关桩、api 均绑 `127.0.0.1`；`$API=http://localhost:8081`、`$STUB=http://localhost:8090`）。api 经仓库外 override 接确定性网关桩（`EMBEDDING_DIM=2048` 与 qdrant 集合维度一致）。
- 生产编排（web → `localhost:8080`，`$WEB`）用于经 nginx 的验收（4.16、5.2、6.12）。
- 账号：teacher_a / student_a1（A 班）、student_b1（B 班）；口令取自本机 `.env`（文中以 `<PASSWORD>` 代替；token 以 `$TOKEN` 等变量代指，均不落盘、不入库、不写入本文件）。
- 后端测试在 api 容器内运行：`docker compose -f docker-compose.dev.yml exec -T api sh -c 'CAMPUSCLAW_TEST_DSN="$DB_USER:$DB_PASSWORD@tcp(db:3306)/$DB_NAME?parseTime=true&charset=utf8mb4" CAMPUSCLAW_TEST_EMBEDDING_DIM="$EMBEDDING_DIM" go test …'`。
- SQL 经 db 容器执行（口令来自容器自身环境变量，不在宿主机命令行出现）：`docker compose -f docker-compose.dev.yml exec -T db sh -c '… mysql --defaults-extra-file=/tmp/.my.cnf --default-character-set=utf8mb4 campusclaw …'`。
- 伪造 token 用 `python3`（`hmac` + `base64.urlsafe_b64encode`）现场构造，密钥取自 `.env` 的 `JWT_SECRET` 或故意错钥。

## 1. 身份来源调整（MODIFIED auth-upload）— 1.1–1.10 全部通过

### 1.1 改造签发：载荷恰为五项 — 通过

- 命令：容器内 `go test ./internal/auth -run Issue -v`；另登录一次后用 `python3` 解码 token 载荷。
- 关键输出：单测 PASS（断言载荷键集合恰为 `sub,jti,iat,exp,iss`、`exp-iat=24h`、两次签发 `jti` 不同）；实测解码：键集合 `[exp, iat, iss, jti, sub]`，`iss=campusclaw-api`，`sub=2`（十进制字符串），`exp-iat=24.0h`。
- 结论：通过。

### 1.2 改造解析：严格校验 — 通过

- 命令：容器内 `go test ./internal/auth -v`（表驱动 `TestParseMatrix`）。
- 关键输出：18 个子用例全部 PASS：正确 token 通过；他钥签名、`alg=none`、`HS512`、过期、`iss` 不符、缺 `sub`/`jti`/`exp`、`sub="abc"`/`"0"`、旧格式 token（含 `role`/`class_id`/`user_id`/`username`、密钥正确）全部报错。
- 结论：通过。

### 1.3 db.GetIdentity — 通过

- 命令：容器内（带测试 DSN）`go test ./internal/db -run GetIdentity -v`。
- 关键输出：`TestGetIdentityIntegration` PASS（teacher_a → teacher / A班 / class 1；999999 → `ErrNotFound`）；`TestGetIdentityDatabaseUnavailable` PASS（连接不可达 → 非 `ErrNotFound` 的普通错误）。
- 结论：通过。

### 1.4 RequireAuth 顺序与 /api/me — 通过

- 命令与关键输出：

```
$ curl -i -H "Authorization: Bearer $TOKEN" $API/api/me
HTTP/1.1 200 OK
{"class_id":1,"class_name":"A班","role":"student","username":"student_a1"}

# 旧格式 token（含 role 等字段、密钥正确）
HTTP 401 {"error":"未登录或会话已失效"}

$ docker compose stop db
$ curl -H "Authorization: Bearer $TOKEN" $API/api/materials
HTTP 503 {"error":"服务暂不可用，请稍后重试"}

$ docker compose start db   # 等 healthy 后，同一 token
HTTP 200 [{"id":1,"title":"A 班教学材料示例","class_id":1,…}]
```

- 结论：通过。

### 1.5 身份变更立即生效（不重新登录）— 通过

- 命令：SQL `UPDATE users SET class_id=2 WHERE username='student_a1'` → 同一 token `GET /api/materials`；改回后重复；再 SQL 把 teacher_a 的 `role` 改 `student` → 上传 → 改回 → 上传。
- 关键输出：

```
baseline（class_id=1）:        [(1,'A 班教学材料示例')]
class_id→2 后同一 token:       [(2,'B 班教学材料示例'), (3,'B 班天文观测安排')]
改回 class_id=1 后同一 token:  [(1,'A 班教学材料示例')]
上传 #1（role=teacher）:       HTTP 201 {"id":66,"index_status":"indexed",…}
role→student 后同一 token:     HTTP 403 {"error":"没有权限执行该操作"}
改回 role=teacher 后同一 token: HTTP 201 {"id":67,"index_status":"indexed",…}
```

全程未重新登录。验收一次性产生的材料 66/67（「角色即时生效测试一/三」）属临时数据，随 2.4 的 `down -v` 全量重建清除。
- 结论：通过。

### 1.6 删除用户后 token 立即失效 — 通过

- 命令：SQL 新建 `tmp_user`（`password_hash` 复制 student_a1，班级 A）→ 登录取 token → `/api/me`；`DELETE FROM users WHERE username='tmp_user'` → 同一 token `/api/me`。
- 关键输出：

```
tmp_user /api/me:  HTTP 200 {"class_id":1,"class_name":"A班","role":"student","username":"tmp_user"}
删除后同一 token:  HTTP 401 {"error":"未登录或会话已失效"}
（随后 users 恢复为 3 个种子用户；孤儿会话行数 0）
```

- 结论：通过。

### 1.7 登录换发（旧 token 吊销）— 通过

- 命令：登录矩阵——带旧 token 登录 / 不带头登录 / 带 token + 错误口令 / 带乱码 token + 正确口令。
- 关键输出：

```
带旧 token 甲登录 → 乙（登录响应体键集合恰为 [role, token, username]）
甲 /api/me → 401；乙 /api/me → 200
不带头登录 → 丙；乙 /api/me → 200
带乙 + 错误口令 → 401 {"error":"用户名或密码错误"}；乙 /api/me → 200
带乱码 token + 正确口令 → 200（丁）；丁 /api/me → 200
```

- 结论：通过。

### 1.8 伪造矩阵 — 通过

- 命令：`python3` 现场构造 9 类 token，逐个 `GET /api/me`。
- 关键输出：

```
wrongkey / tamper_role / tamper_sub / alg_none / hs512 / expired / iss_other / no_sub / no_jti
全部 HTTP 401；9 份响应体逐字节相同（md5 均为 cf31cd17012acd1e463c6787f9656a05）
{"error":"未登录或会话已失效"}
query 携带有效 token → 401；Cookie 携带有效 token → 401；同一 token 在 Authorization 头 → 200
```

- 结论：通过。

### 1.9 前端登录携带已有 token — 通过

- 命令：`cd frontend && node --test src/api/client.login.test.ts && npm run build && npm run lint`。
- 关键输出：`tests 2 / pass 2 / fail 0`（有 token 时登录请求带 `Authorization: Bearer <旧>`；无 token 时不带）；build 成功；lint 0 errors（3 条既有 warning：`AuthContext.tsx:48`、`AuthContext.tsx:63`、`MaterialsPage.tsx:35`）。
- 结论：通过。

### 1.10 既有行为回归 — 通过

- 命令：容器内 `go test ./...`（带测试 DSN 与嵌入维度）。
- 关键输出：全部包 `ok`（`auth 0.065s`、`db 0.199s`、`gateway 2.020s`、`index`/`search`/`tutor`/`vector` 均 ok）。
- 命令：按 `docs/jwt-bearer-auth.md` 的关键命令重跑。
- 关键输出：

```
学生上传:                        403 {"error":"没有权限执行该操作"}
跨班详情 3(B班) vs 999999:       404 / 404，diff 无输出，{"error":"记录不存在"}
跨班下载 3/file vs 999999/file:  404 / 404，diff 无输出
登出: {"ok":true} HTTP 200；旧 token /api/me → 401
/health（无 token）:            200 {"status":"ok"}；/api/login 无需 token
限流（同一来源）：5 次错误口令均 401；随后正确口令仍 401 {"error":"用户名或密码错误"}
```

限流计数在 api 进程内存中：验收用 student_b1 于同一来源 IP 触发后，重启 api 即清零（重启后同一口令恢复 200），与既有文档「重启清零」一致。
- 结论：通过。

## 2. 存储（2.1–2.4）— 全部通过

### 2.1 迁移 003 与约束 / 级联 — 通过

- 重启：连续 `docker restart` api 两次，`/health` 均 200。
- `schema_migrations`：`001_init.sql / 002_knowledge_chunks.sql / 003_tutor.sql` 各恰一行。
- 六张表结构（用 information_schema 查询等价核对，此处摘要）：唯一键 `uq_skills_class_name` / `uq_skills_class_seed`、`uq_agents_class_name` / `uq_agents_class_seed`，两张绑定表复合主键；外键 `messages.conversation_id → conversations.id`、`tutor_agent_skills.skill_id → skills.id`、`tutor_agent_skills.agent_id → tutor_agents.id`、`tutor_agent_materials` 两列均 `ON DELETE CASCADE`。
- `INSERT INTO messages(conversation_id, role='system', …)` → `ERROR 1265 (01000): Data truncated for column 'role'`（`role` 为 `enum('user','assistant')`，mysql exit=1）。
- 级联实测：临时会话（含 2 条消息）删除后 `msgs_after_delete = 0`；临时技能（含 1 条绑定）删除后 `bindings_after_delete = 0`。
- 结论：通过。

### 2.2 数据访问隔离（集成测试）— 通过

- 命令：容器内 `go test -count=1 -v ./internal/db`（带测试 DSN）。
- 关键输出：`TestTutorCrossClassReadsAreNotFound`、`TestTutorConversationReadRequiresOwner`、`TestTutorBindingValidationRollsBack` 均 PASS（连同该包其余 11 个用例，`ok 0.077s`）。
- 结论：通过。

### 2.3 ParseSkill 表驱动 — 通过

- 命令：容器内 `go test -count=1 -v ./internal/tutor -run ParseSkill`。
- 关键输出：`TestParseSkillAcceptsValidFile`、`TestParseSkillOptionalFieldsAndColonStyles`、`TestParseSkillErrorMatrix`（14 个子用例：字段重复、未定义字段、无冒号行、非检索工具、缺「适用条件」、缺名称、缺启用、「启用：可能」、无正文、名称 33 字、正文 2001 字、文件 8001 字等）、`TestParseSkillErrorNamesPlace` 全部 PASS。
- 结论：通过。

### 2.4 down -v 全新重建与幂等 — 通过

- 命令：`docker compose -f docker-compose.dev.yml [-f 仓库外桩 override] down -v` → `up -d`。
- 关键输出（全新启动）：A 班技能 2 条且均启用（数学 / 语文解题引导）；每班 1 个「默认助教」且启用；A 班默认助教绑定 2 条技能与《A 班教学材料示例》；六表计数 `skills/agents/binds/mats/convs/msgs = 2/2/2/1/0/0`（materials 共 3）。
- 连续重启 api 两次：计数不变（同为 `2/2/2/1/0/0/3`）。
- 把 A 班默认助教改名并改提示词后重启：修改保留（`默认助教（改名验收）` / `改名后的提示词：请以验收身份回答。`），`agents_total=2` 无新增（按 `class_id+seed_key` 幂等）。
- 说明：`down -v` 同时清除了实现期遗留的开发数据（材料 66/67、实现期会话与消息、临时智能体等）。改名留痕保留在库中，不影响后续验收（各 verify 按 `seed_key` / 绑定而非名称引用）；全部验收结束后恢复原名称与提示词。
- 结论：通过。

## 3. 接口

### 3.1 智能体接口 — 通过

- 教师侧（teacher_a）：`POST /api/agents`（空提示词、空绑定 `skill_ids:[] / material_ids:[]`）→ `201 {"id":3}`；`PUT /api/agents/3` 改名「验收智能体甲（改）」、停用、换绑定 `{skill_ids:[1],material_ids:[1]}` → `200 {"id":3}`；`GET /api/agents` 反映 `{'id':3,'enabled':False,'skill_ids':[1],'material_ids':[1]}`。
- 学生侧（student_a1）：`GET /api/agents` 只有 `[{'id':1,'name':'默认助教（改名验收）'}]`（停用的 3 与 B 班 2 都不出现），每项键恰为 `{id,name}`；`POST`/`PUT` → `403 {"error":"没有权限执行该操作"}`，前后表计数 `agents/agent_skills/agent_materials = 3/3/2` 不变。
- 绑定 404 矩阵（teacher_a）：绑定 B 班技能（id=3）、不存在技能（999999）、B 班材料（id=2）、不存在材料（999999）与 `PUT /api/agents/999999` 全部 `404 {"error":"记录不存在"}`，md5 均为 `be4e30913dc50d23910098baf0f72cab`（逐字节一致）；失败后 agent 3 绑定仍为 `[1]/[1]`（事务回滚）。
- 校验 400：同名 → `400 已存在同名智能体`；名称 65 字 → `400 名称不能超过 64 个字符`；提示词 2001 字 → `400 提示词不能超过 2000 个字符`；空白名称 → `400 名称不能为空`。
- 结论：通过。

### 3.2 技能接口 — 通过

- 教师侧：`POST /api/skills` 合法文件（开头 `#` 注释行、半角冒号、正文含 `#` 与空行）→ `201 {"id":4}`；`PUT /api/skills/4` 启用改「否」→ `200`，`GET` 显示 `enabled=False`、`source_text` 为第二次提交原文；同名再建 → `400 已存在同名技能`。
- 7 种不合规文件均 400 且 `error` 含行号或字段名：`第 2 行：字段“名称”重复`、`第 3 行：未定义的字段“作者”…`、`第 1 行：不是“字段：值”格式`、`字段“工具”只能是 knowledge_search`、`缺少必填字段“适用条件”`、`字段“启用”只能是“是”或“否”`、`第 3 行：字段之后缺少空行与说明正文`；`skills` 计数 3→4（仅合法那条写入）。
- 删除级联：先绑定技能 4 到 agent 3（`skill_ids [1,4]`，`agent_skills` 3→4），`DELETE /api/skills/4` → `200 {"ok":true}`，agent 3 的 `skill_ids` 回到 `[1]`，`agent_skills` 回到 3。
- 学生侧：`GET /api/skills` 只含启用项 `ids [1,2]`（停用的 4 与 B 班 3 不出现），响应无 `source_text` 字段；`POST`/`PUT`/`DELETE` 全 403，`skills` 计数不变。
- B 班技能（id=3）的 `PUT`/`DELETE` → 404，与 999999 的 `PUT`/`DELETE` md5 均为 `be4e30913dc50d23910098baf0f72cab`。
- 结论：通过。

### 3.3 会话接口 — 通过

- student_a1 新建两个会话 → 均 `201`，标题均为「新的提问」（id 1、2）。
- 用已停用（3）/ B 班（2）/ 不存在（999999）的智能体新建 → 404，三者 md5 均为 `be4e30913dc50d23910098baf0f72cab`。
- SQL 停用 A 班全部智能体后新建 → `400 {"error":"本班暂无已启用的助手"}`；随后恢复（agent 1 启用，agent 3 保持停用）。
- teacher_a 会话 id=3、student_b1 会话 id=4；student_a1 列表仅 `[2,1]`（本人、最近更新在前）。
- student_a1 请求会话 3 / 4 / 999999 的详情 → 三者 404，md5 均为 `be4e30913dc50d23910098baf0f72cab`。
- 结论：通过。

### 3.4 请求参数不被采信 — 通过

- `GET /api/agents` 与 `?class_id=2` → 响应 `diff` 无输出；`GET /api/skills` 与 Header `X-Class-Id: 2` → `diff` 无输出。
- `POST /api/conversations` 请求体附带 `class_id=2 / user_id=1 / role=teacher` → `201`，新建会话 id=5；SQL 核对归属 `class_id=1, user_id=2`（student_a1），其列表可见、B 班不可见。
- 结论：通过。

## 4. 编排（作答链路与流式）

### 4.1 限定材料集合的混合检索 — 通过

- 新增集成测试 `backend/internal/search/restrict_integration_test.go`（真实 MySQL + Qdrant + 网关桩），api 容器内运行 `go test -count=1 -v ./internal/search -run 'HybridInMaterials'` → 3 个测试全部 `PASS`：
  - `TestHybridInMaterialsRestrictsBothBranches`：不受限对照里关键词与向量两路都能召回「乙」；限定 {甲} 后结果只含甲的切片（2 条 hits 均来自材料 4），两路候选都不含乙。
  - `TestHybridInMaterialsIntersectsTheClass`：B 班材料「丙」在 B 班可正常召回；A 班请求限定 {甲, 丙} 时只返回甲（1 条 hit 来自材料 6），丙不穿越。
  - `TestHybridInMaterialsEmptySetSkipsTheGateway`：空集合与含不存在编号 {999999} 均返回空结果，且 `$STUB/stats` 的 `embedding_calls` 不变（检索整段跳过）。
- `POST /api/search` 请求体附带 `material_ids` 与不附带 → 响应 `diff` 无输出（未知字段被丢弃，口径不变）。
- 结论：通过。

### 4.2 词法路由 `tutor.Route` — 通过

- `go test -count=1 -v ./internal/tutor -run 'Route'` 全部 `PASS`：`TestRouteSplitsOnEverySeparator`（全角/半角逗号、顿号、全角/半角分号、竖线 6 个子用例）、`TestRouteTrimsWordsAndDropsEmptyOnes`、`TestRouteIsCaseInsensitive`、`TestRoutePicksTheHighestWordCount`、`TestRouteBreaksTiesBySmallerID`、`TestRoutePicksNothingWhenNothingMatches`、`TestRouteNeverReturnsTwo`。
- 结论：通过。

### 4.3 资料后处理 `BuildCitations` — 通过

- `go test -count=1 -v ./internal/tutor -run 'Citations'` 全部 `PASS`：`TestBuildCitationsDropsEmptyBodies`（空正文剔除）、`TestBuildCitationsRenumbersAfterSubjectFilter`（范围过滤后从 1 连续编号）、`TestBuildCitationsKeepsOrderAndFields`（顺序与编号一致）、`TestBuildCitationsExcerptTruncatesAt300`（摘录 300 字截断）、`TestBuildCitationsEmptyInputIsNeverNil`。
- 结论：通过。

### 4.4 消息组装 `tutor.BuildMessages` — 通过

- `go test -count=1 -v ./internal/tutor -run 'BuildMessages'` 全部 `PASS`：快照断言固定顺序的单条系统消息、默认角色、解题引导硬性要求、未命中文案、有/无资料两种要求；`TestBuildMessagesHistoryWindow`（历史末尾 6 条、每条 500 字）、`TestBuildMessagesRewritesTheFinalQuestionWithMaterials`（最后一条以资料开头、以「学生提问：」结尾）、`TestBuildMessagesSkipsUnusableHistory`、系统消息不含资料正文、非系统消息不超过 6 条。
- 结论：通过。

### 4.5 流式安全的输出过滤 `tutor.OutputFilter` — 通过

- `go test -count=1 -v ./internal/tutor -run 'OutputFilter'` 全部 `PASS`：`TestOutputFilterSplitMarkerAcrossFragments`（`[1` / `2]` 跨片段）、`TestOutputFilterKeepsInRangeCitations`（引用数 3 保留 `[1]`、`[3]` 移除 `[7]`）、`TestOutputFilterZeroCitationsRemovesEverything`、`TestOutputFilterNonMarkersPassThrough`（`[a]`、空括号、`[` 结尾、`-1` 等原文输出 7 个子用例）、`TestOutputFilterHoldsThenReleasesTheBannedPrefix`（禁用文案前缀暂存后释放）、`TestOutputFilterFailsOnExactlyTheBannedAnswer`（整段相等报告失败）、`TestOutputFilterReleasesWhenTheBannedTextGetsAMarker`、`TestOutputFilterConcatenationMatchesExpectation`（拼接等于期望全文）。
- 结论：通过。

### 4.6 对话网关流式调用 — 通过

- `go test -count=1 -v ./internal/gateway -run 'Stream'`（对网关桩的集成测试）11 个测试全部 `PASS`：≥ 2 段 delta 分段到达、`stream:true` 请求体、首段前失败与中途中断被区分为不同错误、`ChatClient` 非流式路径不受影响。
- 调用一次 `/api/ask` 后 `GET $STUB/stats`：`last_chat_request.stream = False`（`/api/ask` 保持非流式）。
- 结论：通过。

### 4.7 流式全链路 `POST /api/conversations/{id}/stream` — 通过

- 响应头逐项核对：`Content-Type: text/event-stream`、`Cache-Control: no-cache`、`Connection: keep-alive`、`X-Accel-Buffering: no` 均出现。
- 事件序列：`meta` → 9 个 `delta` → `done`；所有 `delta.text` 拼接与 SQL 查出的 `message_id=4` 正文逐字节相同（md5 `16b2dc12bf234ec2ff667fe48a4986f6`）。
- 首轮后会话标题等于 31 字问题的前 24 字，`updated_at` 由 `07:40:03Z` 刷新为 `07:45:16Z`。
- 生成中再调用同一会话：`HTTP 409`、`Content-Type: application/json`、`{"error":"回答正在生成，请稍后重试"}`；后台流随后 `done`（`message_id=4`）。
- 结论：通过。

### 4.8 流开始前的失败为普通 JSON — 通过

- 无 token → `401 {"error":"未登录或会话已失效"}`；他人会话（id=3，teacher_a）→ `404 {"error":"记录不存在"}`；1 个字的问题 → `400 {"error":"问题需为 2 到 500 个字符"}`；`stop qdrant` 后 → `503 {"error":"服务暂不可用，请稍后重试"}`；桩 `fail_before_first_delta=true` → `503 {"error":"服务暂不可用，请稍后重试"}`。
- 五个响应的 `Content-Type` 均为 `application/json; charset=utf-8` 且均带 `Cache-Control: no-store`。
- 桩「首段前失败」一轮后 SQL 复核会话 5：用户消息 3→4（该轮提问已落库），助手消息保持 2 条（无落库）；随后 `fail_before_first_delta` 复位为 false。
- 结论：通过。

### 4.9 流开始后的失败与断开 — 通过

- 桩「第 2 段后中断」（`abort_after_deltas=2`，`chat_text=甲乙丙丁戊己庚辛壬癸子丑寅卯`）：客户端收到 `meta` → 2 个 `delta`（`甲乙丙`、`丁戊己`）→ `error {"message":"回答生成中断，请重试"}`（通用文案）；随后会话详情中 `message_id=8` 的助手消息正文 `甲乙丙丁戊己` 与这 2 段拼接逐字节相同。
- 客户端主动断开：`chat_text` 设为 296 字长文，`curl -N --max-time 1`（exit 28）在首段后断开，客户端收到 45 个 `delta` / 135 字；稍后会话详情中 `message_id=12` 的助手正文为 138 字 = 已收到 135 字 + 断开瞬间在途的 1 个分段（`本流式`），长度落在 3 字分段边界上，且是完整答案（296 字）的严格前缀 —— 落库正文即断开时已推送的部分，未丢失、未多写。
- 说明：客户端所见（135 字）比落库（138 字）少 3 字，是断开瞬间最后一个已写入套接字的分段未能被客户端读走的正常竞态；服务端侧「已推送」即 138 字。
- 结论：通过。

### 4.10 只在绑定集合内作答 — 通过

- teacher_a 上传两份材料：`数学材料-验收`（id=9，正文含「数学专属内容…」与共享句）与 `语文材料-验收`（id=10，正文含「语文专属内容…」与同一句），均 `201` 且 `index_status=indexed`。
- 新建智能体「验收-只绑数学」（prompt=`你是只绑数学材料的验收助手。`，技能 [1]，材料 [9]）→ `201 {"id":5}`；student_a1 用它新建会话 → `{"id":6,"agent_id":5}`。
- 问语文题「语文阅读理解：验收共享句子表达了怎样的情感？」：`meta.skill` 为 `null`，`citations` 只含 `material_id=9`（标题「数学材料-验收」），完整事件流以 `done` 收尾。
- `$STUB/stats`（对话调用 17→18）最后一条请求的最后一条用户消息以 `[1] 材料《数学材料-验收》切片 0` 开头；不含「语文专属内容」「阅读理解要把握作者的情感」「语文材料-验收」任一字符串（语文材料完全未进上下文）。
- 结论：通过。

### 4.11 会话锁定智能体 — 通过

- 在锁定智能体「验收-只绑数学」（id=5）的会话 6 中提问「验收共享句子表达了什么？」，请求体附带智能体乙（id=1）的 `agent_id`。
- `$STUB/stats`（对话调用 18→19）记录的系统消息以甲的提示词 `你是只绑数学材料的验收助手。` 开头，不含乙的提示词片段「改名后的提示词」；`meta.citations` 只含 `material_id=9`（甲绑定的数学材料），上下文中不含甲未绑定的「A 班教学材料示例」。
- 结论：通过。

### 4.12 「解题引导」规则与启停 / 解绑 — 通过

- 新建会话 7（agent 1）。同一道题「数学解题引导：验收共享句子表达了什么？」三次提问：
  - 技能启用且绑定：`meta.skill=数学解题引导`，`$STUB/stats` 系统消息含「硬性要求：先判对错并指出错在哪一步，不得写出最终答案、选项、得数或完整解题过程。」，不含未命中文案。
  - 教师 `PUT /api/skills/1`（`启用：否`）→ `200`，再问：`meta.skill=null`，系统消息不含该硬性要求、含「这次问题没有匹配到已启用的学科技能，请直接回答」。
  - 重新启用（`启用：是`）但 `PUT /api/agents/1` 解绑该技能（`skill_ids:[2]`）后再问：与停用完全一致（skill 为 null、无硬性要求、含未命中文案）。
- 验收后已恢复 agent 1 绑定 `[1,2]`（`GET /api/agents` 复核）。
- 真实网关项：本机本次验收未接真实网关，记为「未验证」（同迭代 2 的 10.5 处理口径）。
- 结论：通过。

### 4.13 检索落空仍作答且无编号 — 通过

- 桩输出设为「答案见[1]和[2]。」，在绑定材料的会话 7（agent 1，材料 [1]）问无依据的问题「光速不变原理的推导过程」：`$STUB/stats` 对话调用 22→23（+1）；`meta.citations` 为 `[]`；3 个 delta 拼接 `答案见和。`（越界编号 [1]、[2] 被过滤），落库助手消息（id=24）正文同为 `答案见和。`。
- 桩输出设为「资料中未找到相关内容」再问同一问题：`HTTP 503`、`Content-Type: application/json`、`Cache-Control: no-store`、`{"error":"服务暂不可用，请稍后重试"}`；会话 7 计数 user 5 / assistant 4 —— 该轮只落了用户提问（id=25），无助手消息落库；随后输出复位。
- 结论：通过。

### 4.14 引用编号连续且一一对应 — 通过

- 新增「英语材料-验收」（id=11，正文含共享句），新建绑定 [9,10,11] 的智能体「验收-三材料」（id=6）与会话 8；提问「验收共享句子表达了什么？」得到 3 条检索命中。
- `meta.citations` 的 `index` 依次为 1、2、3，分别对应 `material_id` 9、10、11（编号连续、与命中一一对应）。
- 桩输出「[1]甲乙丙[3]丁戊己[7]庚辛壬」：delta 拼接与落库正文（id=27）均为 `[1]甲乙丙[3]丁戊己庚辛壬` —— 含 `[1]`、`[3]`，越界的 `[7]` 被移除。
- 结论：通过。

### 4.15 已停用智能体的已有会话 — 通过

- SQL 把会话 8 的智能体「验收-三材料」（id=6）置为 `enabled=0`；在原会话 8 提问「验收共享句子表达了什么？」：完整事件流 `meta`（3 条引用）→ 12 个 `delta` → `done`（`message_id=29`）。
- `POST /api/conversations {"agent_id":6}`（已停用）→ `404 {"error":"记录不存在"}`；随后 SQL 恢复该智能体 `enabled=1`。
- 结论：通过。

### 4.16 nginx 流式路径关闭缓冲与缓存 — 通过

- `grep -n "proxy_buffering off\|proxy_cache off\|proxy_read_timeout 120s" frontend/nginx.conf` → 三行均有输出（24、25、27 行，位于 stream location `^/api/conversations/[0-9]+/stream$` 内）；`grep -n add_header frontend/nginx.conf` 只有第 13 行 server 级 CSP，该 location 内无 `add_header`（继承 CSP）。
- 生产编排（`docker-compose.yml` + 仓库外验收 override 接网关桩，web 镜像重新构建）下经 `$WEB=127.0.0.1:8080` 发起流式请求：`200`、`Content-Type: text/event-stream`；用 Python 记录到达时间：`meta` 于 +0.087s、首个 `delta` 于 +0.087s、后续 delta 以约 21ms 间隔**实时**到达（99 个）、`done` 于 +2.280s —— 首个 delta 远早于 done。
- 结论：通过。

### 4.17 /api/ask 口径不变 — 通过

- student_a1 用无依据的问题调用 `/api/ask`（请求体附带 `agent_id` 与 `class_id`）→ `200 {"answer":"资料中未找到相关内容","citations":[]}`；`$STUB/stats` 对话调用次数 26→26 不变（未调用模型、非流式）。
- 结论：通过。

### 5.1 swaggo 注解与固定版本生成 — 通过

- 生成命令（在容器内、固定版本）：`go run github.com/swaggo/swag/cmd/swag@v1.16.6 init --parseInternal --parseDependency -g cmd/server/main.go -o docs`（运行目录 `backend/`；模块同样固定 `github.com/swaggo/swag v1.16.6`）。`--parseDependency` 不可省：缺它时解析 `json.RawMessage` 类型字段报错退出。
- 幂等：重跑生成命令后 `backend/docs/` 三个产物（`docs.go`、`swagger.json`、`swagger.yaml`）与重跑前逐字节一致（sha256 比对相同）。说明：`backend/docs/` 目前是未纳入 git 的新目录（`?? backend/docs/`），`git diff --exit-code backend/docs` 待使用者把该目录随本次改动入库后即可按原命令复核；生成幂等性已由哈希比对先行证明。
- `swagger.json`：16 条路径全部在列（`/api/login`、`/api/me`、`/api/logout`、`/api/materials` 与 `/{id}`、`/{id}/file`、`/{id}/reindex`、`/api/search`、`/api/ask`、`/api/agents` 与 `/{id}`、`/api/skills` 与 `/{id}`、`/api/conversations` 与 `/{id}`、`/{id}/stream`）；`securityDefinitions.BearerAuth` 为 `apiKey`/`header`/`Authorization`，描述为「填 `Bearer <token>`（Bearer 与 token 之间有一个空格），token 来自 POST /api/login 的响应。」
- 结论：通过。

### 5.2 自托管 Swagger UI（含实现修正） — 通过

- `curl -sI $WEB/swagger/`（$WEB=127.0.0.1:8080，生产编排 + 仓库外网关桩 override）→ `200 OK`；响应含 `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; …`（`script-src 'self'`，且整个头部无任何 `unsafe-inline`）。
- 自写 `index.html`：`grep -n "<script" backend/internal/swaggerui/index.html` → 2 行，均带 `src=`（`swagger-ui-bundle.js`、`swagger-initializer.js`），无内联脚本。
- 注册顺序：`backend/cmd/server/main.go` 中 `/swagger/`（151 行）先于 `/api/` 子树（153 行，包在 `httpapi.NoStore` 内）注册，不经认证、不在 `NoStore` 内；nginx `/swagger/` 反代不写 `add_header`，继承 server 级 CSP。
- 实现修正（本次验收发现并修复）：按 Swagger UI 常用写法（`StandaloneLayout` + `SwaggerUIStandalonePreset`）渲染时，其顶栏自带徽标 SVG 内嵌一个 `<style>` 块（样式文本 `.logo_small_svg__cls-2{fill:#fff}.logo_small_svg__cls-3{fill:#85ea2d}`，CSP 违规哈希 `sha256-RL3ie0nH+Lzz2YNqQN83mnU0QJ1ot4QL7b99vMdIX99w=`），被 `style-src 'self'` 拦截，浏览器控制台出现 1 条 CSP 违规。修正为不引用 StandalonePreset 的基础版式：`swagger-initializer.js` 改为 `presets: [SwaggerUIBundle.presets.apis]` + `layout: "BaseLayout"`，`index.html` 不再引入 `swagger-ui-standalone-preset.js`；api 镜像已重新构建生效。修正后功能等价：16 条路径 = 21 个操作全部渲染、Authorize 可点、文档描述与 models 完整。
- 浏览器打开 `$WEB/swagger/`：控制台 0 条消息（含打开 / 关闭 Authorize 之后再查），无 CSP 违规。
- 说明：未启用 design.md Decision 9 的「放宽 `style-src`」退路——违规源（徽标内联样式）被移除后无需任何放宽，CSP 保持原样；相关文件改动随本次验收一并提交。
- 结论：通过。

### 5.3 在文档页面试调 — 通过

- `POST /api/login` 试调（用户名与口令由使用者在页面内键入；自动化不代键入、不读取口令）：`Execute` 后 200（nginx 访问日志 `06:30:48 "POST /api/login" 200 280`，referrer `http://127.0.0.1:8080/swagger/`）；响应体键集合恰为 `[role, token, username]`，`username=teacher_a`、`role=teacher`（token 仅留存在页面内供 Authorize 使用，未打印、未写入本文件）。
- 点 `Authorize` → 对话框填入 `Bearer <token>` → `Authorize` → `Close`：文档页按钮类名变为 `btn authorize locked`（已授权）。
- `GET /api/agents` 试调 → 200：5 个智能体 `1 默认助教`、`3 验收智能体甲（改）`（停用）、`5 验收-只绑数学`、`6 验收-三材料`、`7 验收-教师新建`（停用）。SQL 交叉核对 `tutor_agents`：class 1 恰为 `[1,3,5,6,7]`，B 班的 `id=2` 在库但未出现在响应中 —— 只含本班。
- `GET /api/conversations` 试调 → 200：2 个会话 `12（agent 6，「验收共享句子表达了什么？」）`、`3（agent 1，「新的提问」）`。SQL 交叉核对 `conversations` 全表 12 行：`user_id=1`（teacher_a）恰为 `{3,12}`，student_a1 的 9 行、student_b1 的 1 行均未出现 —— 只含本人。
- 收尾：页面脚本中的临时 token 引用已删除；文档页未启用 `persistAuthorization`，授权仅存页面内存，不落 localStorage。
- 说明：流程中授权前有一次 `POST /api/agents` 试调（06:28:46）得 401（40 字节错误体）——文档页对未授权调用按 401 处理，未产生任何数据。
- 结论：通过。

### 5.4 文档不含密钥 — 通过

- `grep -rniE "jwt_secret|api_key|password" backend/docs` → 3 处命中，均为登录请求的字段名而非值：`swagger.yaml:87 password:`、`docs.go:1386`、`swagger.json:1379`；`jwt_secret` / `api_key` 零命中。
- `.env` 各密钥的值不出现在 `backend/docs`：在容器内按键名（`SECRET|PASSWORD|API_KEY`）用环境变量引用逐个做固定串匹配（命令与判定均不打印任何值），9 个变量全部 `absent_from_docs`（`QDRANT_API_KEY` 为空值）。
- 结论：通过。

### 5.5 README 与示例 — 通过

- README 已包含 5 项内容：技能文件格式与示例（「技能文件格式」节 + `physics-skill.txt` 完整示例）、新接口一览（「助手接口一览」表 + curl 试调三例）、`/swagger/` 入口（「快速开始」中 `http://localhost:8080/swagger/` 及试调说明）、网关桩开关（「不接真实模型」节 6 个开关）、「升级到本版本后需重新登录」说明（迭代 4 介绍与「已知行为与限制」）。
- 按 README 示例操作：把示例技能文件原文经 `POST /api/skills`（teacher_a）提交 → `201 {"id":5}`；`DELETE /api/skills/5` → `200`（存在才可删）；删除后 `GET /api/skills` 恢复为两份预置技能（id 1、2），验收痕迹已清理。
- 按 README 打开 `$WEB/swagger/` → 页面标题「CampusClaw API 文档」，16 条路径可见，功能如 5.2。
- 结论：通过。

### 6.1 SSE 帧解析器单测 — 通过

- 命令（`frontend/`）：`node --test src/api/sse.test.ts`。
- 关键输出：`tests 6 / pass 6 / fail 0`。任务要求的四类覆盖齐备：一帧被拆成 3 个 chunk、两帧同处一个 chunk、`\r\n` 换行（含 `\r` 与 `\n` 跨 chunk 拆开）、一帧内多行 `data` 以换行拼接；另有「多字节字符跨 chunk 解码」「无 data 帧与注释行忽略」两例。
- 结论：通过。

### 6.2 API 客户端与流式读取 — 通过

- 命令（`frontend/`）：`npm run build`；`npm run lint`；`grep -rn "EventSource" src`。
- 关键输出：build 退出码 0（286 modules，`dist/assets/index-*.js` 441.56 kB / css 6.56 kB）；lint 退出码 0（0 errors、3 warnings — `AuthContext.tsx` 2 处与 `MaterialsPage.tsx:35` 查询 effect 1 处，均为 React Compiler 风格提示，不阻断）；`EventSource` 无任何匹配（流式经 `fetch` 读取响应体逐帧解析）。`client.ts` 含智能体 / 技能 / 会话各接口与 `streamConversation`（`/api/conversations/{id}/stream` 经 fetch，非 EventSource）。
- 结论：通过。

### 6.3 导航与路由 — 通过

- student_a1 在 `$WEB` 登录后主导航恰为 4 项（材料 / 知识检索 / 知识问答 / 助手），无「智能体管理」「技能管理」；在材料页用 `evaluate_script` 逐项读取 `aria-current`：仅当前标签为 `"page"`，其余为 null。
- 直接打开 `/admin/agents`、`/admin/skills` 均回到 `/materials`（可见「A班 教学材料」标题）。
- 未登录（已登出状态）打开 `/assistant` 回到登录页，且此期间 `list_network_requests`（xhr/fetch）为空 —— 未发出任何 `/api/conversations` 请求。
- teacher_a 登录后主导航恰为 6 项（材料 / 知识检索 / 知识问答 / 助手 / 智能体管理 / 技能管理），各页标题依次为「A班 教学材料」「知识检索」「知识问答」「助手」「智能体管理」「技能管理」；逐项检查 `aria-current`：材料页与智能体管理页均为「仅当前标签 `"page"`，其余 null」；`GET /api/me` 为 `teacher_a / teacher / A班（class_id=1）`。
- 结论：通过。

### 6.4 助手页会话列表与新建会话 — 通过

- 会话列表按最近更新倒序：UI 列表顺序与 API `GET /api/conversations`（`updated_at` 降序：conv8 `08:04:44Z` → conv7 `07:59:20Z` → conv6 `07:56:43Z` → … → conv1）逐一对应。
- 「新建会话」下拉只列已启用智能体：默认助教、验收-只绑数学、验收-三材料（3 个）；已停用的「验收智能体甲」不在列（与 `GET /api/agents` 学生侧只含启用项一致）。
- 选定「验收-三材料」后点「新建会话」：列表顶部新增标题「新的提问」；API 交叉核对新会话 `id=9, agent_id=6, title=新的提问`，位于列表首位（`updated_at=09:37:51Z`）。
- 结论：通过。

### 6.5 逐段显示、出处与技能名 — 通过

- 首轮（会话 9，验收-三材料，问「验收共享句子表达了什么？」）：页面内 fetch 抓取的 SSE 帧显示 `meta.skill=null`、`citations` 3 条（index 1/2/3 → 材料 9/10/11「数学/语文/英语材料-验收」，`chunk_index 0`，字符区间 0–42 / 0–42 / 0–61）；12 个 `delta` 拼接与渲染正文 `stubgateway 依据收到的资料作答： [1] [2] [3]` 一致；出处列表渲染「[1] 数学材料-验收 切片序号 0 字符 0–42 + 摘录」三行，与 meta 逐字段一致；`done.message_id=33`。
- 次轮（会话 10，验收-只绑数学，问「数学解题引导：请根据验收共享句子说明数学材料中的含义」，26 字）：**逐段显示**——因验收标签页处于后台（`document.visibilityState === "hidden"`），浏览器将 50ms `setInterval` 节流至约 1s（采样器记录 distinct=1、仅末态，佐证是节流而非页面卡顿）；改用 `MutationObserver` 以渲染事件为粒度记录回答区正文长度：`6→9→12→15→18→21→23→26`，8 个递增的不同值，时间戳 931886→932045ms（约 160ms 内逐段出现，非一次性出现），与抓取的 9 个 `delta` 帧（约 20–30ms 间隔）对应。
- 技能名与出处：完成后显示「技能：数学解题引导」与 1 条出处「[1] 数学材料-验收 切片序号 0 字符 0–42」；与 SSE `meta`（`skill:"数学解题引导"`，citations[0]：index 1 / material_id 9 / chunk_id 10 / 字符 0–42）及落库消息（id=35，`skill_name=数学解题引导`、citations 同上）三处一致。
- 标题 24 字：会话 10 标题更新为「数学解题引导：请根据验收共享句子说明数学材料中的」（26 字问题的前 24 字），侧栏与 API 一致。
- 结论：通过。

### 6.6 出处打开材料详情、历史会话重开 — 通过

- 点击回答中的出处「[1] 数学材料-验收 切片序号 0 字符 0–42」：页面打开材料详情，显示标题「数学材料-验收」、正文「数学专属内容：勾股定理适用于直角三角形。验收共享句子：本句同时出现在两份材料中。」及「下载原文件」「关闭」按钮。
- 刷新 `/assistant`：会话 10（最近更新）自动打开，两轮历史从落库消息完整重渲染 —— 两轮各显示提问、回答正文、「技能：数学解题引导」与 1 条出处；再点开会话 9：该轮回答与 3 条出处（[1] 数学材料-验收 / [2] 语文材料-验收 / [3] 英语材料-验收，均含切片序号与字符区间）完整显示。
- 结论：通过。

### 6.7 助手页错误处理 — 通过

- 吊销 token：携带 `sessionStorage` 中 Bearer token 在页面内调 `POST /api/logout` 得 200 `{"ok":true}`；随后提交提问，请求被鉴权拒绝（401），客户端清除 token 并跳转 —— `location.href` 变为 `http://127.0.0.1:8080/login`，`sessionStorage.getItem('campusclaw_token')` 为 `null`，`localStorage` 无任何键。
- qdrant 停机：验收栈 `stop qdrant` 后在会话 11 提问，回答区无任何新内容，新增一轮显示「服务暂不可用」（`client.ts` 对 503 的映射文案）；`start qdrant` 后容器内 `curl -sf http://qdrant:6333/collections` 通过、服务恢复。
- 桩中断：设桩「第 2 段后中断」（`abort_after_deltas=2`）后提问：回答区保留已显示文字「stubga」（= 前 2 段 delta 拼接），并显示「回答生成中断，请重试」（SSE `error` 帧文案）；落库助手消息正文同为「stubga」。
- 并发提交：单脚本内连发两次提交（Q1「并发验证甲：验收共享句子表达了什么？」，800ms 后 Q2「并发验证乙：验收共享句子表达了什么？」）：Q2 立即显示服务端 409 文案「回答正在生成，请稍后重试」（会话锁），同一时刻 Q1 仍在逐段显示（采集时已渲染 942 字，随后完成至 2565 字、3 条出处）。首次尝试把两次提交分置于两个自动化调用（间隔约 3s）：Q2 提交时 Q1 的客户端连接已提前中断（服务端落库了 387 字的部分正文）、锁已释放，Q2 正常流式作答，未触发 409；改为单脚本连发后稳定复现。
- 结论：通过。

### 6.8 纯文本显示 — 通过

- 先把 `window.alert` 替换为记录函数（`window.__alerts.push(...)`），再把桩「指定输出文字」设为 `<b>粗体</b><script>alert(1)</script>`，在提问框输入同一字符串并提交。
- 回答区渲染 `textContent` 与原文逐字节相同（`exactMatch=true`）；`window.__alerts` 记录数为 0（`alert` 未被调用）；回答区内 `querySelectorAll('b, script, img')` 计数为 0（即未被解析出任何元素），`.chat-main` 内 `b, script` 总计数亦为 0。
- 结论：通过。

### 6.9 材料页回归（MaterialDetailView 复用） — 通过

- 搜索（teacher_a，A 班此时 4 份材料）：输入「数学」→ 仅剩「数学材料-验收」1 条；输入不存在的「zzz」→ 列表 0 条；清空 → 恢复 4 条。
- 教师上传：文件选择框选 `/tmp/acceptance-material.md`（标题留空）→ `POST /api/materials` 201 → 页面提示「上传成功」，列表顶部出现「验收-表格与注入材料」（标题缺省为文件名去扩展名），材料 id=12。
- 详情与 Markdown：打开新材料详情 —— 表格渲染为真实 `<table>`（1 个表，表头 `[列 A, 列 B]`，数据行 `[甲,1,乙,2]`）；正文里的 `<script>alert('material-xss')</script>` 原样显示为文本（体内 `script` 元素计数 0；事先替换的 `window.alert` 记录器计数 0），react-markdown 无原始 HTML 支持。
- 下载：点「下载原文件」→ `GET /api/materials/12/file` 200（fetch + Blob，带 Authorization 头）。
- 主题切换：头部按钮「🌙 深色」→ `html[data-theme]` 由 `light` 变 `dark`；再点变回 `light`。
- 登出：并入 6.12 收尾（登出后跳转 `/login`、`sessionStorage` 清空）。
- 结论：通过。

### 6.10 智能体管理页 — 通过

- 新建：`/admin/agents` 填名称「验收-教师新建」、提示词，勾选技能「数学解题引导」与材料「数学材料-验收」→ 保存 → 列表项显示「已启用 技能1材料1」。
- 停用：编辑该智能体取消勾选「启用」→ 保存 → 列表显示「已停用」；重新打开编辑表单：启用未勾选、技能与材料勾选保持（`{enabled:false, skillChecked:true, matChecked:true}`），与保存后一致。
- 同名：再新建「验收-教师新建」→ 页面显示服务端 400 文案「已存在同名智能体」。
- 结论：通过。

### 6.11 技能管理页 — 通过

- 上传错误文件：文件选择框选 `/tmp/acceptance-skill-bad.md`（第 3 行为未定义字段「作者」）→ 文本框读入 58 字符 / 6 行；提交后页面显示服务端错误原文「第 3 行：未定义的字段“作者”，可用字段为 名称、适用条件、资料、工具、启用」。
- 改正后粘贴提交：把合法技能文本粘贴进文本框 → 提交 →「已保存」，列表新增「验收-错误技能」（状态「已启用」）。
- 编辑：启用改「否」→ 提交 →「已保存」，列表状态变「已停用」。
- 删除：点「删除」→ 行内确认（「确认删除」/「取消」）→「确认删除」→「已删除」，列表恢复为两份预置技能（数学解题引导 / 语文解题引导）。
- 交叉核对：智能体编辑页的技能勾选选项为 `[数学解题引导, 语文解题引导]`，已删除的技能不再出现。
- 结论：通过。

### 6.12 生产 CSP 下全流程 — 通过

- 栈：生产编排 + 仓库外网关桩 override（api 为 distroless，web 经 nginx `$WEB=127.0.0.1:8080`，CSP 为 server 级 `default-src 'self'; script-src 'self'; style-src 'self'`）。
- 依次完成的流程（teacher_a）：登录（使用者在页内键入口令）→ 材料（见 6.9：搜索 / 详情（表格渲染、`<script>` 作纯文本）/ 上传 / 下载）→ 知识检索（一次检索 4 条命中 —— 数学 / 语文 / 英语材料-验收、验收-表格与注入材料，均含切片序号与字符区间）→ 知识问答（正文「stubgateway 依据收到的资料作答： [1] [2] [3] [4]」+ 4 条出处）→ 助手会话提问（逐段显示：`resp.body.tee()` 记到 13 个 chunk；`MutationObserver` 记下回答区长度 12 个递增不同值 `3→34`（另有初始空态 `-1`），`done` 收尾、0 错误；标题更新为「验收共享句子表达了什么？」；完成后 3 条出处；新建会话下拉不含已停用的「验收-教师新建」）→ 智能体管理（6.10）→ 技能管理（6.11）→ 主题切换（`light→dark→light`）→ `/swagger/` 试调（5.3）→ 登出（`POST /api/logout` 200，跳转 `/login`）。
- 控制台：SPA 侧重走 6 个路由（材料 / 知识检索 / 知识问答 / 助手 / 智能体管理 / 技能管理）后读取 —— 0 条消息；Swagger 页 1 条非 CSP 错误（授权前的 401 试调，见 5.3 说明），两页均无 CSP 违规。
- 存储：登出前 `sessionStorage` 键恰为 `["campusclaw_token"]`、`localStorage` 零键；登出后两者均为空。
- 结论：通过。

### 7.1 openspec validate --strict — 通过

- 命令：`openspec validate add-tutor-agents-skills-streaming --strict`。
- 关键输出：`Change 'add-tutor-agents-skills-streaming' is valid`，退出码 0，无 error。
- 结论：通过。

### 7.2 全量自动化检查 — 通过

- 后端：在验收栈的 stubgateway 容器内（golang:1.24、挂载 `backend/`、含 `.env` 环境变量；生产 api 为 distroless 无 shell）运行 `go vet ./... && go test ./...`，测试依赖经环境变量注入（`CAMPUSCLAW_TEST_DSN` / `_QDRANT_URL` / `_GATEWAY_URL` / `_EMBEDDING_DIM`）。
- 关键输出：vet 无输出；19 个包中 11 个测试包全部 `ok`（`ask`、`auth`、`config`、`db`、`gateway`、`index`、`knowledge`、`search`、`tutor`、`vector`、`cmd/stubgateway`），其余 `[no test files]`，0 失败。
- 前端（`frontend/`）：`npm run build`、`npm run lint` 见 6.2（均 exit 0）；`node --test src/api/*.test.ts` → `tests 8 / pass 8 / fail 0`（含 6.1 的 6 例）。
- 结论：通过。

### 7.3 确认仓库无真实密钥与 token — 通过

- `git ls-files | grep -x .env` → 无输出（`.env` 未被跟踪）。
- `git grep -n "Bearer ey"` → 唯一命中为已归档 change 的 tasks.md 中 verify 指令自身引用的命令字面（`openspec/changes/archive/2026-09-30-switch-to-jwt-bearer-auth/tasks.md:61`），并非 token；按真实 token 形状复查：`git grep -nE "eyJ[A-Za-z0-9_-]{12,}"` 与排除占位 `Bearer <token>` 的 `git grep -nE "Bearer [A-Za-z0-9_.-]{20,}"` 均无输出。
- `git grep -n "JWT_SECRET=\|API_KEY="` → 仅 `.env.example` 的占位值与归档 tasks.md 的 verify 文本：`JWT_SECRET=change-me-…`、`QDRANT_API_KEY=`（空）、`EMBEDDING_API_KEY=change-me-…`、`CHAT_API_KEY=change-me-…`。
- 结论：通过。
