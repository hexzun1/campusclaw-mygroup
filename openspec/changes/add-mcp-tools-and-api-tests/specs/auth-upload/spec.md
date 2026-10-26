## MODIFIED Requirements

### Requirement: 口令与密钥安全

口令 MUST 以 bcrypt 哈希存储；JWT 签名密钥（`JWT_SECRET`）、数据库凭据与预置账号口令 MUST 只来自环境变量，缺失时服务 MUST 启动失败；`JWT_SECRET` 少于 32 个字符 MUST 视为不合格并使服务启动失败；仓库 MUST NOT 包含真实密钥。MCP 令牌 `MCP_AUTH_TOKEN` MUST 只来自本机 `.env`（经环境变量注入）：`mcp-demo` 在其为空时 MUST 拒绝启动；api 在其为空时 MUST 照常启动，只是不预置工具服务器；`.env.example` MUST 只含 `MCP_AUTH_TOKEN=` 而不含值。教师登记的工具服务器令牌 MUST 只保存在服务端数据库中，只写不读：任何接口响应（含错误信息）、前端页面、审计记录与服务端日志 MUST NOT 含其原文。

#### Scenario: 库中无明文口令

- **WHEN** 查询 users 表的口令字段
- **THEN** 每个值 MUST 以 bcrypt 前缀（如 `$2a$` 或 `$2b$`）开头
- **AND** MUST NOT 等于任何预置账号的明文口令

#### Scenario: 缺少必需配置时启动失败

- **WHEN** 未提供 `JWT_SECRET` 或数据库口令等必需环境变量就启动 api
- **THEN** api MUST 启动失败，并输出缺失的变量名
- **AND** MUST NOT 使用内置默认值继续运行

#### Scenario: JWT_SECRET 过短时启动失败

- **WHEN** 提供的 `JWT_SECRET` 少于 32 个字符就启动 api
- **THEN** api MUST 启动失败，并输出 `JWT_SECRET` 不合格的原因
- **AND** 输出 MUST NOT 含该密钥的值

#### Scenario: token 载荷不含口令信息

- **WHEN** 解码任一登录返回的 token 载荷
- **THEN** 载荷 MUST NOT 含口令、口令哈希或任何密钥

#### Scenario: 仓库不含真实密钥

- **WHEN** 检查已提交到 Git 的文件
- **THEN** MUST 存在 `.env.example` 且只含变量名与占位值
- **AND** MUST NOT 存在 `.env` 文件或任何真实口令、密钥

#### Scenario: MCP 令牌只来自环境变量

- **WHEN** 检查 `.env.example`、`mcp-server/` 源码与 Dockerfile、`docker-compose.yml` 以及构建出的镜像历史
- **THEN** `.env.example` MUST 含 `MCP_AUTH_TOKEN=` 且无值，其余位置 MUST NOT 出现本机 `MCP_AUTH_TOKEN` 的值
- **AND** `MCP_AUTH_TOKEN` 为空时 `mcp-demo` MUST 拒绝启动，而 api MUST 正常启动且不预置工具服务器

#### Scenario: 已登记服务器的令牌不回传

- **WHEN** 教师登记、列出、连接（含失败）工具服务器并查看工具与审计
- **THEN** 所有响应体、页面内容与服务端日志 MUST NOT 含该令牌原文，列表只以 `has_token` 表示是否已配置

### Requirement: Compose 部署与健康检查

系统 MUST 以 Docker Compose 启动 web、api、db、qdrant、mcp-demo 五个服务；只有 web MUST 映射到宿主的对外端口；`mcp-demo` 为便于自查 MUST 只映射到宿主的 `127.0.0.1:8090`，MUST NOT 绑定到其它网卡；db、qdrant、api MUST NOT 映射宿主端口。数据库、向量库数据与上传文件 MUST 持久化；api 与 `mcp-demo` 的 `GET /health` MUST 无需登录且只表示进程存活。

#### Scenario: 按 README 从零启动

- **WHEN** 第三方克隆仓库，按 README 复制 `.env.example` 为 `.env` 并填值（含 `MCP_AUTH_TOKEN`），执行 `docker compose up --build -d`
- **THEN** 浏览器访问 `http://localhost:8080` MUST 显示登录页
- **AND** `GET /health` MUST 返回 200 与 `{"status":"ok"}`，且不要求登录
- **AND** `curl http://127.0.0.1:8090/health` MUST 返回 200 与 `{"status":"ok"}`

#### Scenario: 只暴露 web 端口

- **WHEN** 服务全部启动后在宿主机检查端口
- **THEN** 对外只有 web 的端口 MUST 可访问；`mcp-demo` 的 8090 MUST 只监听在 `127.0.0.1`
- **AND** 数据库端口（3306）、向量库端口（6333、6334）与 api 端口 MUST NOT 映射到宿主机

#### Scenario: 重建后数据仍在

- **WHEN** teacher_a 上传一条材料并完成索引后执行 `docker compose down`，再执行 `docker compose up -d`（不删除数据卷）
- **THEN** 预置账号 MUST 仍可登录
- **AND** 该材料 MUST 仍出现在本班列表中且可下载
- **AND** 以 `vector` 模式检索该材料的内容 MUST 仍能命中

#### Scenario: 数据库不可用时返回 503

- **WHEN** db 服务停止后，已登录用户调用 `GET /api/materials`
- **THEN** 响应 MUST 为 503
- **AND** MUST NOT 返回 401 或清除用户的会话 Cookie

#### Scenario: mcp-demo 只绑定本机回环

- **WHEN** 执行 `docker compose port mcp-demo 8090` 并在宿主机检查监听地址
- **THEN** 映射 MUST 为 `127.0.0.1:8090`，宿主机上 MUST NOT 存在 `0.0.0.0:8090` 或 `[::]:8090` 的监听
