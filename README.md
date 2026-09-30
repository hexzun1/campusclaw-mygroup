# CampusClaw

- **价值**：面向中小学的教研智能体，把分散在群文件与网盘里的教学材料集中进按班级隔离的知识库，为后续检索与智能问答打底。
- **场景**：教师登录后上传并管理本班教学材料；学生登录后只读查看、下载本班材料；不同班级之间数据互不可见。
- **本学期不做**：知识库问答与检索、对话助手、作业流程、注册与找回密码、OAuth/SSO、平台超级管理员、PDF/Word 解析、公网部署与多副本高可用。

## 当前迭代

迭代 1（v0.1.0-auth-upload）：登录与角色权限、班级数据隔离、材料上传与知识库入库。规约见 `openspec/changes/add-auth-rbac-class-knowledge/`。

- 技术栈：React + TypeScript + Vite（前端）/ Go net/http（后端）/ MySQL 8.0 / Nginx / Docker Compose
- 部署形态：单实例 Docker Compose，只暴露 web 端口（默认 8080）
- 跨班访问：访问其他班级的材料与访问不存在的材料，统一返回 **404**

登录凭据已从「服务端会话 + HttpOnly Cookie」改为 **JWT Bearer token**（见 `openspec/changes/switch-to-jwt-bearer-auth/`）：登录接口在响应 JSON 中返回 `token`，之后每个请求用 `Authorization: Bearer <token>` 携带，登出即吊销该 token。

## 快速开始

```bash
cp .env.example .env
# 编辑 .env，为 JWT_SECRET（至少 32 个字符）/ DB_PASSWORD / MYSQL_ROOT_PASSWORD / SEED_*_PASSWORD 等填入真实值
docker compose up --build -d
```

- 登录地址：http://localhost:8080 （`WEB_PORT` 未设置时默认 8080；被占用可在 `.env` 中修改 `WEB_PORT`）
- 健康检查：http://localhost:8080/health → `{"status":"ok"}`，无需登录

### 用 curl 调接口

```bash
# 1) 登录取 token（口令取自本机 .env，不要写进命令历史或文档）
TOKEN=$(curl -s -H 'Content-Type: application/json' \
  -d '{"username":"teacher_a","password":"<SEED_TEACHER_A_PASSWORD>"}' \
  http://localhost:8080/api/login | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

# 2) 带 Bearer 头取本班材料列表
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/materials

# 3) 当前身份；4) 登出（该 token 立即失效）
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/me
curl -s -X POST -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/logout
```

- **不再使用 `curl -c/-b` Cookie jar**：token 放在 query、Cookie 或请求体里都会被忽略，只有 `Authorization` 头有效。
- `JWT_TTL` 可选，默认 `24h`；到期后需重新登录（不做刷新令牌）。重新登录不会踢掉旧 token，旧 token 到自己过期或被登出吊销为止一直有效。

## 预置账号

| 用户名 | 角色 | 班级 | 密码 |
| --- | --- | --- | --- |
| teacher_a | 教师 | A 班 | 来自 `.env` 的 `SEED_TEACHER_A_PASSWORD` |
| student_a1 | 学生 | A 班 | 来自 `.env` 的 `SEED_STUDENT_A1_PASSWORD` |
| student_b1 | 学生 | B 班 | 来自 `.env` 的 `SEED_STUDENT_B1_PASSWORD` |

首次启动会自动建表并写入以上账号及两班各一条示例材料，种子过程幂等，重启不会重复插入或覆盖已上传内容。

## 已知行为与限制

- **token 存放在 `sessionStorage`**：前端能读到 token 才能把它放进 `Authorization` 头，这是 Bearer 方案的固有代价——页面一旦出现 XSS，token 就可能被读走。因此只把 token 存进 `sessionStorage`（关闭标签页即清除，且不跨标签页共享），不存 `localStorage`，不存用户名/角色；服务端再用 `Cache-Control: no-store` 与严格 CSP（`script-src 'self'`，无 `unsafe-inline`/`unsafe-eval`）收敛风险。发现泄露时更换 `JWT_SECRET` 即可让全部 token 失效。
- **从迭代 1 升级需重新登录**：旧的会话 Cookie 不再被接受（`sessions` 表保留但已不再读写），`SESSION_SECRET`/`SESSION_TTL` 由 `JWT_SECRET`/`JWT_TTL` 取代。
- **跨班访问统一 404**：无论是访问不存在的材料 ID，还是访问其他班级的材料 ID，接口都返回相同的 404 响应，不会泄露"该 ID 是否存在"。
- **单实例部署**：当前只支持单个 api/db 实例（登录限流计数在内存中，重启会清零），不做多副本、公网证书与 CI/CD，详见 `openspec/changes/add-auth-rbac-class-knowledge/proposal.md` 的 Non-goals。
- **MySQL 首次启动较慢**：db 容器首次初始化建表可能需要几十秒，此时访问 web 可能短暂看到 502，等待 `docker compose ps` 中 db 变为 `healthy` 后即可正常访问。
- **修改端口**：如需更换对外端口，在 `.env` 中设置 `WEB_PORT` 后重新 `docker compose up -d` 即可，无需改动 compose 文件。
