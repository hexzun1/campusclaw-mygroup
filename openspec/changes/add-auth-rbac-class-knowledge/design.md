## Context

仓库根已有 README、项目规则文件（AGENTS.md / CLAUDE.md）与本 change 四件套，尚无业务代码。动机见 `proposal.md` 的 Why；可验收行为见 `specs/auth-upload/spec.md`（下文用 R1–R10 指代其中的 Requirement）。第 2 课初稿采用 Flask + SQLite 服务端渲染，第 3 课需求明确为前后端分离的 Go + MySQL + React 栈，本版 design 据此改写（见 Decision 1）。

## Goals / Non-Goals

**Goals:**

- 只有一条请求链路：浏览器 → Nginx → Go → MySQL；信任边界在 Go，浏览器与静态页一律不可信。
- 认证、授权、隔离三件事分别可判定：会话携带用户、角色、班级；学生上传 403；跨班 404。
- 上传链路完整且无脏数据：校验 → 落盘 → 解析 → 同事务双表入库 → 本班可见；原文件只经鉴权接口下载。
- 第三方按 README 执行 `docker compose up --build` 即可复现；down 再 up 数据仍在。

**Non-Goals:**

- 见 `proposal.md` 的 Non-goals。
- 不做"就绪探针"语义：`/health` 只表示进程存活。

## Decisions

### Decision 1：技术栈 —— Go + MySQL + React + Nginx（替代第 2 课初稿的 Flask + SQLite）

| 层 | 选型 | 用途 |
| --- | --- | --- |
| 前端 | React 18 + TypeScript + Vite；react-markdown + remark-gfm；原生 CSS | 登录页、材料页；Markdown 详情渲染；深浅色主题 |
| 后端 | Go + 标准库 `net/http`（不引入 Web 框架）；MySQL 驱动；`golang.org/x/crypto/bcrypt` | HTTP API、业务逻辑、口令哈希 |
| 数据 | MySQL 8.0 | 用户、班级、会话、材料、知识库正文 |
| 入口 | Nginx | 托管前端构建产物；同源反向代理 `/api` 与 `/health` |
| 部署 | Docker Compose | 统一构建并启动 web、api、db |

**理由：** 后续迭代要做检索并支持多连接，MySQL 比 SQLite 的文件锁更合适，Compose 环境与生产也更等价；前后端分离后信任边界清晰地落在 API 层。选的都是各自位置上最主流的方案，把注意力留给"谁能看到什么数据"。
**备选：** 保留 Flask + SQLite 单体——更轻、无需独立 db 容器，但与本课需求的架构不一致，且 SQLite 在 Compose 多进程下的等价性较弱，故不采用。

### Decision 2：登录态用服务端会话 + Cookie，不用 JWT

- 会话存 MySQL `sessions` 表：随机不可猜的会话 ID（≥ 32 字节随机数），关联 `user_id`、过期时间；TTL 由环境变量 `SESSION_TTL` 配置。
- Cookie：`HttpOnly`、`SameSite=Lax`、`Path=/`；本地 HTTP 环境不设 `Secure`（HTTPS 不在本迭代）。
- 登录成功时签发**新**会话 ID，并删除该用户携带的旧会话，防止会话固定。
- 登出：删除服务端会话行并清 Cookie；之后再用同一 Cookie 请求一律 401。
- 每次请求由中间件查 `sessions` 表并联表取 `role`、`class_id`，写入请求上下文；角色和班级**只**从这里取。

**备选：** JWT —— 无法在服务端立即作废（登出后旧 token 仍有效），会让"登出后旧 Cookie 失效"无法判定；localStorage 存 token 还会暴露给 XSS。故不采用。

### Decision 3：口令与登录防护

- 口令只存 bcrypt 哈希（`users.password_hash`）；预置账号口令从环境变量 `SEED_TEACHER_A_PASSWORD`、`SEED_STUDENT_A1_PASSWORD`、`SEED_STUDENT_B1_PASSWORD` 读取。
- 登录失败统一返回 401 与同一文案"用户名或密码错误"；用户不存在时也对一个固定假哈希做一次 bcrypt 比较，使耗时相近，避免枚举账号。
- 限流：按"用户名 + 客户端 IP"统计连续失败次数，达到 `LOGIN_MAX_FAILURES`（默认 5）后锁定 `LOGIN_LOCK_SECONDS`（默认 300 秒）；锁定期内即使口令正确也拒绝，响应与凭据错误**完全一致**。单实例，计数放内存即可（见 Risks）。

### Decision 4：班级隔离 —— 单库 + class_id，先取行再校验

- 班级只来自会话；query、Header、表单中的 `class_id` 一律忽略。
- 列表：SQL 强制 `WHERE class_id = ?`，参数取自会话。
- 按 ID 访问（详情、文件下载）：先按 `id` 取行（不在 SQL 里预先过滤班级），再比较 `row.class_id` 与会话班级；**不存在或跨班都返回 404**，响应体完全相同，不含对方任何字段。
- 写入：新材料与知识库条目的 `class_id` 只取自会话。
- `materials.class_id`、`knowledge_entries.class_id` 非空且建索引。

**备选：** 跨班返回 403 —— 会暴露"该 ID 存在"，故统一 404，并写入 README。

### Decision 5：上传与入库

```
POST /api/materials  (multipart/form-data, 字段 file、可选 title)
  ├─ 会话校验：无会话 → 401
  ├─ 角色校验：role != teacher → 403（尚未读取请求体、未落盘、未写库）
  ├─ http.MaxBytesReader(MAX_UPLOAD_BYTES)：超限 → 413（读完整请求体之前拒绝）
  ├─ 扩展名白名单 .txt / .md，否则 400
  ├─ 内容非空且为合法 UTF-8，否则 400
  ├─ 存储名由服务端生成：{UPLOAD_DIR}/{class_id}/{uuid}.{ext}；客户端文件名只作展示标题
  ├─ BEGIN
  │    INSERT materials(title, class_id, stored_name, original_name, size, uploaded_by, created_at)
  │    INSERT knowledge_entries(material_id, class_id, body_text, created_at)
  ├─ COMMIT；任何一步失败 → ROLLBACK + 删除已写文件 → 4xx/5xx
  └─ 201 {"id": ..., "title": ...}
```

- 能在落盘前完成的校验都放在落盘前。
- 详情接口返回标题、班级、上传时间与知识库正文，**不**返回磁盘路径。
- 下载 `GET /api/materials/{id}/file` 经会话 + 班级校验后由 Go 读文件返回；Nginx **不**把上传目录作为静态目录暴露，上传目录只挂载到 api 容器。

### Decision 6：数据模型（MySQL 8.0，utf8mb4）

| 表 | 关键字段 |
| --- | --- |
| `classes` | id, name UNIQUE |
| `users` | id, username UNIQUE, password_hash, role ENUM('teacher','student'), class_id FK |
| `sessions` | id (随机串 PK), user_id FK, expires_at, created_at |
| `materials` | id, class_id FK NOT NULL + INDEX, title, stored_name, original_name, size_bytes, uploaded_by FK, created_at |
| `knowledge_entries` | id, material_id FK UNIQUE, class_id NOT NULL + INDEX, body_text MEDIUMTEXT, created_at |

- 种子：班级 A/B；teacher_a（A）、student_a1（A）、student_b1（B）；A 班、B 班各一条标题含「A 班」「B 班」的材料及对应知识库正文。
- 种子**幂等**：以唯一键判断，重复启动不重复插入、不覆盖已上传内容。
- 应用使用普通数据库账号（仅本库的增删改查权限），不使用 root；MySQL root 口令不下发给 api。

### Decision 7：部署拓扑与接口

```
浏览器 ──:8080──▶ web (Nginx：静态前端 + 反代 /api、/health)
                     │
                     ▼
                  api (Go :8081，不映射宿主端口)  ── 上传目录 volume（仅挂 api）
                     │
                     ▼
                  db (MySQL 8.0，不映射宿主端口) ── 数据 volume
```

| 方法与路径 | 鉴权 | 说明 |
| --- | --- | --- |
| POST /api/login | 无 | 校验口令、限流、签发新会话 |
| POST /api/logout | 会话 | 删除服务端会话并清 Cookie |
| GET /api/me | 会话 | 返回 username、role、class_id、班级名 |
| GET /api/materials | 会话 | 本班列表，可带 `q` 按标题筛选（仅本班范围） |
| GET /api/materials/{id} | 会话 + 班级 | 详情与知识库正文；跨班或不存在 404 |
| GET /api/materials/{id}/file | 会话 + 班级 | 下载原文件；跨班或不存在 404 |
| POST /api/materials | 会话 + 教师 | 上传入库 |
| GET /health | 无 | 存活判定，返回 `{"status":"ok"}` |

- 开发时 Vite 代理 `/api`、`/health` 到后端，路径与生产 Nginx 一致。
- 数据库不可用时，业务接口返回 503，而不是把已登录用户判成 401。
- 环境变量（`.env.example` 全部列出，不含真实值）：`SESSION_SECRET`、`SESSION_TTL`、`DB_HOST`、`DB_NAME`、`DB_USER`、`DB_PASSWORD`、`MYSQL_ROOT_PASSWORD`（只给 db）、`UPLOAD_DIR`、`MAX_UPLOAD_BYTES`（默认 2 MB）、`LOGIN_MAX_FAILURES`、`LOGIN_LOCK_SECONDS`、`SEED_*_PASSWORD`、`WEB_PORT`（默认 8080）。必需项缺失时 api 启动失败并打印缺失的变量名，不使用内置默认密钥。

### 建议目录

```
backend/
  cmd/server/main.go
  internal/{config,db,auth,materials,knowledge}/
  migrations/  seed/
  Dockerfile
frontend/
  src/{pages,components,api}/
  nginx.conf  Dockerfile  vite.config.ts
docker-compose.yml  .env.example  .gitignore  .dockerignore
```

## Risks / Trade-offs

- [限流计数在内存，重启清零] → 单实例可接受；多副本需要改为共享存储，不在本迭代。
- [MySQL 首次启动需几十秒，入口短暂 502] → api 启动时重试连接数据库；Compose 为 db 配 healthcheck，api `depends_on` 条件为 healthy；README 说明。
- [8080 端口被占用] → 用 `WEB_PORT` 修改。
- [本地 HTTP 下 Cookie 无 Secure] → 仅限本地演示；HTTPS 在后续课程。
- [Markdown 渲染 XSS] → react-markdown 默认不渲染原始 HTML，保持该默认，不开启 `rehype-raw`。

## Migration Plan

从零起步，无旧数据；第 2 课初稿中的 Flask/SQLite 设计仅存在于规约，未产生代码，无需迁移。实现顺序见 `tasks.md` §1–§9。回滚：`docker compose down -v` 清空数据卷即回到初始状态。
