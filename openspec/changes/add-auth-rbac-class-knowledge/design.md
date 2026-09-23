## Context

仓库根已有 README 三行说明、项目规则文件与本 change 四件套，尚无业务代码。动机见 `proposal.md` 的 Why；可验收行为见 `specs/auth-upload/spec.md`。Apply 阶段（第 3 课）在本仓库实现 CampusClaw 最小可运行栈。

## Goals / Non-Goals

**Goals:**

- 登录可用，会话携带 `user_id`、`role` 与 `class_id`。
- 权限在服务端可判定：学生上传返回 403。
- 班级隔离不可通过改前端或篡改请求参数绕过：列表查询与按 ID 访问都校验班级。
- 教师上传链路完整：落盘 → 解析 → 同一事务写入材料表与知识库表 → 本班列表查库可见。
- `docker compose up` 一键启动；`GET /health` 供探活；密钥与数据库路径可通过环境变量配置。

**Non-Goals:**

- 见 `proposal.md` 的 Non-goals（检索问答、对话、作业、账号管理、SSO、生产 HA 等）。
- 不做管理员角色与跨班管理 API。

## Decisions

### Decision 1：技术栈 Flask + SQLite

| 层次 | 选择 | 说明 |
| --- | --- | --- |
| Web | Flask 3.x | 路由；Jinja2 服务端渲染登录页与材料列表；上传与 JSON API 同进程 |
| DB | SQLite 3 | 单文件 `data/app.db`，标准库 `sqlite3`，全部使用参数化查询 |
| 密码 | werkzeug.security（pbkdf2/scrypt）或 bcrypt | 在 `requirements.txt` 显式列出 |
| 运行 | gunicorn（Compose 内）/ `flask run`（本地开发） | Compose 内绑定 `0.0.0.0:8080` |

**备选：** Node + Express + better-sqlite3。行为等价即可；选 Python 以减少课程环境差异。若换栈，须同步改 tasks 中的命令，spec 不变。

### Decision 2：服务端签名会话（而非 JWT）

- 登录成功后写入 Flask signed cookie session，设置 `HttpOnly`、`SameSite=Lax`；payload 至少含 `user_id`、`role`（`teacher` | `student`）、`class_id`。
- 签名密钥 `SECRET_KEY` **只从环境变量读取**；应用启动时若缺失则直接报错退出，不回退到默认值。
- 受保护路由统一前置校验（`login_required` 装饰器）：无有效会话时，页面请求 `302` 到 `/login`，API 请求返回 `401` JSON，且不渲染任何业务数据。
- 登出：`POST /logout` 清除会话并重定向到 `/login`。

**备选：** JWT。本项目为同源服务端渲染，cookie 会话更简单，也便于服务端立即失效。

### Decision 3：密码哈希存储

- 用户表只有 `password_hash` 列，不存在明文 `password` 列。
- 种子脚本写入哈希；登录用库函数校验（如 `check_password_hash` / `bcrypt.checkpw`），依赖库的恒定时间比较。
- 登录失败统一返回"用户名或密码错误"，不区分是用户不存在还是密码错误。

**备选：** 仅 PBKDF2 手写实现——易出错，不采用。

### Decision 4：单库 + `class_id` 过滤实现班级隔离

隔离 **MUST 在服务端实现**，前端隐藏按钮不算。

- **列表查询**：统一封装 `list_materials(class_id)`，SQL 强制 `WHERE class_id = ?`，参数只来自 `session['class_id']`。
- **按 ID 访问**：先按 `id` 查行；不存在返回 `404`；`row.class_id != session['class_id']` 时**统一返回 `404`**（不暴露他班资源是否存在），响应体不含他班标题、正文或路径。该约定同时写入 README。
- **写入**：新记录的 `class_id` 只取自会话；表单或 query 中的 `class_id` 一律忽略。
- 不提供"代传他班"或管理员跨班 API。

**备选：** 每班一个独立数据库——隔离更强但运维与种子复杂，不适合当前规模。

### Decision 5：上传与入库数据流

```
Client (teacher, multipart/form-data)
POST /api/materials
  │
  ├─ login_required；role != teacher → 403（不落盘、不写库）
  ├─ 校验扩展名（.txt / .md）、非空、大小 ≤ 2 MB，否则 400
  ├─ 保存 uploads/{class_id}/{uuid}_{safe_filename}
  ├─ 解析为 UTF-8 纯文本；失败 → 删除已存文件，返回 400，不写库
  ├─ BEGIN TRANSACTION
  │    INSERT materials (title, class_id, file_path, uploaded_by, created_at)
  │    INSERT knowledge_entries (material_id, class_id, body_text, created_at)
  ├─ COMMIT（任一步失败 → ROLLBACK 并删除文件，返回 500）
  └─ 201 {"material_id": ...}

Client GET /materials（HTML）或 GET /api/materials（JSON）
  ├─ login_required
  ├─ rows = list_materials(session.class_id)
  └─ 200
```

- 标题：优先取表单字段 `title`，缺省用文件名（去扩展名）。
- 知识库：MVP 写入解析后的全文 `body_text`；分块、向量化、RAG 不在本 change。
- 学生与教师共用列表接口；学生页面不渲染上传表单，服务端仍拒绝其写请求。

### Decision 6：数据模型

| 表 | 关键字段 | 说明 |
| --- | --- | --- |
| `classes` | id, name | 预置班级 A、班级 B |
| `users` | id, username UNIQUE, password_hash, role, class_id | 预置 teacher_a、student_a1、student_b1 |
| `materials` | id, title, class_id, file_path, uploaded_by, created_at | 教学材料元数据 |
| `knowledge_entries` | id, material_id, class_id, body_text, created_at | 知识库条目 |
| `lectures` / `assignments` / `assistants` / `skills` | id, class_id, title 等 | 六类核心结构中的占位表，可各含 0~1 行 |

种子材料：A 班、B 班各至少一条，标题分别含「A 班」「B 班」，并在 `knowledge_entries` 中有对应条目。

### Decision 7：Docker Compose 与 `GET /health`

- `Dockerfile`：基于 `python:3.12-slim`，安装依赖，复制 `app/`、`scripts/`，`CMD` 启动 gunicorn。
- `docker-compose.yml`：service `app`，`build: .`，`ports: ["8080:8080"]`，`env_file: .env`，`volumes: ./data:/app/data`、`./uploads:/app/uploads`，`healthcheck` 调用 `http://127.0.0.1:8080/health`。
- 启动入口：若 `data/app.db` 不存在，先运行 `python scripts/init_db.py` 建表并写种子，再启动服务。
- `.env.example`：列出 `SECRET_KEY=`（占位）与 `DATABASE_PATH=data/app.db`，不含真实密钥；`.env` 加入 `.gitignore`。
- `GET /health`：无需登录，返回 `200` 与 `{"status":"ok"}`；数据库文件不可读时返回 `503`。

### 建议模块划分

```
app/
  __init__.py      # create_app()，校验 SECRET_KEY
  auth.py          # /login、/logout、login_required、role_required
  materials.py     # 列表、按 id 查看、上传
  knowledge.py     # parse_file()、写入 knowledge_entries
  db.py            # 连接与带 class_id 的查询封装
  templates/       # login.html、materials.html
scripts/init_db.py # 建表 + 种子
```

## Risks / Trade-offs

- [PDF/Word 解析复杂] → 首版只支持 `.txt` / `.md`；失败时 spec 要求不留脏数据。
- [跨班返回 403 还是 404 不一致] → 本 design 固定为 404，并写入 README 与 tasks 验收。
- [SQLite 并发写有限] → 课程演示规模可接受；后续如需扩展再换数据库。
- [Flask 开发服务器不适合容器] → Compose 内使用 gunicorn。
- [会话密钥泄露或弱密钥] → 只从环境变量读取，缺失即启动失败；`.env` 不入库。

## Migration Plan

从零起步，无旧数据迁移：`init_db.py` 建表并写种子。实现顺序见 `tasks.md`：数据 → 登录 → 隔离 → 上传 → Compose → validate。回滚方式：`docker compose down` 后删除 `data/` 与 `uploads/` 即可恢复初始状态。
