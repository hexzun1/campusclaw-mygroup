# MCP 工具 Specification

## Purpose

让教师把外部 MCP 工具（含本仓库自建的 `mcp-demo`）登记到本班、审阅后绑定到技能，使智能体会话在命中该技能时能在宿主严格校验与逐班授权之下调用这些工具，并把每一次调用（成功、失败、被拒）留下不含参数与结果原文的审计记录。

## Requirements

### Requirement: MCP 服务器的部署与鉴权

本仓库 MUST 包含自建 MCP 服务器 `mcp-server/`：使用 Go 标准库 `net/http` 实现，JSON-RPC 2.0 由本仓库代码处理，不引入第三方 MCP SDK；它有独立的 Dockerfile，由 `docker-compose.yml` 以服务名 `mcp-demo` 一并构建启动，容器内监听 8090，与 api 同网，主应用通过 `http://mcp-demo:8090` 访问。服务 MUST 提供 `GET /health`（无需令牌，返回 200 `{"status":"ok"}`）与 `POST /mcp`（Streamable HTTP，本系统只使用"一个请求对应一个 `application/json` 响应"的形式，不使用 SSE）。`POST /mcp` MUST 要求 `Authorization: Bearer <令牌>`，令牌取自环境变量 `MCP_AUTH_TOKEN` 并以常量时间比较；缺少令牌或令牌错误 MUST 返回 HTTP 401。`MCP_AUTH_TOKEN` 为空时服务 MUST 拒绝启动。令牌 MUST NOT 出现在 Git、镜像层或日志中。

#### Scenario: 健康检查无需令牌

- **WHEN** 不带令牌请求 `http://127.0.0.1:8090/health`
- **THEN** 响应 MUST 为 200 与 `{"status":"ok"}`

#### Scenario: 无令牌或错误令牌被拒绝

- **WHEN** 不带令牌、或带错误令牌向 `/mcp` 发送 `tools/list`
- **THEN** 响应 MUST 为 HTTP 401，且响应体与日志 MUST NOT 含正确令牌

#### Scenario: 令牌为空时拒绝启动

- **WHEN** `MCP_AUTH_TOKEN` 为空时启动 `mcp-demo`
- **THEN** 进程 MUST 以非零状态退出，并输出可读原因（不含任何令牌值）

#### Scenario: 令牌不进镜像

- **WHEN** 检查 `mcp-server/Dockerfile` 与构建出的镜像历史（`docker history --no-trunc`）
- **THEN** 其中 MUST NOT 出现 `MCP_AUTH_TOKEN` 的值，令牌只在运行时由环境变量注入

### Requirement: MCP 协议方法

`POST /mcp` MUST 支持以下方法：`initialize` 返回 `protocolVersion`、`capabilities.tools` 与 `serverInfo`（名称、版本）；`notifications/initialized` 是通知，MUST 返回 HTTP 202 且无响应体；`tools/list` 返回每个工具的 `name`、`description` 与 `inputSchema`（`type` 为 `object` 的 JSON Schema）；`tools/call` 返回 `content: [{type:"text", text}]`，工具内部出错时 MUST 返回 `isError: true` 与可读文本（而不是 JSON-RPC 错误）。成功响应的 `Content-Type` MUST 为 `application/json`。

#### Scenario: 握手与列出工具

- **WHEN** 带正确令牌依次发送 `initialize`、`notifications/initialized`、`tools/list`
- **THEN** 第一个响应 MUST 含 `protocolVersion`、`capabilities.tools` 与 `serverInfo.name`、`serverInfo.version`
- **AND** 第二个响应 MUST 为 202 且无响应体
- **AND** 第三个响应 MUST 列出 `formula_hint` 与 `unit_convert`，各含 `description` 与 `type` 为 `object` 的 `inputSchema`

#### Scenario: 调用工具返回文本

- **WHEN** 带正确令牌调用 `tools/call`，`name` 为 `formula_hint`，`arguments` 为 `{"topic":"勾股定理"}`
- **THEN** 结果 MUST 含 `content[0].type == "text"` 且 `text` 非空，`isError` MUST 不为 `true`

### Requirement: MCP 错误处理

`POST /mcp` MUST 按 JSON-RPC 2.0 返回错误：请求体不是合法 JSON 返回 `-32700`；不是合法的 JSON-RPC 请求（如缺少 `jsonrpc:"2.0"` 或 `method`）返回 `-32600`；未知方法返回 `-32601`；未知工具或参数不合法返回 `-32602`。错误的 `message` MUST 面向人可读。请求体超过 64KB MUST 返回 HTTP 413。

#### Scenario: 各类协议错误

- **WHEN** 带正确令牌分别发送非法 JSON、缺 `method` 的对象、方法 `tools/unknown`、调用不存在的工具 `nope`、调用 `formula_hint` 但缺少 `topic`
- **THEN** 错误码 MUST 依次为 `-32700`、`-32600`、`-32601`、`-32602`、`-32602`，且每个 `message` 都是可读文字

#### Scenario: 请求体过大

- **WHEN** 带正确令牌发送 65KB 的请求体
- **THEN** 响应 MUST 为 HTTP 413

### Requirement: 内置工具

`mcp-demo` MUST 至少提供两个工具，名称 MUST 满足 `^[a-z][a-z0-9_]{0,63}$`。`formula_hint(topic: string 必填, level?: string)` MUST 基于内置的数学与物理常见主题表，给出与主题相关的公式与思路提示，只给提示、不给具体题目的答案；主题不在表中时 MUST 返回文本「暂无该主题的提示」（不是错误）。`unit_convert(value: number 必填, from: string 必填, to: string 必填)` MUST 支持长度、质量、时间的常用单位换算；单位不支持或跨量纲换算时 MUST 返回 `isError: true` 与可读文本。

#### Scenario: 公式提示只给思路

- **WHEN** 调用 `formula_hint`，`topic` 为「勾股定理」
- **THEN** 返回文本 MUST 含该定理的公式与使用思路

#### Scenario: 未知主题

- **WHEN** 调用 `formula_hint`，`topic` 为「量子色动力学」
- **THEN** 返回文本 MUST 为「暂无该主题的提示」，`isError` MUST 不为 `true`

#### Scenario: 单位换算

- **WHEN** 调用 `unit_convert`，参数为 `{"value":1.5,"from":"km","to":"m"}`，再调用参数为 `{"value":1,"from":"kg","to":"s"}`
- **THEN** 第一次 MUST 返回含 `1500` 的文本
- **AND** 第二次 MUST 返回 `isError: true` 与可读的不支持原因

### Requirement: 工具服务器登记与管理

所有 `/api/mcp/servers` 接口 MUST 仅限教师（学生 403，在读取请求体之前返回），班级 MUST 取自会话，他班服务器的编号 MUST 返回与"不存在"一致的 404。`GET /api/mcp/servers` MUST 列出本班服务器，每项含 `id`、`name`、`base_url`、`has_token`（布尔值）、`status`（`disconnected` / `connected` / `error`）、`tool_count`、`last_error`、`tools_refreshed_at`。`POST /api/mcp/servers {name, base_url, token}` 成功返回 201：`name` MUST 满足 `^[a-z][a-z0-9-]{0,31}$` 且同班唯一（重名返回 409）；`base_url` MUST 是 `http` 或 `https`、不含用户名或口令、不超过 200 个字符、不以 `/mcp` 结尾（主机自行拼接 `/mcp`）；`token` 长度 MUST 为 1–512；其余校验失败返回 400 且错误信息可读。令牌只写不读：任何接口的响应（含错误信息）与服务端日志 MUST NOT 含令牌原文。`DELETE /api/mcp/servers/{id}` 在仍被本班技能引用时 MUST 返回 409 并列出引用它的技能名，否则返回 204。

#### Scenario: 登记与列表

- **WHEN** teacher_a 以合法的名称、地址与令牌登记服务器，再调用列表
- **THEN** 登记 MUST 返回 201；列表中该项 MUST 含 `has_token: true`、`status: "disconnected"`、`tool_count: 0`，且 MUST NOT 含令牌字段或令牌原文

#### Scenario: 登记校验

- **WHEN** teacher_a 分别以名称 `Campus`、`1tools`、33 个字符的名称，地址 `ftp://x`、`http://u:p@x`、以 `/mcp` 结尾的地址、201 个字符的地址，空令牌、513 个字符的令牌登记
- **THEN** 每次 MUST 返回 400 且错误信息指出出错字段
- **AND** 以本班已存在的名称登记 MUST 返回 409

#### Scenario: 被引用时不能删除

- **WHEN** 某技能的「工具」引用了服务器 `campus-tools` 的工具，teacher_a 删除该服务器
- **THEN** 响应 MUST 为 409，并列出该技能的名称；解除引用后再删除 MUST 返回 204

#### Scenario: 学生不能管理服务器

- **WHEN** student_a1 调用任一 `/api/mcp/servers` 接口
- **THEN** 响应 MUST 为 403，数据库 MUST 无变化

### Requirement: 连接与工具发现

`POST /api/mcp/servers/{id}/connect` MUST 依次向 `<base_url>/mcp` 发送 `initialize`、`notifications/initialized`、`tools/list`（携带该服务器的令牌）。成功时 MUST 以本次结果整体替换该服务器的工具缓存（不做增量合并）：名称不满足 `^[a-z][a-z0-9_]{0,63}$` 的工具 MUST 丢弃并记录日志，`description` MUST 截断到 1024 个字符，`inputSchema` 超过 8KB 的工具 MUST 丢弃；随后把状态置为 `connected`、刷新 `tools_refreshed_at`，返回 200 与工具清单。连接失败（网络不通、超时、HTTP 401、协议错误或响应不是 `application/json`）MUST 返回 502 与可读提示（如「令牌被拒绝」「无法连接到服务器」），把状态置为 `error`、记录 `last_error`（不含令牌）并清空工具缓存。`GET /api/mcp/servers/{id}/tools` MUST 返回缓存的工具清单（`name`、`description`、`input_schema`），未连接时返回空数组。`POST /api/mcp/servers/{id}/disconnect` MUST 清空缓存并把状态置为 `disconnected`。连接超时为 5 秒，单次 `tools/call` 超时为 10 秒，远端响应体超过 256KB 视为失败。任何连接失败 MUST NOT 影响检索、问答与流式对话。

#### Scenario: 连接成功并替换缓存

- **WHEN** teacher_a 连接 `campus-tools` 两次，第二次之前 `mcp-demo` 的工具列表发生变化
- **THEN** 两次 MUST 返回 200；第二次之后工具缓存 MUST 恰为第二次 `tools/list` 的结果（经过滤与截断），不保留第一次独有的工具

#### Scenario: 发现结果的过滤

- **WHEN** 被连接的服务器返回名为 `Bad-Name` 的工具、`description` 为 2000 字的工具、`inputSchema` 为 9KB 的工具
- **THEN** 第一个与第三个 MUST 被丢弃，第二个的 `description` MUST 被截断为 1024 个字符

#### Scenario: 令牌错误与网络不通

- **WHEN** 以错误令牌登记的服务器被连接，或连接一个不存在的地址
- **THEN** 响应 MUST 为 502，提示分别为「令牌被拒绝」与「无法连接到服务器」之类的可读文字
- **AND** 该服务器 `status` MUST 为 `error`，工具缓存 MUST 为空，响应与日志 MUST NOT 含令牌原文
- **AND** 同时进行的检索与助手会话 MUST 正常工作

#### Scenario: 断开清空缓存

- **WHEN** teacher_a 断开已连接的服务器，再请求其工具清单
- **THEN** `status` MUST 为 `disconnected`，工具清单 MUST 为空数组

### Requirement: 预置工具服务器

api 启动时，若配置了 `MCP_AUTH_TOKEN`，MUST 为 A 班幂等登记一台名为 `campus-tools` 的服务器：`base_url` 为 `http://mcp-demo:8090`，令牌取自 `MCP_AUTH_TOKEN`，初始状态为 `disconnected`。幂等性 MUST 以预置键判断，重复启动 MUST NOT 产生重复记录，也 MUST NOT 覆盖教师已做的修改。未配置 `MCP_AUTH_TOKEN` 时 MUST 不预置，api 照常启动。

#### Scenario: 预置并可直接连接

- **WHEN** 配置了 `MCP_AUTH_TOKEN` 的全新环境启动后，teacher_a 连接 `campus-tools`
- **THEN** 连接 MUST 返回 200 且列出 `formula_hint` 与 `unit_convert`（主机与 `mcp-demo` 使用同一个令牌）

#### Scenario: 重复启动幂等

- **WHEN** api 连续重启两次
- **THEN** A 班名为 `campus-tools` 的服务器 MUST 只有一条

### Requirement: 技能对 MCP 工具的引用校验

技能「工具」字段中形如 `工具名@服务器名` 的值是对 MCP 工具的引用。创建、修改（含通过修改启用）技能时，系统 MUST 对每个引用逐项校验：① 格式合法；② 服务器属于本班（他班服务器 MUST 与不存在的服务器报同样的错，不泄露存在性）；③ 服务器状态为 `connected`；④ 工具在该服务器的发现缓存中；⑤ 映射后的函数名 `工具名__服务器名` 不超过 64 个字符。任一项不满足 MUST 返回 400，错误信息 MUST 指出出错的字段与具体值。保存之后服务器才断开的情况由运行时兜底（见「工具调用循环」），不追溯修改已保存的技能。

#### Scenario: 引用未连接或不存在的工具

- **WHEN** teacher_a 保存「工具：formula_hint@campus-tools」的技能，而 `campus-tools` 处于 `disconnected`；再保存引用 `nope@campus-tools`（服务器已连接）的技能
- **THEN** 两次 MUST 返回 400，错误信息分别指出 `campus-tools` 未连接、`nope@campus-tools` 不在工具清单中

#### Scenario: 引用他班服务器

- **WHEN** B 班教师保存引用 `formula_hint@campus-tools`（A 班服务器）的技能
- **THEN** 响应 MUST 为 400，错误信息 MUST 与引用一个不存在的服务器名时的措辞一致

#### Scenario: 合法引用

- **WHEN** `campus-tools` 已连接，teacher_a 保存「工具：knowledge_search，formula_hint@campus-tools」的技能
- **THEN** 保存 MUST 成功，技能的工具列表 MUST 为 `["knowledge_search", "formula_hint@campus-tools"]`

### Requirement: 工具调用循环

只有本轮路由命中的技能含 MCP 工具引用时，系统 MUST 进入工具阶段；否则 MUST 与没有工具时的行为完全一致，任何发往网关的请求 MUST NOT 含工具定义。

- **候选集**：只取命中技能中列出、且运行时仍可用（服务器 `connected` 且工具在缓存中）的工具；不可用的引用 MUST 不提供给模型，并各记一条 `status: denied`、`reason: not_connected`、`round: 0` 的调用记录。
- **工具阶段**：以非流式方式调用对话网关，使用 OpenAI 兼容的 `tools` 字段把候选工具声明为 function（`name` 为 `工具名__服务器名`，`description` 与 `parameters` 取自发现缓存），`tool_choice` 为 `auto`。候选集为空时 MUST 跳过工具阶段。
- **宿主校验**：模型提出的每次调用 MUST 先校验。工具不在候选集中记 `denied`、`reason: not_allowed`；参数不是合法 JSON 或不满足 `inputSchema` 的 `required` 与基本类型记 `error`、`reason: invalid_args`，并把错误作为工具消息回传给模型；校验通过才携带该服务器的令牌调用远端 `tools/call`。
- **结果处理**：只取 `type` 为 `text` 的内容，去除控制字符，截断到 2000 个字符。`isError: true`、超时、HTTP 失败 MUST 记为 `error`，并以工具消息把失败回传给模型，MUST NOT 静默丢弃。
- **上限**：工具阶段最多 2 轮、累计最多 3 次调用、总时长不超过 20 秒；超出上限的调用请求 MUST 记 `denied`、`reason: limit`，随后进入正文生成，用已有信息作答，不报错。
- **降级**：工具阶段的网关调用失败时 MUST 跳过工具阶段、照常生成正文，并在服务端日志记录原因。
- **最终生成**：仍以流式方式调用网关，请求 MUST NOT 含工具定义；工具结果作为数据拼进本轮用户消息（位置与格式见 `tutor-chat`「消息组装」）。
- 工具阶段 MUST 在写出 `meta` 之前完成；本轮全部调用记录（含 denied）MUST 出现在 `meta.tool_calls` 中并写入审计。

#### Scenario: 不含 MCP 工具的技能保持原样

- **WHEN** 本轮命中的技能只有 `knowledge_search`，或没有命中技能
- **THEN** 网关只被流式调用一次，请求 MUST NOT 含 `tools`，`meta` MUST NOT 含 `tool_calls`

#### Scenario: 正常调用一次工具

- **WHEN** 命中含 `formula_hint@campus-tools` 的技能，模型在工具阶段调用 `formula_hint__campus-tools`
- **THEN** 宿主 MUST 带 `campus-tools` 的令牌调用 `mcp-demo` 的 `tools/call`
- **AND** `meta.tool_calls` MUST 含 `{tool:"formula_hint@campus-tools", status:"ok", round:1, duration_ms}`
- **AND** 最终生成请求 MUST NOT 含工具定义，本轮用户消息中 MUST 含以「〔工具结果 formula_hint@campus-tools〕」开头的工具结果

#### Scenario: 模型调用候选集外的工具

- **WHEN** 模型在工具阶段请求调用未在候选集中声明的 `unit_convert__campus-tools`
- **THEN** 该调用 MUST 不发往远端，记 `denied`、`reason: not_allowed`

#### Scenario: 参数不合法

- **WHEN** 模型对 `formula_hint__campus-tools` 传入不是 JSON 的参数，或缺少必填的 `topic`
- **THEN** 该调用 MUST 不发往远端，记 `error`、`reason: invalid_args`，并把错误作为工具消息回传给模型

#### Scenario: 超过上限

- **WHEN** 模型在第 1 轮请求 4 次调用
- **THEN** 前 3 次按规则处理，第 4 次 MUST 记 `denied`、`reason: limit`，之后进入正文生成且回答正常完成

#### Scenario: 工具失败回传

- **WHEN** 调用 `unit_convert` 得到 `isError: true`，或远端超时
- **THEN** 记录 MUST 为 `error`，失败信息 MUST 作为工具消息回传给模型，回答 MUST 正常完成

#### Scenario: 工具阶段网关失败时降级

- **WHEN** 工具阶段的非流式网关调用失败
- **THEN** 系统 MUST 跳过工具阶段，照常流式生成回答，服务端日志 MUST 记录原因

### Requirement: 调用审计

每一条进入 `meta.tool_calls` 的调用记录 MUST 写入 `mcp_tool_calls`，字段包括：工具名、服务器名、服务器编号、状态（`ok` / `error` / `denied`）、原因、耗时毫秒数、触发用户、班级、会话编号、轮次与时间。审计 MUST NOT 存储调用参数、结果原文或令牌。`GET /api/mcp/calls?limit=50` MUST 仅限教师（学生 403），只返回本班记录，按时间倒序；`limit` 缺省 50、取值 1–200，越界返回 400。每条 MUST 含 `tool@server`、状态、原因、用户名、会话编号、轮次与时间。

#### Scenario: 调用后可在审计中看到

- **WHEN** student_a1 的一次提问触发了 `formula_hint@campus-tools`，teacher_a 随后调用 `GET /api/mcp/calls`
- **THEN** 列表第一条 MUST 为该次调用，含 `formula_hint@campus-tools`、`ok`、student_a1 的用户名、该会话编号与轮次
- **AND** 数据库 `mcp_tool_calls` 中该行 MUST NOT 含参数或结果原文

#### Scenario: 审计接口校验

- **WHEN** student_a1 调用 `GET /api/mcp/calls`，teacher_a 以 `limit=0` 与 `limit=201` 调用
- **THEN** 前者 MUST 为 403，后两者 MUST 为 400

### Requirement: 逐班授权与令牌保护

工具服务器、工具缓存与调用审计 MUST 以会话班级为边界：他班对象 MUST 表现为不存在，审计 MUST 只返回本班记录；已登记服务器的令牌 MUST 永不回传；服务器断开后，其工具在保存与调用两条路径上 MUST 立即不可用。以下四项是同伴交叉验证的必验场景。

#### Scenario: ① 服务器隔离

- **WHEN** B 班教师对 A 班服务器的编号调用 `connect`、`tools`、`disconnect` 与 `DELETE`，并保存引用 `formula_hint@campus-tools` 的技能
- **THEN** 前四者 MUST 都返回与不存在的编号一致的 404，A 班服务器 MUST 无任何变化
- **AND** 保存技能 MUST 返回 400

#### Scenario: ② 令牌不回传

- **WHEN** 对列表、登记、连接（含令牌错误导致的失败）、工具清单接口的响应体，以及 api 与 `mcp-demo` 的日志检索令牌原文
- **THEN** 均 MUST 检索不到

#### Scenario: ③ 断开即失效

- **WHEN** teacher_a 断开 `campus-tools` 后，先保存一个引用 `formula_hint@campus-tools` 的新技能，再让已绑定该工具的技能在对话中被命中
- **THEN** 保存 MUST 返回 400
- **AND** 对话的 `meta.tool_calls` 中该工具 MUST 为 `denied`、`reason: not_connected`，审计中 MUST 有对应的 denied 记录，回答 MUST 正常完成

#### Scenario: ④ 审计只看本班

- **WHEN** A 班产生调用记录后，B 班教师调用 `GET /api/mcp/calls`
- **THEN** 结果 MUST NOT 含任何 A 班记录

### Requirement: 工具服务器管理页

教师 MUST 能在「工具服务器」页查看本班服务器列表（名称、地址、是否已配令牌、状态、工具数、最近错误），登记、连接、断开、删除服务器，并查看每台服务器的工具（名称、说明、参数结构），以便在绑定前审阅工具说明、防范工具投毒；页面 MUST 同时展示本班调用审计表。学生 MUST 看不到该页面入口，直接访问地址时 MUST 回到材料页，服务端仍 MUST 对学生的请求返回 403。

#### Scenario: 教师管理服务器

- **WHEN** teacher_a 在页面上登记一台服务器、连接、查看工具、断开、删除
- **THEN** 每一步后列表 MUST 反映最新状态；查看工具时 MUST 显示名称、说明与参数结构；连接失败时 MUST 显示服务端返回的可读提示
- **AND** 页面任何位置 MUST NOT 显示令牌原文

#### Scenario: 学生无入口

- **WHEN** student_a1 登录后查看导航并直接访问工具服务器页地址
- **THEN** 导航中 MUST 没有该入口，访问地址时 MUST 回到材料页

### Requirement: 助手页的工具调用展示

助手页 MUST 在回答上方以小标签展示本轮 `meta.tool_calls` 中的每一条记录（工具引用与状态）；`denied` 与 `error` MUST 以醒目样式区分。本轮没有工具调用时 MUST 不显示标签。重新打开历史会话时 MAY 不显示历史轮次的工具标签。

#### Scenario: 展示工具调用标签

- **WHEN** 一次提问的 `meta.tool_calls` 含一条 `ok` 与一条 `denied`
- **THEN** 回答上方 MUST 出现两个标签，分别显示 `formula_hint@campus-tools` 与对应状态，`denied` 的标签 MUST 使用醒目样式

#### Scenario: 无工具调用不显示

- **WHEN** 一次提问的 `meta` 不含 `tool_calls`
- **THEN** 回答上方 MUST NOT 出现工具标签
