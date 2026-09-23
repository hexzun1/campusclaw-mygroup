> Apply 约定：一个 task 一轮 —— 读 task → 实现 → 对照 spec 审查 diff → 运行 verify → 通过后勾选 `[x]` → 提交。verify 未通过不得勾选，不得带入下一项。
> 开发期约定：`docker-compose.dev.yml` 仅供本地调试，把 api 映射到 `127.0.0.1:8081`、db 映射到 `127.0.0.1:3306`；下文 `$API` 指 `http://localhost:8081`。正式交付只用 `docker-compose.yml`（仅暴露 web）。

## 1. 项目骨架与配置

- [ ] 1.1 确认 README 含价值/场景/不做三行说明，AGENTS.md 与 CLAUDE.md 含"无规约不写代码" — verify: 打开文件可见，README 的"不做"与 proposal Non-goals 一致
- [ ] 1.2 初始化 Go 模块：`backend/cmd/server`、`backend/internal/{config,db,auth,materials,knowledge}` — verify: `cd backend && go build ./...` 成功
- [ ] 1.3 实现 `internal/config`：从环境变量读取 design Decision 7 所列全部变量，必需项缺失时报错退出并打印变量名 — verify: 不设 `SESSION_SECRET` 运行 `go run ./cmd/server` 以非 0 退出，输出含 `SESSION_SECRET`
- [ ] 1.4 用 Vite 初始化 `frontend/`（React 18 + TypeScript），预留登录页与材料页路由；`vite.config.ts` 代理 `/api`、`/health` 到 `http://localhost:8081` — verify: `cd frontend && npm run build` 成功
- [ ] 1.5 提交 `.env.example`（全部变量名 + 占位值）、`.gitignore`、`.dockerignore`（排除 `.env`、`uploads/`、`node_modules/`）与 `docker-compose.dev.yml`（仅 db + api，端口只绑 127.0.0.1）— verify: `git status` 中无 `.env`；`grep -c = .env.example` ≥ 12

## 2. 数据层与种子

- [ ] 2.1 编写建表迁移：classes、users、sessions、materials、knowledge_entries（utf8mb4；两表 class_id 非空并建索引）— verify: `docker compose -f docker-compose.dev.yml up -d db` 后启动 api，`mysql ... -e "SHOW TABLES"` 列出 5 张表
- [ ] 2.2 创建普通数据库账号供 api 使用（非 root，仅本库权限）— verify: `SHOW GRANTS FOR '<DB_USER>'` 不含全局权限
- [ ] 2.3 实现幂等种子：班级 A/B；teacher_a、student_a1、student_b1（口令 bcrypt，来自 `SEED_*_PASSWORD`）；两班各一条标题含「A 班」「B 班」的材料及知识库正文 — verify: `SELECT username, role, class_id FROM users` 返回 3 行；`SELECT title FROM materials` 含两班标题
- [ ] 2.4 验证种子幂等 — verify: 重启 api 两次后 users 仍为 3 行、materials 行数不变
- [ ] 2.5 实现 `internal/db` 查询封装：`ListMaterials(classID, q)` 强制带班级条件；`GetMaterialByID(id)` 只按 id 取行（班级校验留到 §4）— verify: 临时测试对班级 A 调用 `ListMaterials` 返回 ≥ 1 条且不含「B 班」

## 3. 登录、会话与限流

- [ ] 3.1 实现 `POST /api/login`：bcrypt 校验；成功时生成 ≥ 32 字节随机会话 ID 写入 sessions，删除请求携带的旧会话；设置 HttpOnly、SameSite=Lax Cookie — verify: `curl -i -c jar -d '{"username":"teacher_a","password":"..."}' $API/api/login` 返回 200 且 `Set-Cookie` 含 `HttpOnly` 与 `SameSite=Lax`
- [ ] 3.2 实现会话中间件：查 sessions 联表取 user_id、role、class_id 放入请求上下文；无效或过期返回 401 JSON，不含业务数据 — verify: 不带 Cookie `curl -i $API/api/materials` 返回 401，响应体无材料标题
- [ ] 3.3 实现 `GET /api/me` — verify: 带 jar 调用返回 `teacher_a`、`teacher`、班级 A
- [ ] 3.4 失败响应统一：错误口令与不存在用户均返回 401 + "用户名或密码错误"；用户不存在时也做一次 bcrypt 比较 — verify: 两种请求的 `curl -s` 输出完全一致
- [ ] 3.5 实现"用户名 + IP"失败限流（`LOGIN_MAX_FAILURES`、`LOGIN_LOCK_SECONDS`），锁定期响应与凭据错误一致 — verify: 连续 5 次错误后用正确口令登录仍返回同样的 401 响应体
- [ ] 3.6 登录签发新会话 — verify: 带旧 Cookie 再次登录后，用旧会话 ID 调用 `/api/me` 返回 401
- [ ] 3.7 实现 `POST /api/logout`：删除会话行并清 Cookie — verify: 登出后用原 Cookie 调用 `/api/me` 返回 401

## 4. 班级隔离

- [ ] 4.1 `GET /api/materials` 只用会话班级，忽略 query / Header / body 中的 class_id；支持 `q` 标题筛选 — verify: student_a1 调用 `$API/api/materials?class_id=<B班ID>` 结果只含 A 班标题
- [ ] 4.2 实现 `GET /api/materials/{id}`：先取行再比班级，不存在与跨班返回完全相同的 404 — verify: teacher_a 请求 B 班材料 ID 与请求 `999999` 的响应码与响应体完全一致（`diff` 无输出）
- [ ] 4.3 详情响应含标题、班级、上传时间、知识库正文，不含存储名或磁盘路径 — verify: student_a1 请求 A 班材料返回 200，JSON 中无 `stored_name`、`path` 字段

## 5. 角色授权与上传入库

- [ ] 5.1 `POST /api/materials` 先校验会话与角色，学生直接 403（读请求体前）— verify: student_a1 上传后返回 403；两表 `COUNT(*)` 前后相同，`ls uploads/` 无新增
- [ ] 5.2 伪造角色无效 — verify: student_a1 表单加 `role=teacher`、Header 加 `X-Role: teacher` 仍返回 403
- [ ] 5.3 上传校验：`MaxBytesReader` 超限 413；扩展名白名单 `.txt/.md` 否则 400；空内容或非 UTF-8 返回 400 — verify: 分别上传超限文件、`.exe`、空 `.txt`、GBK 编码 `.txt`，得到 413/400/400/400，两表与 `uploads/` 均无变化
- [ ] 5.4 成功路径：服务端生成存储名落盘 → 解析 → 同一事务写 materials + knowledge_entries（class_id 取自会话）→ 201；任一步失败回滚并删文件 — verify: teacher_a 上传 `.md` 得 201，两表各增 1 行，class 为 A，正文与文件一致
- [ ] 5.5 上传归属会话班级 — verify: teacher_a 表单附带 `class_id=<B班ID>` 上传后，新记录 class 仍为 A；student_b1 列表不含该条
- [ ] 5.6 路径穿越防护 — verify: 上传文件名 `../../etc/passwd.md`，文件保存在 `uploads/<班级>/` 下且名为服务端生成的 uuid
- [ ] 5.7 上传后本班可见 — verify: teacher_a 与 student_a1 调用 `/api/materials` 均含新标题

## 6. 材料读取 API 收尾

- [ ] 6.1 实现 `GET /api/materials/{id}/file`：会话 + 班级校验后由 Go 返回原文件；跨班与不存在同为 404 — verify: student_a1 下载 A 班文件内容与原文件 `diff` 一致；下载 B 班 ID 返回 404
- [ ] 6.2 数据库不可用时业务接口返回 503 — verify: `docker compose -f docker-compose.dev.yml stop db` 后带有效 Cookie 调用 `/api/materials` 返回 503，Cookie 未被清除
- [ ] 6.3 实现 `GET /health`（无需登录、只判存活）— verify: 不带 Cookie `curl -s $API/health` 返回 200 `{"status":"ok"}`

## 7. 前端页面

- [ ] 7.1 API 封装：所有请求走同源 `/api`，`credentials: 'include'`；任一请求 401 时清空用户状态并跳转登录页 — verify: 登录后在浏览器删掉 Cookie 再点刷新列表，页面回到登录页
- [ ] 7.2 登录页：提交 `/api/login`，失败只显示"用户名或密码错误" — verify: 输入错误口令，页面提示与该文案一致
- [ ] 7.3 启动时调用 `/api/me` 决定进入材料页或登录页；不在 localStorage / sessionStorage 存会话、角色或 token — verify: 刷新材料页仍停留；DevTools → Application 中两种 Storage 无相关键
- [ ] 7.4 材料列表：请求 `/api/materials`，支持标题搜索框（本班范围）— verify: 输入「A 班」只显示匹配项，从不出现 B 班材料
- [ ] 7.5 上传入口按 `/api/me` 的角色显示：仅教师可见；上传成功或失败给出提示并刷新列表 — verify: student_a1 页面无上传入口；teacher_a 上传 `.exe` 显示失败原因，上传 `.md` 显示成功并出现在列表
- [ ] 7.6 详情与下载：详情走 `/api/materials/{id}`，`.md` 用 react-markdown + remark-gfm 渲染且不启用原始 HTML；下载走 `.../file` — verify: 上传含表格与 `<script>alert(1)</script>` 的 `.md`，详情中表格正确渲染、无弹窗；点击下载得到原文件
- [ ] 7.7 深浅色主题切换 — verify: 点击切换按钮，页面背景与文字颜色在两套主题间切换
- [ ] 7.8 登出按钮调用 `/api/logout` 后回到登录页 — verify: 登出后浏览器后退，材料页无法取到数据并回到登录页

## 8. Docker Compose 与文档

- [ ] 8.1 编写 `backend/Dockerfile`（多阶段构建 Go 二进制）与 `frontend/Dockerfile`（构建产物 + Nginx）— verify: `docker compose build` 成功
- [ ] 8.2 编写 `frontend/nginx.conf`：托管静态文件；反代 `/api/`、`/health` 到 api；**不**配置任何指向上传目录的 location — verify: `grep -n uploads frontend/nginx.conf` 无输出
- [ ] 8.3 编写 `docker-compose.yml`：web（映射 `${WEB_PORT:-8080}:80`）、api、db（MySQL 8.0，带 healthcheck，不映射端口）；db 数据卷与 uploads 卷（仅挂 api）；api `depends_on` db healthy — verify: `docker compose up --build -d` 后 `docker compose ps` 三个服务均为 running/healthy
- [ ] 8.4 只暴露 web 端口 — verify: `docker compose port db 3306` 与 `docker compose port api 8081` 均无输出；本机 `nc -z localhost 3306` 失败
- [ ] 8.5 上传目录不可直接访问 — verify: `curl -i http://localhost:8080/uploads/任意名.md` 不返回文件内容
- [ ] 8.6 持久化 — verify: teacher_a 上传一条后 `docker compose down && docker compose up -d`，列表仍含该条且可下载
- [ ] 8.7 README 补充：启动步骤（`cp .env.example .env` → 填值 → `docker compose up --build -d`）、登录地址、health 地址、预置账号、跨班统一返回 404、单实例说明、MySQL 首次启动可能短暂 502、`WEB_PORT` 改端口 — verify: 请同伴只按 README 从零启动并登录成功

## 9. 发布验收与归档

- [ ] 9.1 运行 `openspec validate add-auth-rbac-class-knowledge --strict` — verify: 退出码 0 且无 error
- [ ] 9.2 按 spec 逐条跑关键 Scenario（至少：未登录 401、学生上传 403、跨班按 ID 404、登出后旧 Cookie 401、重建后数据仍在），把命令与输出写进迭代说明 `docs/iteration-1.md` — verify: 文档中每条 Scenario 都有"通过/不通过"结论与命令输出
- [ ] 9.3 在 `docs/iteration-1.md` 用自己的话写三项设计决策（技术栈、会话方案、跨班 404）及否决的备选 — verify: 三项各含"决策 / 备选 / 理由"
- [ ] 9.4 确认仓库无真实密钥 — verify: `git ls-files | grep -x .env` 无输出；`git grep -n "SESSION_SECRET="` 只出现在 `.env.example` 且值为占位
- [ ] 9.5 同伴交叉验证：请同伴用本仓库与预置账号试一条跨班 URL — verify: 迭代说明记录同伴姓名与结论（返回 404）
- [ ] 9.6 打 tag `v0.1.0-auth-upload` 并推送 — verify: `git tag` 列出该 tag；从 tag 检出后按 README 可启动
- [ ] 9.7 执行 `/opsx:archive` 归档本 change — verify: `openspec list` 无活动变更；`openspec/specs/auth-upload/spec.md` 已生成且与 delta 一致
