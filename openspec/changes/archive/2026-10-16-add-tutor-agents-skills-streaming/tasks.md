> Apply 约定：一个 task 一轮 —— 读 task → 实现 → 对照 spec 审查 diff → 运行 verify → 通过后勾选 `[x]` → 提交。verify 未通过不得勾选，不得带入下一项。**第 1 节（身份来源调整）必须全部完成后才能开始第 2 节。**
> 开发环境：`docker compose -f docker-compose.dev.yml up -d`（db、qdrant、网关桩、api 均绑定 `127.0.0.1`）；`$API=http://localhost:8081`，`$STUB=http://localhost:8090`，`$WEB=http://localhost:8080`（生产编排）。账号：teacher_a / student_a1（A 班）、student_b1（B 班），口令取自本机 `.env`。
> 取 token：`TOKEN=$(curl -s -H 'Content-Type: application/json' -d '{"username":"student_a1","password":"…"}' $API/api/login | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')`。伪造 token 用 `python3` 的 `hmac` + `base64.urlsafe_b64encode` 现场构造（密钥取自 `.env` 的 `JWT_SECRET`，或故意用错误密钥）。SQL 用 `docker compose -f docker-compose.dev.yml exec -T db mysql --default-character-set=utf8mb4 -uroot -p… campusclaw -e "…"`。
> 网关桩：`GET $STUB/stats` 查看调用次数与最近一次请求体（用于核对系统消息、资料与"是否调用"）；`POST $STUB/control` 切换"首段前失败""第 N 段后中断""指定输出文字"等开关（具体字段在 4.6 实现时确定并写入 README）。
> 证据统一记录到 `docs/iteration-3.md`：每条 verify 的命令、关键输出、"通过 / 不通过"。

## 1. 身份来源调整（MODIFIED auth-upload，最先完成）

- [x] 1.1 改造签发：`Issue(userID)` 只写 `sub`（十进制字符串）、`jti`、`iat`、`exp`、`iss=campusclaw-api` — verify: `go test ./internal/auth -run Issue` 断言解码后的载荷键集合恰为这五项、`exp-iat=24h`、两次签发 `jti` 不同；`curl` 登录后用 `python3` 解码 token 载荷，键集合与之一致
- [x] 1.2 改造解析：限定 HS256、`exp` 必填、`iss` 匹配；解析为 `MapClaims` 后要求键集合恰为五项、`sub` 为正整数、`jti` 非空 — verify: `go test ./internal/auth` 表驱动覆盖：正确 token 通过；他钥签名、`alg=none`、`HS512`、过期、`iss` 不符、缺 `sub`/`jti`/`exp`、`sub="abc"`/`"0"`、含 `role` / `class_id` / `user_id` / `username` 的旧格式 token，全部报错
- [x] 1.3 新增 `db.GetIdentity(userID)`：`users JOIN classes` 取用户名、角色、班级编号与班级名，无行返回 `ErrNotFound` — verify: 集成测试对存在用户返回正确字段，对 999999 返回 `ErrNotFound`；停掉 db 时返回非 `ErrNotFound` 的错误
- [x] 1.4 `RequireAuth` 改为"解析（不查库）→ 吊销表 → 回库取身份 → 写上下文"，`GET /api/me` 直接用上下文中的班级名 — verify: `curl -i -H "Authorization: Bearer $TOKEN" $API/api/me` 返回 200 且字段与改动前一致；用旧格式 token（含 `role` 等字段、密钥正确）请求得 401；`stop db` 后带有效 token 请求 `/api/materials` 得 503，`start db` 后同一 token 得 200
- [x] 1.5 数据库中的班级与角色变更立即生效 — verify: SQL 把 student_a1 的 `class_id` 改为 B 班后，用原 token `GET /api/materials` 只返回 B 班材料，改回后只返回 A 班；把 teacher_a 的 `role` 改为 `student` 后用原 token 上传得 403，改回后上传得 201；全程不重新登录
- [x] 1.6 删除用户后 token 立即失效 — verify: SQL 新建临时用户 `tmp_user`（口令哈希复制 student_a1 的、班级 A），登录取 token，`/api/me` 得 200；`DELETE FROM users WHERE username='tmp_user'` 后同一 token 得 401
- [x] 1.7 登录换发：口令校验通过后，若请求带通过 ①–③ 的旧 token，先吊销其 `jti` 再签发；吊销写库失败返回 503 且不签发；口令错误或被限流时不碰旧 token；携带无效 token 不影响登录 — verify: 带旧 token 甲登录得乙后，甲调用 `/api/me` 得 401、乙得 200；不带头登录得丙后，乙仍 200；带乙但口令错误得 401 后，乙仍 200；带乱码 token 用正确口令登录得 200；登录响应体键集合仍恰为 `token, username, role`
- [x] 1.8 伪造矩阵回归 — verify: 脚本对 `GET /api/me` 依次发送：他钥签名、在载荷中加入 `role=teacher`（签名未重算）、改 `sub`（签名未重算）、`alg=none`、`HS512`、过期、`iss=other`、缺 `sub`、缺 `jti`，全部 401 且响应体逐字节相同；放在 query / Cookie 的有效 token 得 401
- [x] 1.9 前端登录携带已有 token：`login()` 在 `sessionStorage` 有 token 时加 `Authorization`，成功后覆盖保存 — verify: 新增 `frontend/src/api/client.login.test.ts`，用 `node --test` 运行（模拟 `sessionStorage` 与 `fetch`）断言有 token 时登录请求带 `Authorization: Bearer <旧>`、无 token 时不带；`cd frontend && npm run build && npm run lint` 通过
- [x] 1.10 既有行为回归 — verify: `cd backend && go test ./...` 全部通过；按 `docs/jwt-bearer-auth.md` 中的关键命令重跑：学生上传 403、跨班详情 / 下载与 999999 响应 `diff` 无输出、登出后旧 token 401、`/health` 与 `/api/login` 无需 token、限流 5 次后正确口令仍 401

## 2. 存储

- [x] 2.1 新增迁移 `003_tutor.sql`：`tutor_agents`（含 `seed_key`）、`skills`、`tutor_agent_skills`、`tutor_agent_materials`、`conversations`、`messages`，唯一键、外键与级联按 design Decision 2 — verify: 连续重启 api 两次均成功，`schema_migrations` 中 `003` 恰一行；`SHOW CREATE TABLE` 六张表含约定的唯一键与 `ON DELETE CASCADE`；`INSERT INTO messages(... role='system' ...)` 报错；删除一条会话后其消息行数为 0；删除一条技能后其绑定行数为 0
- [x] 2.2 `internal/db` 增加智能体、技能、绑定、会话、消息的读写，按编号读取一律带 `class_id`（会话再带 `user_id`，学生侧与新建会话再带 `enabled=1`） — verify: 集成测试：用 A 班身份读取 B 班智能体 / 技能 / 会话均为 `ErrNotFound`；读取同班他人会话为 `ErrNotFound`；绑定校验对"含 B 班编号""含不存在编号"返回 `ErrNotFound` 且绑定未改变（事务回滚）
- [x] 2.3 实现技能文件解析 `tutor.ParseSkill`（纯函数，错误带行号或字段名） — verify: `go test ./internal/tutor -run ParseSkill` 表驱动覆盖：合法文件（含开头 `#` 行、全角与半角冒号、正文内 `#` 与空行原样保留）；字段重复、未定义字段、非"字段：值"行、工具为 `web_search`、缺「适用条件」、「启用：可能」、无正文、名称 33 字、文件 8001 字，均返回含行号或字段名的中文错误
- [x] 2.4 种子：A 班两份预置技能、每班一个已启用的默认智能体（A 班的绑定两份技能及首次创建时已有的 A 班材料），按 `class_id + seed_key` 幂等 — verify: `docker compose -f docker-compose.dev.yml down -v` 后重新启动：A 班技能 2 条且均启用、每班默认智能体 1 个且启用、A 班默认智能体绑定 2 条技能；连续重启两次各表行数不变；把默认智能体改名并改提示词后重启，修改保留且没有新增智能体

## 3. 接口

- [x] 3.1 增加 `RequireTeacher` 中间件（读请求体前返回 403），并实现 `GET/POST /api/agents`、`PUT /api/agents/{id}`（整体替换，绑定在事务内校验与写入） — verify: teacher_a 创建、修改（改名、停用、换绑定）成功且 `GET` 反映最新状态；student_a1 的 `GET` 只含已启用项且每项只有 `id`、`name`；student_a1 的 `POST`/`PUT` 得 403 且表无变化；绑定 B 班或不存在的技能 / 材料得 404，且响应体与 `PUT /api/agents/999999` 逐字节相同；同名、名称 65 字、提示词 2001 字、空白名称均得 400；空提示词、空绑定创建成功
- [x] 3.2 实现 `GET/POST /api/skills`、`PUT/DELETE /api/skills/{id}`（提交 `{"source": 原文}`，按 `ParseSkill` 解析） — verify: teacher_a 创建、修改（启用改为「否」）、删除成功；删除后原先绑定它的智能体的 `skill_ids` 不再含它；7 种不合规文件各得 400 且 `error` 含行号或字段名、表无变化；同名得 400；student_a1 的 `GET` 只含启用项且无 `source_text`，`POST`/`PUT`/`DELETE` 得 403；对 B 班技能 `PUT`/`DELETE` 得与 999999 相同的 404
- [x] 3.3 实现 `GET/POST /api/conversations`、`GET /api/conversations/{id}` — verify: student_a1 新建两个会话标题均为「新的提问」；用已停用、B 班、不存在的智能体新建均得 404 且响应体相同；SQL 停用 A 班全部智能体后新建得 400（之后恢复）；列表只含本人会话；student_a1 请求 teacher_a 的会话、student_b1 的会话与 999999 的详情，三者状态码与响应体逐字节相同
- [x] 3.4 请求参数不被采信 — verify: student_a1 调用 `GET /api/agents?class_id=<B 班>`、`GET /api/skills`（Header 带 `X-Class-Id: <B 班>`）、`POST /api/conversations`（请求体附带 `class_id`、`user_id`、`role`），结果与不带这些参数时相同，新建会话归属 student_a1 与 A 班

## 4. 编排（作答链路与流式）

- [x] 4.1 `search.Engine` 增加限定材料集合的混合检索；`vector.Client` 支持按材料编号过滤 — verify: 集成测试：集合 {甲} 时结果只含材料甲的切片（两路候选都不含乙）；集合 {A 班甲, B 班丙} 只可能返回甲；空集合返回空且 `$STUB/stats` 的嵌入调用次数不变；`POST /api/search` 附带 `material_ids` 时结果与不带时 `diff` 无输出
- [x] 4.2 实现词法路由 `tutor.Route` — verify: `go test -run Route` 覆盖：中英文逗号 / 顿号 / 分号 / 竖线切分、去空白丢空词、大小写不敏感、按命中词数取最高、同分取编号较小、全零不选、至多一个
- [x] 4.3 实现资料后处理：剔除空正文 → 资料范围按标题包含过滤 → 从 1 连续编号生成 citations — verify: `go test -run Citations` 覆盖空正文剔除、范围过滤后编号连续（如剔除第 2 条后为 1、2、3）、citations 顺序与编号一致、摘录沿用 300 字截断
- [x] 4.4 实现消息组装 `tutor.BuildMessages`（固定顺序的单条系统消息、默认角色、"解题引导"硬性要求、未命中文案、有 / 无资料两种要求、不说明模型、数据边界声明；历史末尾 6 条每条 500 字；本轮提问改写为"编号资料 + 学生提问"，每条资料 800 字；不带工具） — verify: `go test -run BuildMessages` 用快照断言各种组合的完整消息数组，并断言系统消息不含任何资料正文、非系统消息不超过 6 条、最后一条以资料开头以「学生提问：」结尾
- [x] 4.5 实现流式安全的输出过滤 `tutor.OutputFilter` — verify: `go test -run OutputFilter` 覆盖：`[1` / `2]` 跨片段拆分；引用数为 3 时保留 `[1]`、`[3]`，移除 `[7]`；引用数为 0 时移除全部 `[n]`；`[a]`、`[` 结尾等非标注按原文输出；禁用文案的逐字前缀被暂存、偏离后释放、整段等于时报告失败；所有输出片段拼接等于期望全文
- [x] 4.6 对话网关流式调用 `ChatClient.StreamChat`（`stream:true`）；网关桩支持流式分段返回，并在 `/control` 增加"首段前失败""第 N 段后中断""指定输出文字"开关；`/api/ask` 保持非流式 — verify: 对网关桩的集成测试得到 ≥ 2 段 delta；"首段前失败"与"中途中断"被区分为不同错误；调用一次 `/api/ask` 后 `$STUB/stats` 记录的请求体中 `stream` 为 `false`
- [x] 4.7 实现 `POST /api/conversations/{id}/stream` 全链路：问题 2–500 字校验、会话锁（409）、第 1–11 步、首段文字到达后才写出 200 与 `meta`、SSE 头与逐帧 Flush、落库与 `done` — verify: `curl -N -i` 得到响应头 `Content-Type: text/event-stream`、`Cache-Control: no-cache`、`Connection: keep-alive`、`X-Accel-Buffering: no`；事件顺序为 `meta` → ≥ 2 个 `delta` → `done`；所有 `delta.text` 拼接与 SQL 查出的该 `message_id` 正文逐字节相同；首轮后会话标题等于问题前 24 字，`updated_at` 已刷新；生成中再调用同一会话得 JSON 409
- [x] 4.8 流开始前的失败为普通 JSON — verify: 无 token、他人会话、1 个字的问题、`stop qdrant`、桩"首段前失败"，依次得 401、404、400、503、503，`Content-Type` 均为 JSON 且带 `Cache-Control: no-store`；桩"首段前失败"时该轮无助手消息落库，用户提问已落库
- [x] 4.9 流开始后的失败与断开 — verify: 桩"第 2 段后中断"时客户端收到 `meta`、2 个 `delta` 与 `error`（`message` 为通用文案），会话详情中出现正文等于这 2 段拼接的助手消息；用 `curl -N --max-time 1` 在首段后主动断开，稍后会话详情中出现正文等于已推送部分的助手消息
- [x] 4.10 只在绑定集合内作答 — verify: teacher_a 上传标题含「数学」与「语文」的两份材料（内容都含同一句话），新建只绑「数学解题引导」与数学材料的智能体；student_a1 用它新建会话，问一道含该句话的语文阅读题：`meta.skill` 为 `null`，`citations` 只含数学材料，`$STUB/stats` 记录的最后一条用户消息不含语文材料
- [x] 4.11 会话锁定智能体 — verify: 在锁定智能体甲的会话中提问，请求体附带智能体乙的 `agent_id`；`$STUB/stats` 记录的系统消息以甲的提示词开头，`citations` 只来自甲绑定的材料
- [x] 4.12 「解题引导」规则与启停 / 解绑 — verify: 同一道数学题先在命中「数学解题引导」时提问，`$STUB/stats` 的系统消息含"不得写出最终答案、选项、得数或完整解题过程"；教师停用该技能后再问，系统消息不含该要求且含「这次问题没有匹配到已启用的学科技能，请直接回答」；重新启用但从智能体解绑后再问，结果同停用；若有真实网关，再记录一次真实回答是否给出得数（无真实网关则记为"未验证"）
- [x] 4.13 检索落空仍作答且无编号 — verify: 桩"指定输出文字"为「答案见[1]和[2]。」，在绑定了材料的会话里问一个无依据的问题：`$STUB/stats` 对话调用次数 +1，`meta.citations` 为 `[]`，`delta` 拼接与落库正文均为「答案见和。」；把输出设为「资料中未找到相关内容」再问，得 JSON 503 且无助手消息落库
- [x] 4.14 引用编号连续且一一对应 — verify: 桩输出「[1]…[3]…[7]」，一次有 3 条引用的提问：`meta.citations` 的 `index` 依次为 1、2、3，推送与落库正文含 `[1]`、`[3]` 且不含 `[7]`
- [x] 4.15 已停用智能体的已有会话 — verify: 会话创建后 SQL 停用其智能体，再在该会话提问，得到完整事件流与 `done`；用该智能体新建会话得 404
- [x] 4.16 `frontend/nginx.conf` 为流式路径关闭缓冲与缓存、读超时 120 秒，并保持继承 CSP — verify: `grep -n "proxy_buffering off\|proxy_cache off\|proxy_read_timeout 120s" frontend/nginx.conf` 均有输出且该 location 内无 `add_header`；生产编排下 `curl -N` 经 `$WEB` 调用并用 `ts`（或 Python 记录接收时间）显示首个 `delta` 早于 `done` 到达
- [x] 4.17 `/api/ask` 口径不变 — verify: 存在智能体与会话时，student_a1 用无依据的问题（请求体附带 `agent_id`）调用 `/api/ask`，返回「资料中未找到相关内容」与空 `citations`，`$STUB/stats` 对话调用次数不变

## 5. 接口文档

- [x] 5.1 为登录、当前身份、材料、检索、问答、智能体、技能、会话、流式提问的 handler 添加 swaggo 注解与全局注解（含 `BearerAuth`），用固定版本的 `swag` 生成 `backend/docs/` 并提交 — verify: 重新执行生成命令后 `git diff --exit-code backend/docs` 无差异；`swagger.json` 中包含上述全部路径与 `securityDefinitions.BearerAuth`
- [x] 5.2 自托管 Swagger UI：`/swagger/` 提供自写 `index.html` 与外链 `swagger-initializer.js`（资源来自 `swaggo/files/v2`），在业务路由之前注册、不经认证与 `NoStore`；nginx 增加 `/swagger/` 反代（不写 `add_header`） — verify: 不带 token `curl -sI $WEB/swagger/` 得 200 且含 `Content-Security-Policy`（`script-src 'self'`、无 `unsafe-inline`）；对自写的 `index.html` 执行 `grep -n "<script"`，每一行都带 `src=`（无内联脚本）；浏览器打开 `$WEB/swagger/` 控制台无 CSP 违规
- [x] 5.3 在文档页面试调 — verify: 浏览器中在 `$WEB/swagger/` 试调 `POST /api/login` 取 token，点 Authorize 填入 `Bearer <token>`，再试调 `GET /api/agents` 与 `GET /api/conversations` 均得 200 且只含本班 / 本人数据；截图或记录响应写入 `docs/iteration-3.md`
- [x] 5.4 文档不含密钥 — verify: `grep -rniE "jwt_secret|api_key|password" backend/docs` 只出现字段名（如登录请求的 `password` 参数）而无任何真实值；`.env` 中各密钥的值在 `backend/docs` 中 `grep` 无输出
- [x] 5.5 更新 README：技能文件格式与示例、新接口一览、`/swagger` 入口、网关桩开关、"发布后需重新登录"的说明 — verify: 按 README 的示例技能文件在技能管理接口上创建成功，按 README 打开 `/swagger/` 可见文档

## 6. 前端

- [x] 6.1 新增 `src/api/sse.ts` 帧解析器（`TextDecoder` 流式解码、空行切帧、`\r\n`、多行 `data`、跨 chunk 拆帧） — verify: `cd frontend && node --test src/api/sse.test.ts` 通过，覆盖一个帧被拆成 3 个 chunk、两个帧在同一 chunk、`\r\n` 换行、多行 `data` 拼接
- [x] 6.2 `api/client.ts` 增加智能体、技能、会话接口与 `streamConversation`（非事件流响应按状态码处理：401 清 token、503「服务暂不可用」、400/404/409 显示服务端 `error`） — verify: `npm run build && npm run lint` 通过；`grep -n "EventSource" frontend/src` 无输出（使用 fetch 读取响应体）
- [x] 6.3 导航与路由：所有人「材料」「知识检索」「知识问答」「助手」，教师另有「智能体管理」「技能管理」；`TeacherOnly` 使学生访问管理页回到材料页 — verify: 浏览器中 student_a1 看到 4 个标签、teacher_a 看到 6 个；student_a1 直接打开 `/admin/agents`、`/admin/skills` 回到 `/materials`；未登录打开 `/assistant` 回到登录页且无 `/api/conversations` 请求；当前标签 `aria-current="page"`
- [x] 6.4 助手页会话列表与新建会话 — verify: student_a1 打开助手页看到本人会话（按最近更新倒序）；「新建会话」下拉只列已启用的智能体；新建后出现在列表顶部且标题为「新的提问」
- [x] 6.5 逐段显示、出处与技能名 — verify: 提问后用页面脚本每 50ms 记录回答区文字长度，记录中出现 ≥ 2 个递增的不同值（非一次性出现）；完成后显示技能名与按 `[1]`、`[2]` 排列的出处（材料标题、切片序号、字符区间、摘录），与 `meta.citations` 一致；会话标题更新为问题前 24 字
- [x] 6.6 出处打开材料详情、历史会话重开 — verify: 点击出处显示该材料标题与正文；刷新页面后选择此前会话，历史消息、各轮出处与技能名完整显示
- [x] 6.7 助手页错误处理 — verify: 吊销当前 token 后提问回到登录页且 token 已清除；`stop qdrant` 后提问显示「服务暂不可用」；桩"第 2 段后中断"时页面保留已显示文字并提示回答中断；生成中再次提交时显示服务端 409 文案
- [x] 6.8 纯文本显示 — verify: 桩输出含 `<b>粗体</b><script>alert(1)</script>`，页面原样显示这些字符；先把 `window.alert` 替换为记录函数，确认未被调用，回答区内 `querySelector('b, script')` 为 `null`
- [x] 6.9 抽出 `MaterialDetailView` 供材料页与助手页复用 — verify: 材料页回归：搜索、详情（Markdown 表格渲染且 `<script>` 不执行）、下载、教师上传、主题切换、登出均正常
- [x] 6.10 智能体管理页 — verify: teacher_a 新建智能体、勾选「数学解题引导」与一份数学材料、保存后停用；列表显示停用状态，再次编辑时勾选与保存一致；保存同名时页面显示服务端 400 文案
- [x] 6.11 技能管理页 — verify: teacher_a 通过文件选择上传含未定义字段的技能文件，页面显示服务端错误原文（含行号）；改正后粘贴提交创建成功并出现在列表；编辑后启用状态变化可见；删除前有确认，删除后列表与智能体编辑页的技能勾选项中都不再出现
- [x] 6.12 生产 CSP 下全流程 — verify: `docker compose up --build -d` 后在 `$WEB` 依次完成登录、材料、知识检索、知识问答、助手会话提问（逐段显示）、智能体管理、技能管理、主题切换、`/swagger/` 试调、登出，浏览器控制台无 CSP 违规；`sessionStorage` 只有 token，`localStorage` 为空

## 7. 收尾与同伴交叉验证

- [x] 7.1 运行 `openspec validate add-tutor-agents-skills-streaming --strict` — verify: 退出码 0 且无 error
- [x] 7.2 全量自动化检查 — verify: `cd backend && go vet ./... && go test ./...` 通过；`cd frontend && npm run build && npm run lint && node --test src/api/*.test.ts` 通过
- [x] 7.3 确认仓库无真实密钥与 token — verify: `git ls-files | grep -x .env` 无输出；`git grep -n "Bearer ey"` 无输出；`git grep -n "JWT_SECRET=\|API_KEY="` 只出现在 `.env.example` 且为占位值
- [ ] 7.4 同伴交叉验证：会话归属 — verify: 同伴用 student_a1 与 teacher_a 的 token 分别请求对方会话的详情与流式提问接口，并请求 999999，三者响应逐字节相同（404）；`docs/iteration-3.md` 记录同伴姓名、命令、输出与结论
- [ ] 7.5 同伴交叉验证：智能体隔离 — verify: 同伴确认学生列表只见已启用智能体、学生调用智能体 / 技能维护接口得 403、教师绑定 B 班资源得 404、会话提问附带他智能体 `agent_id` 无效（系统消息仍为原智能体提示词）；记录同上
- [ ] 7.6 同伴交叉验证：班级边界 — verify: 同伴在数据库把 student_a1 的 `class_id` 改为 B 班后用原 token 请求材料列表只见 B 班、改回后恢复；student_a1 访问 B 班智能体 / 技能 / 会话均得与不存在一致的 404；删除临时用户后其 token 得 401；记录同上
- [ ] 7.7 同伴交叉验证：技能开关 — verify: 同伴对同一道数学题在"技能启用并绑定""技能停用""技能解绑"三种状态下各提问一次，记录 `meta.skill` 与网关桩系统消息中"解题引导"硬性要求的有无（应为 有 / 无 / 无）；记录同上
- [x] 7.8 归档本 change（需使用者明确指示后执行 `/opsx:archive`）— verify: `openspec list` 中无本 change；`openspec/specs/tutor-chat/spec.md` 已生成；`openspec/specs/auth-upload/spec.md`、`knowledge-retrieval/spec.md`、`knowledge-qa-ui/spec.md` 中被修改的 Requirement 已是新内容
