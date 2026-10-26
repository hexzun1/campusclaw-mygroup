## MODIFIED Requirements

### Requirement: 工具服务器登记与管理

所有 `/api/mcp/servers` 接口 MUST 仅限教师（学生 403，在读取请求体之前返回），班级 MUST 取自会话，他班服务器的编号 MUST 返回与"不存在"一致的 404。`GET /api/mcp/servers` MUST 列出本班服务器，每项含 `id`、`name`、`base_url`、`has_token`（布尔值）、`status`（`disconnected` / `connected` / `error`）、`tool_count`、`last_error`、`tools_refreshed_at`。`POST /api/mcp/servers {name, base_url, token}` 成功返回 201：`name` MUST 满足 `^[a-z][a-z0-9-]{0,31}$` 且同班唯一（重名返回 409）；`base_url` MUST 是 `http` 或 `https`、不含用户名或口令、不超过 200 个字符、不以 `/mcp` 结尾（主机自行拼接 `/mcp`），且主机部分 MUST NOT 是被禁止的目标：主机名 `localhost`、`db`、`api`、`qdrant`、`web`（不区分大小写），回环地址（`127.0.0.0/8`、`::1`），链路本地地址（`169.254.0.0/16`、`fe80::/10`，含云元数据地址 `169.254.169.254`），未指定地址（`0.0.0.0`、`::`）；`mcp-demo` 与其它私有网段地址 MUST 照常允许；`token` 长度 MUST 为 1–512 字节（按 UTF-8 编码计）；其余校验失败返回 400 且错误信息可读。令牌只写不读：任何接口的响应（含错误信息）与服务端日志 MUST NOT 含令牌原文。`DELETE /api/mcp/servers/{id}` 在仍被本班技能引用时 MUST 返回 409 并列出引用它的技能名，否则返回 204；删除与并发的技能保存 MUST 互斥：不论二者先后，结束后 MUST NOT 出现"服务器已删除而某技能仍引用它"的状态。

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

#### Scenario: 被禁止的地址

- **WHEN** teacher_a 分别以 `http://localhost:8090`、`http://127.0.0.1:8090`、`http://[::1]:8090`、`http://169.254.169.254`、`http://0.0.0.0:8090`、`http://db:3306`、`http://API:8081`、`http://qdrant:6333`、`http://web` 登记
- **THEN** 每次 MUST 返回 400，错误信息 MUST 指出地址不允许
- **AND** 以 `http://mcp-demo:8090` 与 `http://10.0.0.5:8090` 登记 MUST 成功

#### Scenario: 令牌按字节计长度

- **WHEN** teacher_a 以 171 个汉字（513 字节）的令牌登记，再以 170 个汉字（510 字节）的令牌登记
- **THEN** 前者 MUST 返回 400 且错误信息指出令牌长度，后者 MUST 返回 201
- **AND** 两次都 MUST NOT 返回 503

#### Scenario: 删除与保存技能互斥

- **WHEN** 服务器 `campus-tools` 已连接、未被引用，teacher_a 删除它的同时，另一请求保存一个引用 `formula_hint@campus-tools` 的技能
- **THEN** 两个请求结束后，MUST 恰为以下之一：删除返回 204 且保存返回 400；或保存成功且删除返回 409
- **AND** 数据库中 MUST NOT 存在引用已删除服务器的技能

### Requirement: 连接与工具发现

`POST /api/mcp/servers/{id}/connect` MUST 依次向 `<base_url>/mcp` 发送 `initialize`、`notifications/initialized`、`tools/list`（携带该服务器的令牌）。主机发往远端的每个请求 MUST 只声明 `Accept: application/json`。主机 MUST NOT 跟随任何 HTTP 重定向，3xx 响应视为连接失败。主机在连接与调用时 MUST 校验地址解析后的实际 IP：落在回环、链路本地或未指定地址段时 MUST 视为连接失败且不发出请求（防止域名解析到被禁止的目标）。成功时 MUST 以本次结果整体替换该服务器的工具缓存（不做增量合并）：名称不满足 `^[a-z][a-z0-9_]{0,63}$` 的工具 MUST 丢弃并记录日志，同名工具 MUST 只保留第一个并记录日志，`description` MUST 截断到 1024 个字符，`inputSchema` 超过 8KB 的工具 MUST 丢弃；随后把状态置为 `connected`、刷新 `tools_refreshed_at`，返回 200 与工具清单。连接失败（网络不通、超时、重定向、被禁止的目标、HTTP 401、协议错误或响应不是 `application/json`）MUST 返回 502 与可读提示（如「令牌被拒绝」「无法连接到服务器」），把状态置为 `error`、记录 `last_error`（不含令牌）并清空工具缓存；发现成功但写入缓存失败时，同样 MUST 把状态置为 `error` 并记录可读的 `last_error`，响应为 503。超时提示 MUST 区分阶段：握手（`initialize`、`notifications/initialized`、`tools/list`）超时为「连接超时」，`tools/call` 超时为「调用超时」。`GET /api/mcp/servers/{id}/tools` MUST 返回缓存的工具清单（`name`、`description`、`input_schema`），未连接时返回空数组。`POST /api/mcp/servers/{id}/disconnect` MUST 清空缓存并把状态置为 `disconnected`。连接超时为 5 秒，单次 `tools/call` 超时为 10 秒，远端响应体超过 256KB 视为失败。任何连接失败 MUST NOT 影响检索、问答与流式对话。

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

#### Scenario: 重名工具去重

- **WHEN** 被连接的服务器在 `tools/list` 中两次返回名为 `echo` 的工具
- **THEN** 连接 MUST 返回 200，缓存中 `echo` MUST 只有一条且为第一次出现的那条，服务端日志 MUST 记录丢弃

#### Scenario: 不跟随重定向

- **WHEN** 被连接的服务器对 `/mcp` 返回 302，`Location` 指向另一个地址
- **THEN** 连接 MUST 返回 502，主机 MUST NOT 向 `Location` 发出请求，服务器 `status` MUST 为 `error`

#### Scenario: 解析到被禁止的地址

- **WHEN** 已登记服务器的主机名在连接时解析为 `127.0.0.1` 或 `169.254.169.254`
- **THEN** 连接 MUST 返回 502，主机 MUST NOT 向该地址发出请求，服务器 `status` MUST 为 `error`

#### Scenario: 请求只声明 JSON

- **WHEN** 主机向远端发送 `initialize`、`tools/list` 或 `tools/call`
- **THEN** 请求的 `Accept` 头 MUST 为 `application/json`，MUST NOT 含 `text/event-stream`

#### Scenario: 超时提示区分阶段

- **WHEN** 远端在 `initialize` 上超过连接超时未响应；另一次在 `tools/call` 上超过调用超时未响应
- **THEN** 前者的提示 MUST 含「连接超时」；后者回传给模型的工具消息 MUST 含「调用超时」，MUST NOT 含「连接超时」

### Requirement: 技能对 MCP 工具的引用校验

技能「工具」字段中形如 `工具名@服务器名` 的值是对 MCP 工具的引用。创建、修改（含通过修改启用）技能时，系统 MUST 对每个引用逐项校验：① 格式合法；② 服务器属于本班（他班服务器 MUST 与不存在的服务器报同样的错，不泄露存在性）；③ 服务器状态为 `connected`；④ 工具在该服务器的发现缓存中；⑤ 映射后的函数名 `工具名__服务器名` 不超过 64 个字符。任一项不满足 MUST 返回 400，错误信息 MUST 指出出错的字段与具体值。② 与 ③ MUST 在写入技能的同一事务内对被引用的服务器加锁复核，使保存与同时进行的删除、断开互斥；复核不通过时同样返回 400。保存之后服务器才断开的情况由运行时兜底（见「工具调用循环」），不追溯修改已保存的技能。

#### Scenario: 引用未连接或不存在的工具

- **WHEN** teacher_a 保存「工具：formula_hint@campus-tools」的技能，而 `campus-tools` 处于 `disconnected`；再保存引用 `nope@campus-tools`（服务器已连接）的技能
- **THEN** 两次 MUST 返回 400，错误信息分别指出 `campus-tools` 未连接、`nope@campus-tools` 不在工具清单中

#### Scenario: 引用他班服务器

- **WHEN** B 班教师保存引用 `formula_hint@campus-tools`（A 班服务器）的技能
- **THEN** 响应 MUST 为 400，错误信息 MUST 与引用一个不存在的服务器名时的措辞一致

#### Scenario: 合法引用

- **WHEN** `campus-tools` 已连接，teacher_a 保存「工具：knowledge_search，formula_hint@campus-tools」的技能
- **THEN** 保存 MUST 成功，技能的工具列表 MUST 为 `["knowledge_search", "formula_hint@campus-tools"]`

#### Scenario: 保存期间服务器被删除

- **WHEN** 保存引用 `formula_hint@campus-tools` 的技能时，`campus-tools` 在事务外校验通过之后、写入技能之前被删除
- **THEN** 保存 MUST 返回 400，错误信息指出服务器不存在，数据库 MUST NOT 新增或修改该技能

### Requirement: 工具调用循环

只有本轮路由命中的技能含 MCP 工具引用时，系统 MUST 进入工具阶段；否则 MUST 与没有工具时的行为完全一致，任何发往网关的请求 MUST NOT 含工具定义。

- **候选集**：只取命中技能中列出、且运行时仍可用（服务器 `connected` 且工具在缓存中）的工具；不可用的引用 MUST 不提供给模型，并各记一条 `status: denied`、`reason: not_connected`、`round: 0` 的调用记录。
- **工具阶段**：以非流式方式调用对话网关，使用 OpenAI 兼容的 `tools` 字段把候选工具声明为 function（`name` 为 `工具名__服务器名`，`description` 与 `parameters` 取自发现缓存），`tool_choice` 为 `auto`。候选集为空时 MUST 跳过工具阶段。
- **宿主校验**：模型提出的每次调用 MUST 先校验。工具不在候选集中记 `denied`、`reason: not_allowed`；参数不是合法 JSON 或不满足 `inputSchema` 的 `required` 与基本类型记 `error`、`reason: invalid_args`，并把错误作为工具消息回传给模型；校验通过才携带该服务器的令牌调用远端 `tools/call`。
- **结果处理**：只取 `type` 为 `text` 的内容，去除控制字符，并把该服务器令牌原文替换为 `***`，然后截断到 2000 个字符；这一步 MUST 在结果进入工具消息、最终用户消息之前完成。`isError: true`、超时、HTTP 失败 MUST 记为 `error`，并以工具消息把失败回传给模型，MUST NOT 静默丢弃。
- **上限**：
  - 工具阶段最多 2 轮，总时长不超过 20 秒。
  - 「累计最多 3 次调用」只计工具在候选集内的调用请求（含 `invalid_args`），`not_allowed` 不占名额。
  - 单个工具阶段内模型提出的调用请求总数（含 `not_allowed`）最多 10 次。
  - 超出任一上限的调用请求 MUST 记 `denied`、`reason: limit`，随后进入正文生成，用已有信息作答，不报错。
  - 超出 10 次总数上限的请求 MUST 不再逐条记录，只在本轮记一条 `denied`、`reason: limit`。
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

- **WHEN** 模型在第 1 轮请求 4 次调用，且 4 次都是候选集内的工具
- **THEN** 前 3 次按规则处理，第 4 次 MUST 记 `denied`、`reason: limit`，之后进入正文生成且回答正常完成

#### Scenario: 工具失败回传

- **WHEN** 调用 `unit_convert` 得到 `isError: true`，或远端超时
- **THEN** 记录 MUST 为 `error`，失败信息 MUST 作为工具消息回传给模型，回答 MUST 正常完成

#### Scenario: 工具阶段网关失败时降级

- **WHEN** 工具阶段的非流式网关调用失败
- **THEN** 系统 MUST 跳过工具阶段，照常流式生成回答，服务端日志 MUST 记录原因

#### Scenario: 候选集外的调用不占调用名额

- **WHEN** 模型在第 1 轮请求 2 次候选集外的调用和 3 次 `formula_hint__campus-tools`
- **THEN** 2 次候选集外调用 MUST 记 `denied`、`reason: not_allowed`
- **AND** 3 次 `formula_hint__campus-tools` MUST 都按规则发往远端，MUST NOT 被记为 `limit`

#### Scenario: 调用请求总数上限

- **WHEN** 模型在第 1 轮一次提出 25 个候选集外的调用
- **THEN** 前 10 个 MUST 各记一条 `denied`、`reason: not_allowed`，其余 MUST 合并为一条 `denied`、`reason: limit`
- **AND** `meta.tool_calls` 与审计中本轮的记录 MUST 恰为 11 条，回答 MUST 正常完成

#### Scenario: 结果中的令牌被去除

- **WHEN** 远端 `tools/call` 返回的文本中含有该服务器的令牌原文
- **THEN** 回传给模型的工具消息与最终用户消息中的工具结果 MUST NOT 含令牌原文，原位置 MUST 为 `***`

### Requirement: 调用审计

每一条进入 `meta.tool_calls` 的调用记录 MUST 写入 `mcp_tool_calls`，字段包括：工具名、服务器名、服务器编号、状态（`ok` / `error` / `denied`）、原因、耗时毫秒数、触发用户、班级、会话编号、轮次与时间。审计 MUST NOT 存储调用参数、结果原文或令牌。审计写入 MUST NOT 因提问方断开连接而取消：工具阶段中或结束后客户端断开时，已产生的记录 MUST 仍然落库。工具名、服务器名超出存储宽度（64 与 32 个字符）时 MUST 截断后写入；任何一条记录的内容 MUST NOT 导致同一轮其他记录写入失败。`GET /api/mcp/calls?limit=50` MUST 仅限教师（学生 403），只返回本班记录，按时间倒序；`limit` 缺省 50、取值 1–200，越界返回 400。每条 MUST 含 `tool@server`、状态、原因、用户名、会话编号、轮次与时间。

#### Scenario: 调用后可在审计中看到

- **WHEN** student_a1 的一次提问触发了 `formula_hint@campus-tools`，teacher_a 随后调用 `GET /api/mcp/calls`
- **THEN** 列表第一条 MUST 为该次调用，含 `formula_hint@campus-tools`、`ok`、student_a1 的用户名、该会话编号与轮次
- **AND** 数据库 `mcp_tool_calls` 中该行 MUST NOT 含参数或结果原文

#### Scenario: 审计接口校验

- **WHEN** student_a1 调用 `GET /api/mcp/calls`，teacher_a 以 `limit=0` 与 `limit=201` 调用
- **THEN** 前者 MUST 为 403，后两者 MUST 为 400

#### Scenario: 客户端断开后审计仍落库

- **WHEN** student_a1 提问触发 `formula_hint@campus-tools`，并在远端调用完成之后、`meta` 写出之前断开连接
- **THEN** `mcp_tool_calls` 中 MUST 有该次调用的记录，状态为 `ok`

#### Scenario: 超长函数名不影响同轮审计

- **WHEN** 同一轮中模型先正常调用 `formula_hint__campus-tools`，再请求一个 100 个字符的候选集外函数名
- **THEN** 两条记录 MUST 都写入 `mcp_tool_calls`；后者的工具名 MUST 被截断为 64 个字符，状态为 `denied`、`reason: not_allowed`
