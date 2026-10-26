## Context

现状（第 5 课已归档）：

- **作答链路**（`internal/tutor`）：取会话 → 锁定智能体 → 候选技能 → 词法路由（至多一个技能）→ 首轮改标题、提问落库 → 绑定材料内的 hybrid 检索 → 编号与 citations → 组装消息 → `gateway.ChatClient.StreamChat` 流式生成 → `OutputFilter` → 首段文字到达后才写出 200 与 `meta` → 落库、`done`。技能的 `tools_json` 只能是 `[]` 或 `["knowledge_search"]`，消息组装明确不带工具定义。
- **网关**（`internal/gateway`）：`Chat`（非流式，供 `/api/ask`）与 `StreamChat`（流式）两种调用，都按 OpenAI 兼容协议。开发编排的确定性网关桩 `cmd/stubgateway` 监听容器内 8090，宿主绑定 `127.0.0.1:8090`，`/control` 可切换故障与指定输出。
- **认证与边界**：`RequireAuth` 按 token 的 `sub` 回库取角色与班级；`RequireTeacher` 在读请求体之前返回 403；按编号读取一律在 SQL 中带 `class_id`，取不到即统一的 404。
- **迁移**：`go:embed` 的迁移执行器，现有 `001`–`003`；种子按预置键幂等。
- **文档**：swag v1 注解生成 `backend/docs/`（Swagger 2.0）；`internal/swaggerui` 自托管 Swagger UI（无内联脚本）加载 `/swagger/doc.json`；`/swagger/` 先于 `/api/` 注册；nginx 在 server 级下发严格 CSP，反代 `/api/`、`/swagger/`、`/health` 与 SSE 路径。
- **编排**：生产 `docker-compose.yml` 有 web、api、db、qdrant，只有 web 映射宿主端口；开发 `docker-compose.dev.yml` 把 db 绑 `127.0.0.1:3306`、api 绑 `127.0.0.1:8081`、qdrant 绑 `127.0.0.1:6333`、网关桩绑 `127.0.0.1:8090`，**没有** web（nginx）。
- **账号**：预置 teacher_a、student_a1（A 班）与 student_b1（B 班）；**B 班没有教师账号**。
- 仓库里还没有任何 Python 代码与 `apitest/`。

动机与范围见 `proposal.md`；可验收行为见 `specs/` 下五份 delta。

## Goals / Non-Goals

**Goals:**

- 工具能力"教师可见、宿主可控、逐班隔离、处处留痕"：模型只能在宿主给出的候选集中挑工具，参数经宿主校验，令牌从不离开服务端，每次调用都有审计。
- 不涉及 MCP 工具的会话与第 5 课**逐字节等价**（同样的请求、同样的事件），把回归面压到"命中含 MCP 工具的技能"这一分支。
- 第 5 课的两项遗留（OpenAPI 3 的 `/openapi.json`、`apitest/`）以可重复的方式交付，`apitest` 的证据与数据库对得上。

**Non-Goals:**

- 见 `proposal.md` 的 Non-goals。
- 不追求通用 MCP 客户端：只支持 JSON 响应的 Streamable HTTP、只用 tools、不保持长会话。
- 不为工具阶段做流式（见 Decision 2）。

## Decisions

### Decision 1：MCP 服务器手写 JSON-RPC 2.0（不用 SDK）

`mcp-server/` 是独立 Go 模块（`go.mod` 只依赖标准库），结构：`main.go`（配置、路由、优雅退出）、`rpc.go`（解码、分发、错误码）、`auth.go`（Bearer + `subtle.ConstantTimeCompare`）、`tools.go`（工具注册表与 `inputSchema`）、`formula.go`、`units.go`。

- 请求处理：`http.MaxBytesReader(64KB)`，超限 413 → 鉴权（401 先于解析，避免未鉴权者探测方法）→ `json.Unmarshal`，失败 `-32700` → 校验 `jsonrpc=="2.0"`、`method` 非空，失败 `-32600` → 按方法分发，未知 `-32601` → 工具不存在或参数不合法 `-32602`。没有 `id` 的消息视为通知：`notifications/initialized` 返回 202、无响应体。
- `initialize` 返回 `protocolVersion`（客户端请求的版本若在支持列表中则回显，否则返回本服务支持的最新版本）、`capabilities: {tools: {}}`、`serverInfo: {name: "campus-tools", version}`。服务无状态，不签发 `Mcp-Session-Id`。
- 工具内部错误（不支持的单位等）走 `result.isError=true`，协议错误才走 JSON-RPC error——这是 MCP 规范的区分。
- `formula_hint`：内置约 20 个数学 / 物理主题（勾股定理、一元二次方程、三角函数、等差 / 等比数列、牛顿第二定律、动能定理、欧姆定律……），按"主题表中的关键词是否出现在 `topic` 中"匹配（因此传入整句也能命中），只返回公式与思路提示；`level` 只影响措辞详略。未命中返回「暂无该主题的提示」。
- `unit_convert`：长度（mm/cm/m/km/in/ft）、质量（mg/g/kg/t/lb）、时间（ms/s/min/h/day）三张换算表，先归一到基准单位再换算；跨量纲或未知单位返回 `isError`。
- 日志只记方法名、工具名、耗时与结果状态，**从不**记录请求头与参数。Dockerfile 多阶段构建，最终镜像不含任何 `ARG/ENV MCP_AUTH_TOKEN`，令牌只在运行时由 Compose 注入。

**为何手写**：协议面很小（4 个方法、5 个错误码、单请求单响应），手写能让课程中"JSON-RPC 帧长什么样、错误码如何区分、通知为何是 202"完全可见、可单测；第三方 SDK 会引入会话管理、SSE 与传输抽象，体量远超需求，也违背"标准库实现"的约束。代价是不跟随协议新特性，本课范围内可以接受。

### Decision 2：两段式——非流式工具阶段 + 流式正文阶段

```
… 检索 → citations → 路由命中技能
  └─ 技能含 MCP 引用？── 否 → 与第 5 课完全相同（不带 tools）
         └─ 是 → 解析候选集（不可用的记 denied/not_connected, round 0）
                 └─ 候选集非空 → 工具阶段（≤2 轮、≤3 次、≤20 秒）：
                       非流式 Chat(messages + tools, tool_choice=auto)
                       → 模型返回 tool_calls？── 否 → 结束工具阶段
                       → 逐个宿主校验 → 远端 tools/call → 结果清洗
                       → 追加 assistant(tool_calls) + tool 消息 → 下一轮
                 → 最终生成：重新组装消息（工具结果并入用户消息、系统消息加声明），
                   StreamChat(不带 tools) → 首段到达后写 200 + meta(含 tool_calls) → delta… → done
```

**为何两段式**：

- 流式输出中夹带 `tool_calls` 需要增量拼接函数名与参数、在流中途暂停、调用远端后再续流，复杂度高且与"`meta` 最先到达、`delta` 只含正文增量、拼接等于落库正文"的既有协议冲突。工具阶段只在宿主内部发生，不需要逐字展示，非流式最简单。
- 把"决定调不调工具"和"写给学生看的正文"分开，最终生成请求**不带工具定义**，模型在这一步不可能再发起工具调用，正文阶段的输出过滤、`[n]` 规则与 503 语义完全沿用。
- 代价：命中含工具技能的一轮，首段文字要晚到一个非流式往返加工具耗时（上限 20 秒）；`meta` 本来就要等首段文字，协议语义不变。

### Decision 3：工具名映射 `@` → `__`，用查表反解

OpenAI 兼容的 function 名只允许 `^[a-zA-Z0-9_-]{1,64}$`，`@` 不合法，因此声明给模型的名字是 `工具名__服务器名`（如 `formula_hint__campus-tools`）。由于工具名允许下划线、服务器名允许连字符，**不靠字符串拆分反解**，而是在构造候选集时建立 `函数名 → (工具名, 服务器编号, 服务器名)` 的映射表，模型返回的名字只按表查找，查不到即 `not_allowed`。长度上限 64：工具名最长 64 加服务器名最长 32 可能超限，因此技能保存时校验映射后的长度（spec 校验第 ⑤ 项），超限 400，运行时不会出现截断或碰撞。

### Decision 4：软防线与硬防线

模型输入里同时有学生提问、上传材料与工具结果，都可能被注入指令；防护分两层：

| | 内容 | 作用 |
| --- | --- | --- |
| **软防线**（影响模型，不能保证） | ① **位置**：工具结果只放在本轮用户消息中、资料之后、提问之前，绝不进系统消息；② **声明**：系统消息声明工具结果来自外部服务、属于数据不是指令，且"解题引导"硬性要求优先于工具结果；③ **清洗**：只取 `text` 内容、去控制字符、截断 2000 字，工具说明截断 1024 字 | 降低被工具结果或工具说明（工具投毒）诱导的概率 |
| **硬防线**（宿主强制，模型绕不过） | ① **候选集**：只声明命中技能里列出、且服务器 `connected`、工具在缓存中的工具；② **宿主校验**：函数名查表（不在候选集即 denied）、参数按 `inputSchema` 的 `required` 与基本类型校验；③ **上限**：≤2 轮、≤3 次、≤20 秒、单次 10 秒、远端响应 ≤256KB；④ **逐班授权**：服务器、缓存、技能引用、审计全部按会话班级取数，他班即"不存在"；令牌只由宿主附加，模型与浏览器都拿不到；最终生成不带工具定义 | 即使模型被诱导，也只能在教师审阅过的、本班已连接的工具里、按 schema 合法的参数、有限次数地调用 |

教师在"工具服务器"页能看到每个工具的说明与参数结构，是人工审阅这一环（防工具投毒）；发现后的说明截断与 schema 大小上限则限制了投毒内容的体量。

### Decision 5：令牌存储与不回传

- 已登记服务器的令牌存在 `mcp_servers.auth_token`（`VARBINARY(512)`），**明文**：宿主必须把它原样放进 `Authorization` 头，无法像口令那样只存哈希。加密存储需要再引入一把主密钥及其轮换，本课不做（列入 Non-goals），风险是数据库被整体拖走时令牌泄露——这与数据库凭据、用户数据的风险同级，缓解是数据库不对外暴露、只在本机绑定。
- **不回传**：读取服务器的 SQL 默认**不选** `auth_token` 列，只有 MCP 客户端在发请求的那一刻按编号取出；对外结构体没有令牌字段，只有 `has_token`；所有错误信息在返回或写日志前经 `redact(err, token)` 把令牌替换为 `***`（防远端在错误里回显）；审计表没有令牌列。
- `MCP_AUTH_TOKEN` 只在 `.env`；api 侧它是**可选**配置（只用于预置 `campus-tools`），`mcp-demo` 侧是**必需**配置（为空拒绝启动）。主机与 `mcp-demo` 共用同一个值，"预置后直接连接成功"就是对二者一致的验证。

### Decision 6：数据模型——迁移 `004_mcp.sql`

| 表 | 要点 |
| --- | --- |
| `mcp_servers` | `id`、`class_id` FK、`name VARCHAR(32)`、`base_url VARCHAR(200)`、`auth_token VARBINARY(512)`、`status ENUM('disconnected','connected','error')`、`last_error VARCHAR(255) NULL`、`tools_refreshed_at NULL`、`created_by` FK users、`seed_key NULL`、时间戳；`UNIQUE(class_id, name)`、`UNIQUE(class_id, seed_key)` |
| `mcp_tools` | `server_id` FK `ON DELETE CASCADE`、`name VARCHAR(64)`、`description VARCHAR(1024)`、`input_schema JSON`；`PRIMARY KEY(server_id, name)` |
| `mcp_tool_calls` | `id`、`class_id`、`user_id`、`conversation_id`、`server_id NULL`（FK `ON DELETE SET NULL`）、`server VARCHAR(32)`、`tool VARCHAR(64)`、`status ENUM('ok','error','denied')`、`reason VARCHAR(32) NULL`、`round TINYINT`、`duration_ms INT`、`created_at`；`KEY(class_id, created_at)` |

- 审计冗余存储服务器名与工具名：服务器删除后，历史记录仍可读。
- 技能对服务器的引用不建外键（存在 `skills.tools_json` 里）；删除服务器时扫描本班技能的 `tools_json` 判断是否被引用，被引用则 409 并列出技能名。
- 连接成功时在一个事务里"删除该服务器全部缓存 + 插入新结果 + 更新状态"，满足"整体替换"。

### Decision 7：主机侧 MCP 客户端与运行时调用

`internal/mcp.Client`：对 `<base_url>/mcp` 发 POST，头部 `Authorization: Bearer`、`Content-Type: application/json`、`Accept: application/json, text/event-stream`（声明兼容，但响应若不是 `application/json` 视为协议错误）；`MCP-Protocol-Version` 在握手后携带；若对方在 `initialize` 响应头返回 `Mcp-Session-Id`，后续同一次交互回带它。用 `io.LimitReader(256KB+1)` 读响应，超限即失败。错误分类为"令牌被拒绝（401/403）""无法连接到服务器（拨号失败 / 超时）""协议错误（非 JSON、JSON-RPC error、缺字段）"，映射为面向教师的提示。

- **连接**：5 秒总超时内完成 `initialize` → `notifications/initialized` → `tools/list`（不跟随 `nextCursor`，多页时只取第一页并记日志）。
- **运行时调用**：每次 `tools/call` 前做一次轻量握手（`initialize` + `initialized`）再调用，总超时 10 秒。自建服务器是无状态的，握手只是为了兼容要求先初始化的第三方服务器；不缓存会话（会话恢复是 Non-goal）。
- **运行时可用性**只看两件事：服务器 `status=connected`、工具在缓存中——都是一次本地查询；不在每轮对话前主动探活。

### Decision 8：工具循环的实现细节

- **候选集解析**：遍历命中技能的工具引用，跳过 `knowledge_search`，对 MCP 引用按本班查服务器与缓存；不可用的生成 `{status:denied, reason:not_connected, round:0}`。
- **宿主校验**：参数先 `json.Unmarshal` 为对象；按 `inputSchema.required` 检查缺项；对 `properties` 中声明了 `type` 的键检查 `string/number/integer/boolean/object/array` 的基本类型；不做 `enum`/`pattern` 等完整 JSON Schema 校验（多余参数放行，交给远端再校验）。
- **计数与计时**：轮次从 1 开始；每次模型提出的调用无论成败都计入"次数"，第 4 次起记 `denied/limit`；工具阶段持有一个 20 秒的 `context` 截止时间，到期后剩余调用记 `denied/limit` 并结束工具阶段。
- **回传格式**：每个 tool_call 都追加一条 `role:"tool"` 消息（`tool_call_id` 对应），内容为清洗后的结果，或 `调用失败：<可读原因>`；被 denied 的调用回传 `调用被拒绝：<原因>`，让模型知道不要重试。
- **最终生成**：从数据库重新组装（与第 5 课相同的历史窗口），本轮用户消息按"资料 → 工具结果 → 提问"拼接；只并入 `status=ok` 的结果；系统消息追加第 ⑥ 段声明。
- **审计写入**：工具阶段结束、写出 `meta` 之前，把本轮全部调用记录批量插入 `mcp_tool_calls`（即使之后正文阶段 503，审计也已留存）。
- **网关**：`ChatClient` 增加 `ChatWithTools(ctx, messages, tools) (assistantMessage, error)`，返回 `content` 与 `tool_calls`；`/api/ask` 仍用原 `Chat`，不受影响。

### Decision 9：`meta.tool_calls` 与前端标签

`meta` 结构增加 `ToolCalls []ToolCallSummary \`json:"tool_calls,omitempty"\``，未涉及 MCP 时为空切片并被省略，因此不含工具的会话输出与第 5 课逐字节相同。元素只有 `tool`（`工具名@服务器名`）、`status`、`reason`、`round`、`duration_ms`。助手页在回答气泡上方渲染小标签：`ok` 用常规样式，`error` 用 `--error` 色，`denied` 用醒目的描边样式并附原因文字；历史会话不回放标签（`meta` 不落库，规约中写为 MAY）。

### Decision 10：`/openapi.json` 由 Swagger 2.0 转换

swag v1 只能产出 Swagger 2.0，而课程清单要求 `openapi.json`（OpenAPI 3）。可选方案：① 换用实验中的 swag v2 生成 OAS 3.1——版本不稳定、注解有差异，会让已提交文档整体漂移；② 手写一份 OAS3——与"由注解生成、与代码一致"的要求冲突；③ **启动时转换**：`github.com/getkin/kin-openapi/openapi2conv.ToV3` 把 `docs.SwaggerInfo.ReadDoc()` 转为 OAS 3.0.x，内存中缓存序列化结果，由 `GET /openapi.json` 返回（`Content-Type: application/json`）。选 ③：单一事实来源仍是注解，转换由成熟库完成，失败时 api 启动即报错（转换只依赖仓库内文档，不受运行环境影响）。

- `/openapi.json` 与 `/swagger/` 一起在业务路由之前注册，不经认证与 `NoStore`；nginx 增加 `location = /openapi.json` 反代到 api（不写 `add_header`，继承 CSP）。
- 自托管的 `swagger-initializer.js` 改为加载 `/openapi.json`；`/swagger/doc.json` 保留不动。
- Swagger 2.0 的 `apiKey` 安全定义转换后仍是 `apiKey in header`，在页面 Authorize 中填写 `Bearer <token>`，与第 5 课一致。
- 新接口的注解覆盖 400/401/403/404/409/502 与 `@Security BearerAuth`；"文档与注解一致"仍以重新生成后 `git diff --exit-code backend/docs` 为准，`/openapi.json` 由其派生，无需另行比对。

### Decision 11：apitest 的组织、读库方式与运行目标

- **组织与收集**：`pytest.ini` 设 `python_files = *.py`、`python_functions = test_*`、`testpaths = cases`，并启用 `--import-mode=importlib`——否则多个同名的 `POST.py` 会因模块名冲突而无法收集。`cases/**/` 下不放 `__init__.py`。标记 `gateway` 在 `pytest.ini` 中注册。
- **读库方式**：用 `pymysql` 直连 MySQL，账号用应用账号（`APITEST_DB_USER` 等，本机 `.env` 中与 api 相同的 `DB_USER/DB_PASSWORD`），只用于**核对**与**清理本次测试数据**；不经任何"测试专用接口"，避免为测试在产品里开后门。为此开发编排的 db 宿主端口改为 `127.0.0.1:3307`（只绑本机）。查询全部参数化，清理语句只按本次创建的编号或 `apitest_<时间戳>` 前缀删除。
- **运行目标（需要你确认）**：生产编排的 db 不映射端口、也没有网关桩，读库与依赖网关的确定性断言都只能在**开发编排**上跑；而开发编排没有 nginx，api 在 `127.0.0.1:8081`。因此 README 指导在 `apitest/.env` 中把 `API_BASE_URL` 设为 `http://localhost:8081`；按需求，代码中的缺省值仍是 `http://localhost:8080`。用缺省值跑到生产编排时，读库用例会因连不上 3307 而**明确失败**并提示原因，而不是静默跳过。
- **B 班教师**：预置账号没有 B 班教师。`conftest.py` 的会话级 fixture 在运行时插入临时用户 `apitest_<ts>_teacher_b`（复制 teacher_a 的 `password_hash`、`role=teacher`、`class_id=B`，于是用 `APITEST_TEACHER_A_PASSWORD` 即可登录），结束时删除。这样不修改种子与 `auth-upload`「预置核心数据」规约。登录口令通过 `APITEST_TEACHER_A_PASSWORD` 等变量提供（需求列出的变量里没有口令，但不提供就无法登录，故增补）。
- **SSE 解析**：`lib/sse.py` 用 `requests` 的 `stream=True` + `iter_lines`，按空行切帧，返回 `[(event, data_json)]`。
- **网关依赖**：`gateway` 用例先发起一次流式提问，若返回的是 503 JSON 则 `pytest.skip("对话网关不可用：<响应>")`；报告中显示为 skipped，`run_all.py` 的退出码不把 skip 当作通过（仅在全部非 skip 用例通过时为 0，并在汇总中列出 skip 数）。
- **确定性**：开发编排下网关是网关桩，因此"模型会不会调用工具"由桩决定（Decision 12），端到端用例可重复；接真实网关时它真正检验模型行为，失败时打印 `meta`。
- **报告**：`run_all.py` 调用 `pytest.main([... "--html=reports/report-<ts>.html", "--self-contained-html"])`，退出码透传；`.gitignore` 增加 `apitest/reports/`、`apitest/.env`、`__pycache__/`、`.pytest_cache/`。

### Decision 12：网关桩支持确定性的工具调用；端口让位

- 桩的 `/chat/completions` 在**非流式**且请求带 `tools` 时：若消息中还没有 `role:"tool"` 消息，就对第一个声明的工具返回一个 `tool_calls`，参数按其 `parameters.required` 生成（字符串取本轮「学生提问：」后的文字，数值取 1）；已有工具消息时返回普通文本。`/control` 增加"指定工具调用（函数名与原始参数字符串，可多条）"开关，用于构造 `not_allowed`、`invalid_args`、`limit` 场景；`/stats` 照旧记录最近一次请求体（用于核对最终请求不含 `tools`、用户消息中工具结果的位置）。
- 端口：需求要求 `mcp-demo` 在宿主绑定 `127.0.0.1:8090`，而网关桩已占用该宿主端口。网关桩容器内端口不变（api 仍以 `http://stubgateway:8090` 访问），宿主映射改为 `127.0.0.1:8091`；开发编排同时加入 `mcp-demo`（`127.0.0.1:8090`），以便开发栈上也能完成端到端。README 与第 5 课文档中的 `$STUB` 地址随之更新。

### Decision 13：前端

- 路由 `/admin/mcp`，`TeacherOnly` 包装，导航教师标签增加「工具服务器」（位于「技能管理」之后）。
- 页面三块：服务器列表（登记表单：名称、地址、令牌——令牌输入框 `type=password`、提交后清空、从不回显）；选中服务器后的工具清单（名称、说明、`input_schema` 以 `<pre>` 纯文本格式化展示）；本班调用审计表（最近 50 条，可刷新）。连接失败显示服务端的可读提示。
- 技能管理页无需改动：保存时服务端返回的 400 原文已会显示（如"服务器 campus-tools 未连接"）。
- 助手页按 Decision 9 渲染工具标签。所有新增样式进 `index.css`，无内联脚本与样式。

### Decision 14：配置

`internal/config` 增加：`MCP_AUTH_TOKEN`（可选）；`MCP_CONNECT_TIMEOUT`（默认 5s）、`MCP_CALL_TIMEOUT`（默认 10s）、`MCP_TOOL_STAGE_TIMEOUT`（默认 20s）、`MCP_MAX_RESPONSE_BYTES`（默认 262144）作为可选项集中读取；非法值启动失败并给出可读错误。`mcp-server` 只读 `MCP_AUTH_TOKEN`（必需）与 `MCP_ADDR`（默认 `:8090`）。

## Risks / Trade-offs

- **[工具结果中的提示注入]** 外部服务返回的文字可能诱导模型 → 软防线（位置、声明、清洗）加硬防线（最终生成无工具、候选集、宿主校验、上限）；即使模型被诱导，最多只影响本轮正文措辞，无法越权调用。
- **[工具投毒（恶意说明或 schema）]** → 教师审阅页面、说明截断、schema 大小上限、只有教师能登记与连接；仍依赖教师判断。
- **[令牌明文存库]** → 数据库只绑本机或不映射、读取默认不选该列、响应与日志脱敏；加密存储留待后续。
- **[首段延迟增加]** 命中含工具技能的一轮多一次非流式往返加工具耗时（≤20 秒）→ 只影响这一分支；nginx 读超时 120 秒足够。
- **[运行时只看本地状态]** 服务器状态为 `connected` 但实际已宕机 → 运行时调用失败记 `error` 并回传模型，回答照常完成；教师可在审计中发现并重连。
- **[每次调用都握手]** 多 2 个请求 → 换取对需要初始化的第三方服务器的兼容；在 10 秒单次上限内。
- **[`tools/list` 分页只取首页]** → 自建服务器不分页；第三方大服务器的后续页被忽略并记日志。
- **[apitest 的运行目标]** 缺省 `API_BASE_URL` 与可读库的开发编排不一致 → 用 `.env` 覆盖并在 README 中写明；读库失败时给出明确原因。
- **[临时 B 班教师]** 测试中断可能留下临时账号 → 前缀统一、下次运行开始时先清理残留的 `apitest_` 数据。
- **[开发端口调整]** db 3306→3307、网关桩 8090→8091 会使旧的本地脚本失效 → README 与 tasks 统一更新；生产编排不受影响。
- **[OAS 转换的细节差异]** `openapi2conv` 对 `formData`、`produces` 的转换可能与手写 OAS3 略有不同 → 以页面能试调为验收标准；不手工修补转换结果。

## Migration Plan

1. 在 `.env` 中加入 `MCP_AUTH_TOKEN`（随机串）；`docker compose up --build -d`。迁移 `004` 随 api 启动执行；种子预置 A 班 `campus-tools`（未连接）。
2. 教师在「工具服务器」页连接 `campus-tools`，审阅工具后在技能中引用。
3. 开发环境：`docker compose -f docker-compose.dev.yml up -d`；注意 db 在 `127.0.0.1:3307`、网关桩在 `127.0.0.1:8091`、`mcp-demo` 在 `127.0.0.1:8090`。
4. 回滚：回退代码并重建；`004` 只新增表，旧版本可在新库上运行；已保存的含 MCP 引用的技能在旧版本中会因"工具只能是 knowledge_search"而无法再次保存（读取与作答不受影响），回滚前可先把这些技能的「工具」改回 `knowledge_search`。
