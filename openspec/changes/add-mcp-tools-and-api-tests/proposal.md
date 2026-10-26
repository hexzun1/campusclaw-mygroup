## Why

第 5 课的助教会话只会检索本班材料，技能里的「工具」字段只能写 `knowledge_search`，而且只做校验和展示。第 6 课要求让技能接入外部能力（如公式提示、单位换算），并且全程可控：工具由教师登记和审阅，调用经宿主校验和逐班授权，每次调用都留审计。同时，第 5 课课程清单还有两项没交付：清单要求提供 `openapi.json`，而现在只有 Swagger 2.0 的 `/swagger/doc.json`；按 URL 组织、能核对数据库、能一键出报告的 `apitest/` 接口测试也还没有。本变更一并补齐。

## What Changes

**A. MCP 工具接入（新增能力 `mcp-tools`）**

- **自建 MCP 服务器 `mcp-server/`**：放在本仓库。用 Go 标准库手写 JSON-RPC 2.0，采用 Streamable HTTP，每个请求只返回一个 JSON 响应。
  - 部署：独立 Dockerfile，作为 Compose 服务 `mcp-demo` 监听 8090，宿主机只绑定 `127.0.0.1:8090`。
  - 鉴权：Bearer 令牌取自 `MCP_AUTH_TOKEN`，用常量时间比较；令牌为空时拒绝启动。
  - 方法：`initialize`、`notifications/initialized`、`tools/list`、`tools/call`；返回标准错误码。
  - 工具：提供 `formula_hint`（只给公式与思路提示，不给答案）和 `unit_convert` 两个。
- **主机侧登记、连接与发现**：新增迁移 `004_mcp.sql`，建 `mcp_servers`、`mcp_tools`（发现缓存）、`mcp_tool_calls`（审计）三张表。
  - 接口：教师专用的 `/api/mcp/servers` 系列，覆盖登记、列表、删除、连接、断开和查看工具。
  - 令牌只写不读，任何响应和日志都不出现令牌原文。
  - 连接失败返回 502，并把服务器标记为 error，不影响主链路。
  - 启动时若配置了 `MCP_AUTH_TOKEN`，为 A 班幂等预置一台 `campus-tools`。
- **技能绑定**：技能的「工具」字段可写 1–3 个值，每个值是 `knowledge_search` 或 `工具名@服务器名`。保存时从严校验四项：格式、是否本班、是否已连接、工具是否在缓存中。运行时如发现已断开，记为 denied 兜底。
- **调用循环**：只有本轮命中的技能含 MCP 工具时，才进入工具阶段，否则与第 5 课行为完全一致。
  - 工具阶段：以非流式方式调用网关并附带 `tools`；模型发起的每次调用都经宿主校验，再携带该服务器的令牌调用远端。
  - 结果处理：只取文本、做清洗和截断；最多 2 轮、3 次调用、总计 20 秒。
  - 最终生成：仍走原来的流式正文生成，不带工具定义；工具结果作为数据拼进本轮用户消息。
  - 流式协议：`meta` 增加可选字段 `tool_calls`（不含参数和结果原文）。
  - 降级：工具阶段失败时跳过工具，照常作答。
- **审计**：每次调用（ok / error / denied）都落 `mcp_tool_calls`，不存参数、结果和令牌。教师可通过 `GET /api/mcp/calls` 查看本班记录。
- **前端**：
  - 新增教师专用的「工具服务器」页，可登记、连接、断开、删除服务器，审阅工具说明与参数结构，并查看本班审计表。
  - 助手页在回答上方用小标签展示本轮工具调用，denied 和 error 醒目标出。

**B. 接口文档补齐 `/openapi.json`（MODIFIED `tutor-chat`「接口文档」）**

- 新增 `GET /openapi.json`，返回 OpenAPI 3.0.x 文档：启动时用 `kin-openapi` 的 `openapi2conv` 由 swag 生成的 Swagger 2.0 转换而来，不手写第二份。
- Swagger 页面改为加载 `/openapi.json`；`/swagger/doc.json` 保留。
- 文档补齐 MCP 管理接口与审计接口。

**C. 接口测试 `apitest/`（新增能力 `api-testing`）**

- 技术栈：Python 3 + requests + pytest + pytest-html + pymysql。
- 组织：用例按 URL 组织，文件名用 HTTP 方法，路径参数写成 `_id_`。
- 断言：不只看状态码，还要核对响应内容，并与数据库逐项比对。
- 覆盖：登录、检索、提示词与技能、会话流式消息、MCP 管理、MCP 端到端、断开即失效。
- 运行：`run_all.py` 一键运行，生成单文件 HTML 报告（不入库）。
- 配置：全部来自环境变量，开发编排的 MySQL 改为只绑定 `127.0.0.1:3307`。

**对既有规约的修改（MODIFIED）**

- `tutor-chat`：
  - 「技能文件格式」：「工具」字段支持多个取值。
  - 「消息组装」：不再一律禁止工具定义，改为只在工具阶段携带；最终生成仍不带工具定义；增加工具结果的位置与数据声明。
  - 「流式响应协议」：`meta` 增加可选的 `tool_calls`。
  - 「接口文档」：增加 `/openapi.json` 和 MCP 接口的覆盖要求。
- `auth-upload`：
  - 「Compose 部署与健康检查」：增加 `mcp-demo` 服务，它只绑定本机回环地址。
  - 「口令与密钥安全」：增加 `MCP_AUTH_TOKEN` 与已登记服务器令牌的约束。
- `knowledge-qa-ui`「页面导航与访问控制」：教师导航增加「工具服务器」标签。

**BREAKING（仅开发环境）**：`docker-compose.dev.yml` 的端口有两处调整。

- MySQL 宿主端口由 `127.0.0.1:3306` 改为 `127.0.0.1:3307`。
- 网关桩宿主端口由 `127.0.0.1:8090` 改为 `127.0.0.1:8091`，把 8090 让给 `mcp-demo`。

生产编排对外只暴露 web 的约定不变。

## Capabilities

### New Capabilities

- `mcp-tools`：
  - MCP 服务器：协议、鉴权、错误码和内置工具。
  - 主机侧：服务器登记、连接与发现，以及逐班授权。
  - 技能对 MCP 工具的引用校验。
  - 调用循环：宿主校验、上限与降级。
  - 审计与查询。
  - 页面：工具服务器管理页、助手页的工具调用展示。
- `api-testing`：`apitest/` 的目录组织、配置与机密、断言口径、必须覆盖的场景、网关依赖用例、测试数据隔离、一键运行与报告。

### Modified Capabilities

- `tutor-chat`：「技能文件格式」「消息组装」「流式响应协议」「接口文档」。
- `auth-upload`：「Compose 部署与健康检查」「口令与密钥安全」。
- `knowledge-qa-ui`：「页面导航与访问控制」。原规约写明教师"另外看到两个标签"，新增「工具服务器」标签后必须同步修改，否则规约自相矛盾。

## Impact

- **新目录**：
  - `mcp-server/`：独立 Go 模块，含 Dockerfile。
  - `apitest/`：含 `cases/`、`lib/`、`conftest.py`、`pytest.ini`、`run_all.py`、`requirements.txt`、`.env.example`。
- **后端**（`backend/`）：
  - 新迁移 `004_mcp.sql`。
  - 新包 `internal/mcp`：JSON-RPC 客户端、登记与发现、调用循环、审计。
  - `internal/tutor`：技能「工具」字段解析与保存时校验、工具阶段编排、`meta.tool_calls`、消息组装。
  - `internal/gateway`：带 `tools` 的非流式调用。
  - `cmd/stubgateway`：支持确定性的工具调用。
  - 种子：预置 `campus-tools` 服务器。
  - 路由：新增 `/openapi.json`。
  - swag 注解与文档再生成。
  - 新依赖 `github.com/getkin/kin-openapi`。
- **前端**：工具服务器页、助手页工具调用标签、导航教师标签、Swagger 初始化脚本改为加载 `/openapi.json`。
- **部署**：
  - `docker-compose.yml` 增加 `mcp-demo`，宿主机只绑定 `127.0.0.1:8090`。
  - `docker-compose.dev.yml` 增加 `mcp-demo`，并调整 MySQL 和网关桩的宿主端口（见 BREAKING）。
  - `frontend/nginx.conf` 放行 `/openapi.json`。
- **配置与文档**：
  - `.env.example` 增加 `MCP_AUTH_TOKEN=`（不填值）。
  - `.gitignore` 增加 `apitest/reports/`、`apitest/.env`、`__pycache__/`、`.pytest_cache/`。
  - README 增加 MCP 与接口测试两节。

## Non-goals（非目标）

- 不实现 MCP 的 resources 与 prompts，只实现 tools。
- 不支持 SSE 形式的服务器推送与会话恢复；远端若返回 `text/event-stream`，按协议错误处理。
- 不做 OAuth，MCP 鉴权只用静态 Bearer 令牌。
- 不做指标与链路追踪，本课只做到日志与审计这一层。
- 不允许学生登记、连接或查看工具服务器，也不允许学生查看审计记录。
- 不存储工具调用的参数与结果原文，审计只存元信息。
- 不做多技能同时注入、模型自主选技能、语义路由。这些沿用第 5 课的结论。
- 不加密存储已登记服务器的令牌（理由与风险见 design.md）。
- 不把 apitest 接入 CI，只在本机一键运行。
