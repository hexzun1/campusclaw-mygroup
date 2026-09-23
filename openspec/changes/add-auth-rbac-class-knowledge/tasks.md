## 1. 仓库与文档（T1）

- [ ] 1.1 确认 README 含价值/场景/不做三行说明，AGENTS.md 含"无规约不写代码" — verify: 打开两文件可见对应内容，且 README 的"不做"与 proposal Non-goals 一致
- [ ] 1.2 添加 `requirements.txt`（Flask、gunicorn）、`.gitignore`（`.env`、`data/`、`uploads/`）与 `app/` 包骨架 — verify: `pip install -r requirements.txt` 后 `python -c "import app"` 无报错

## 2. 数据模型与种子（T2）

- [ ] 2.1 实现 `scripts/init_db.py`：创建 classes、users、materials、knowledge_entries、lectures、assignments、assistants、skills 表 — verify: 运行后 `sqlite3 data/app.db ".tables"` 列出上述 8 张表
- [ ] 2.2 写入种子：班级 A/B；teacher_a、student_a1、student_b1（密码写入 `password_hash`）；A 班、B 班各一条标题含「A 班」「B 班」的材料及对应知识库条目 — verify: `sqlite3 data/app.db "SELECT username, role, class_id FROM users"` 返回三行；`SELECT title FROM materials` 含两班标题
- [ ] 2.3 实现 `app/db.py`：连接管理、参数化查询、`list_materials(class_id)`、`get_material(id)` — verify: 临时脚本对班级 A 调用 `list_materials` 返回 ≥ 1 条且不含「B 班」标题

## 3. 登录、会话与密码（T3）

- [ ] 3.1 `create_app()` 从环境变量读取 `SECRET_KEY`，缺失即抛错退出 — verify: 不设 `SECRET_KEY` 运行 `flask run` 报错退出并提示变量名
- [ ] 3.2 实现 `GET/POST /login`：哈希校验成功后会话写入 `user_id`、`role`、`class_id` — verify: teacher_a、student_a1 登录后访问 `/materials` 返回 200
- [ ] 3.3 错误密码统一提示"用户名或密码错误"且不建立会话 — verify: 用错误口令登录后访问 `/materials` 仍被 302 到 `/login`
- [ ] 3.4 实现 `login_required`：页面 302 到 `/login`，API 返回 401 — verify: `curl -i localhost:5000/materials` 为 302；`curl -i localhost:5000/api/materials` 为 401 且无材料数据
- [ ] 3.5 实现 `POST /logout` — verify: 登出后访问 `/materials` 被 302 到 `/login`
- [ ] 3.6 确认密码字段为哈希 — verify: `sqlite3 data/app.db "SELECT password_hash FROM users"` 均为哈希前缀，不等于演示口令

## 4. 班级隔离（T4）

- [ ] 4.1 `/materials` 与 `/api/materials` 只按 `session['class_id']` 查询 — verify: student_a1 列表只含 A 班标题，不含「B 班」
- [ ] 4.2 `GET /api/materials/<id>` 校验班级，跨班统一 404 且不含他班内容 — verify: teacher_a 请求 B 班材料 id 返回 404，响应体无 B 班标题/正文/路径
- [ ] 4.3 路由忽略客户端传入的 `class_id` — verify: `/api/materials?class_id=<B班id>` 仍只返回 A 班记录
- [ ] 4.4 README 注明"跨班访问统一返回 404" — verify: README 可见该说明

## 5. 角色权限与上传入库（T5）

- [ ] 5.1 学生上传返回 403，不落盘不写库 — verify: student_a1 `POST /api/materials` 得 403；上传前后 materials、knowledge_entries 行数相同，`uploads/` 无新文件
- [ ] 5.2 教师上传：校验格式 → 落盘 → 解析 → 同一事务写 materials + knowledge_entries，`class_id` 取自会话 — verify: teacher_a 上传 `.md` 得 201，两表各增 1 行且 class 为 A
- [ ] 5.3 上传后列表可见 — verify: teacher_a、student_a1 刷新列表均见新标题；student_b1 列表不见
- [ ] 5.4 失败无脏数据 — verify: 上传 `.exe`、空文件分别得 400，两表行数不变，`uploads/` 无残留
- [ ] 5.5 学生页面不渲染上传表单 — verify: student_a1 打开 `/materials` 页面源码中无上传表单

## 6. Docker Compose 与 GET /health（T6，第 3 课实现）

- [ ] 6.1 实现 `GET /health`（无需登录，返回 `{"status":"ok"}`）— verify: 未登录 `curl -s localhost:5000/health` 返回 200 JSON
- [ ] 6.2 添加 `Dockerfile`、`docker-compose.yml`（挂载 `./data`、`./uploads`，`env_file: .env`，healthcheck）、`.env.example` — verify: `docker compose up --build -d` 后 `docker compose ps` 显示 healthy
- [ ] 6.3 启动时库不存在则自动 init_db — verify: 删除 `data/app.db` 后 `docker compose up` 仍可用预置账号登录
- [ ] 6.4 数据持久化 — verify: 上传一条后 `docker compose down && docker compose up -d`，列表仍含该条
- [ ] 6.5 README 补充启动步骤、登录地址、演示账号、health 地址与 `.env` 说明 — verify: 他人按 README 从零可打开登录页并 `curl -sf localhost:8080/health` 成功

## 7. 规约校验与收尾

- [ ] 7.1 运行 `openspec validate add-auth-rbac-class-knowledge --strict` — verify: 命令退出码 0 且无 error
- [ ] 7.2 手工对照 spec 关键 Scenario：跨班被拒、学生上传 403、上传后列表可查、重建容器数据仍在 — verify: 在 PR 或实验报告中记录各项"通过/不通过"
