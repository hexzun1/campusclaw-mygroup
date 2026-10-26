## Purpose

在仓库内提供按 URL 组织、可一键运行并生成 HTML 报告的接口测试 `apitest/`：用例不仅检查状态码，还核对响应内容并与数据库逐项比对，覆盖登录、检索、提示词与技能、流式会话、MCP 管理与端到端调用，作为每次迭代验收的可重复证据。

## ADDED Requirements

### Requirement: 测试目录与用例组织

仓库 MUST 包含 `apitest/` 目录，使用 Python 3、`requests`、`pytest`、`pytest-html`、`pymysql`，依赖写在 `apitest/requirements.txt`。接口用例 MUST 按 URL 组织：`apitest/cases/` 下的目录层级与接口路径一致，文件名为大写的 HTTP 方法，路径参数写成 `_id_`，例如 `cases/api/login/POST.py` 对应 `POST /api/login`、`cases/api/conversations/_id_/stream/POST.py` 对应 `POST /api/conversations/{id}/stream`；跨接口的流程用例放在 `cases/e2e/`。登录取 token、数据库连接、SSE 解析、测试数据前缀与清理等公共能力 MUST 放在 `apitest/lib/` 与 `conftest.py`。pytest 配置 MUST 能收集这些以方法命名的文件，且同名文件位于不同目录时 MUST NOT 互相冲突。

#### Scenario: 用例与接口一一对应

- **WHEN** 列出 `apitest/cases/api/` 下的文件
- **THEN** MUST 至少存在 `login/POST.py`、`me/GET.py`、`search/POST.py`、`skills/POST.py`、`agents/POST.py`、`conversations/POST.py`、`conversations/_id_/stream/POST.py`、`mcp/servers/POST.py`、`mcp/servers/GET.py`、`mcp/servers/_id_/connect/POST.py`、`mcp/servers/_id_/tools/GET.py`、`mcp/servers/_id_/disconnect/POST.py`、`mcp/calls/GET.py`，以及 `cases/e2e/test_mcp_flow.py`

#### Scenario: 全部用例被收集

- **WHEN** 在 `apitest/` 下执行 `pytest --collect-only -q`
- **THEN** 上述每个文件中的测试函数 MUST 都被收集，且 MUST NOT 出现模块名冲突错误

### Requirement: 测试配置与机密

测试的全部配置 MUST 来自环境变量（可由 `apitest/.env` 加载）：`API_BASE_URL`（缺省 `http://localhost:8080`）、`APITEST_DB_HOST`、`APITEST_DB_PORT`、`APITEST_DB_USER`、`APITEST_DB_PASSWORD`、`APITEST_DB_NAME`、`MCP_AUTH_TOKEN`，以及预置账号的口令 `APITEST_TEACHER_A_PASSWORD`、`APITEST_STUDENT_A1_PASSWORD`、`APITEST_STUDENT_B1_PASSWORD`。仓库 MUST 提供只含变量名、不含真实值的 `apitest/.env.example`；代码中 MUST NOT 硬编码任何口令、令牌或密钥。开发编排中的 MySQL MUST 只绑定 `127.0.0.1:3307`，供测试读库。缺少必需变量时，测试 MUST 在开始前给出列出变量名的可读错误并以失败退出。

#### Scenario: 没有硬编码机密

- **WHEN** 在 `apitest/` 中检索本机 `.env` 里的各口令与令牌的值
- **THEN** MUST 检索不到；`apitest/.env.example` 中各变量 MUST 无值

#### Scenario: 缺少配置时可读失败

- **WHEN** 不设置 `APITEST_DB_PASSWORD` 运行 `python run_all.py`
- **THEN** 进程 MUST 以非零状态退出，输出 MUST 指出缺少 `APITEST_DB_PASSWORD`

#### Scenario: 测试库端口只绑本机

- **WHEN** 开发编排启动后检查 db 服务的端口映射
- **THEN** MUST 为 `127.0.0.1:3307`，而不是 `0.0.0.0`

### Requirement: 断言口径

每个用例 MUST 同时断言状态码与响应内容；凡是写入或读取持久数据的接口，用例 MUST 用数据库查询核对响应中的关键字段与库中数据一致（例如编号、名称、角色、班级、正文、状态），而不能只看状态码。涉及令牌的接口，用例 MUST 断言响应中不出现令牌原文。

#### Scenario: 响应与数据库一致

- **WHEN** 任一创建类用例（如创建技能）通过
- **THEN** 该用例 MUST 已从数据库读出新记录，并断言其字段与提交内容和响应一致

### Requirement: 登录与身份用例

`apitest` MUST 覆盖：正确口令登录返回 200 且含 `token`；口令错误返回 401；缺少字段返回 400；用 token 调用 `GET /api/me` 得到的用户名、角色、班级与数据库 `users`、`classes` 一致。

#### Scenario: 登录用例集

- **WHEN** 运行 `cases/api/login/POST.py` 与 `cases/api/me/GET.py`
- **THEN** 上述四类断言 MUST 全部执行并通过

### Requirement: 检索用例

`apitest` MUST 覆盖：`keyword`、`vector`、`hybrid` 三种模式均返回 200，结果中的材料编号与切片序号与数据库 `knowledge_chunks` 一致；无 token 返回 401；B 班用户检索 A 班独有内容返回 200 且 `hits` 为空。

#### Scenario: 检索用例集

- **WHEN** 运行 `cases/api/search/POST.py`
- **THEN** 三种模式的结果 MUST 与数据库逐条核对一致，401 与跨班空结果 MUST 通过

### Requirement: 提示词与技能用例

`apitest` MUST 覆盖：教师创建含提示词的智能体与合法技能，成功后数据库中的字段与提交原文和响应一致；非法技能文件返回 400；学生创建智能体或技能返回 403；引用未连接服务器工具的技能保存返回 400。

#### Scenario: 智能体与技能用例集

- **WHEN** 运行 `cases/api/agents/POST.py` 与 `cases/api/skills/POST.py`
- **THEN** 上述断言 MUST 全部通过，其中技能的 `source_text`、名称、适用条件、工具列表 MUST 与数据库一致

### Requirement: 会话与流式消息用例

`apitest` MUST 覆盖：创建会话后流式发送一条消息，解析 SSE，断言事件顺序为 `meta` → 至少一个 `delta` → `done`；全部 `delta` 拼接等于数据库中 `done.message_id` 对应消息的正文；会话详情接口的返回与数据库中的会话与消息一致。

#### Scenario: 流式消息用例

- **WHEN** 运行 `cases/api/conversations/POST.py` 与 `cases/api/conversations/_id_/stream/POST.py`
- **THEN** 事件顺序、拼接一致性与详情一致性 MUST 全部通过

### Requirement: MCP 管理用例

`apitest` MUST 覆盖：教师登记、列出、连接、列出工具、断开服务器，每一步与数据库 `mcp_servers`、`mcp_tools` 核对；学生调用每个 MCP 管理接口与审计接口都返回 403；B 班教师访问 A 班服务器返回 404；所有响应中都不出现令牌原文。B 班教师账号由测试在运行时临时创建并在结束后删除。

#### Scenario: MCP 管理用例集

- **WHEN** 运行 `cases/api/mcp/` 下的全部用例
- **THEN** 上述断言 MUST 全部通过，且每个响应体中检索不到所用令牌

### Requirement: MCP 端到端与断开即失效用例

`cases/e2e/test_mcp_flow.py` MUST 覆盖完整流程：使用预置（或新登记）的 `campus-tools` → 连接 → 创建「工具：formula_hint@campus-tools」的技能并绑定到本班一个智能体 → 创建会话并提出能命中该技能、需要公式提示的问题 → 断言 `meta.tool_calls` 含 `formula_hint@campus-tools` → 断言 `GET /api/mcp/calls` 与数据库 `mcp_tool_calls` 中出现该会话 `status=ok` 的记录。若本次模型没有调用工具，用例 MUST 判为失败并输出 `meta` 便于排查。随后断开服务器再触发同一技能，MUST 断言 `meta.tool_calls` 中该工具为 `denied`，且审计中有对应的 denied 记录。

#### Scenario: 端到端通过

- **WHEN** 在网关可用的环境运行 `cases/e2e/test_mcp_flow.py`
- **THEN** 工具调用、审计接口与数据库三处的断言 MUST 全部通过

#### Scenario: 模型未调用工具时失败

- **WHEN** 本次提问的 `meta` 中没有 `tool_calls`
- **THEN** 用例 MUST 失败，失败信息 MUST 含该次 `meta` 的完整内容

### Requirement: 依赖网关的用例

依赖对话网关产出回答的用例 MUST 标记为 `gateway`。若检测到网关不可用（流式接口在事件流开始前返回 503），这些用例 MUST 以 skip 结束并写明原因，MUST NOT 计为通过；其它用例不受影响。

#### Scenario: 网关不可用时跳过

- **WHEN** 网关不可用时运行 `run_all.py`
- **THEN** 报告中 `gateway` 用例 MUST 显示为 skipped 且附原因，其余用例照常执行并计入结果

### Requirement: 测试数据隔离

测试创建的数据 MUST 统一使用可识别的前缀：智能体名、技能名、临时账号名等使用 `apitest_<时间戳>`；MCP 服务器名受 `^[a-z][a-z0-9-]{0,31}$` 约束（连字符保证 `工具名__服务器名` 可无歧义拆分），因此使用 `apitest-<时间戳>`。用例结束后 MUST 清理自己创建的数据；MUST NOT 修改或删除预置数据（预置账号、班级、材料、预置技能、默认智能体、`campus-tools` 的配置）。若某用例需要临时改变预置对象的状态（如连接或断开 `campus-tools`），MUST 在结束时恢复。

#### Scenario: 运行后无残留

- **WHEN** 完整运行一次 `run_all.py` 后查询数据库
- **THEN** 名称以 `apitest_` 或 `apitest-` 开头的智能体、技能、服务器与账号 MUST 不存在，预置数据的行数与运行前一致

### Requirement: 一键运行与报告

`apitest/run_all.py` MUST 一键调用 pytest 运行全部用例，生成单文件 HTML 报告 `apitest/reports/report-<时间戳>.html`，进程退出码 MUST 反映测试结果（全部通过为 0，否则非零）。`.gitignore` MUST 包含 `apitest/reports/`、`apitest/.env`、`__pycache__/`、`.pytest_cache/`，报告 MUST NOT 提交到 Git。README MUST 有一节说明如何安装依赖、配置环境变量、运行 `run_all.py` 与查看报告。

#### Scenario: 一键运行出报告

- **WHEN** 在开发编排运行的前提下执行 `python apitest/run_all.py`
- **THEN** MUST 在 `apitest/reports/` 下生成一个可单独打开的 HTML 报告，退出码与报告中的通过 / 失败情况一致

#### Scenario: 报告不入库

- **WHEN** 运行后执行 `git status --porcelain`
- **THEN** 输出 MUST NOT 含 `apitest/reports/` 下的文件
