> Apply 约定：一个 task 一轮 —— 读 task → 实现 → 对照 spec 审查 diff → 运行 verify → 通过后勾选 `[x]` → 提交。verify 未通过不得勾选，不得带入下一项。
> 开发期约定：`docker-compose.dev.yml` 把 api 映射到 `127.0.0.1:8081`、db 到 `127.0.0.1:3306`；下文 `$API` 指 `http://localhost:8081`。取 token 的写法：`TOKEN=$(curl -s -H 'Content-Type: application/json' -d '{"username":"student_a1","password":"..."}' $API/api/login | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')`，之后用 `-H "Authorization: Bearer $TOKEN"`；**不再使用 `curl -c/-b` Cookie jar**。手工伪造 token 时用 `python3` 的 `hmac` + `base64.urlsafe_b64encode` 现场构造，密钥取自本机 `.env` 的 `JWT_SECRET`。
> 与 `add-traceable-vector-retrieval` 的关系：它的验收命令若使用 Cookie jar，实施时须改成 Bearer 头（其 spec 中的"会话"措辞已被本 change 的术语声明覆盖，无需改 spec）。两个 change 修改的 `auth-upload` 条目互不重叠。
> 依赖 `github.com/golang-jwt/jwt/v5` 的引入并入 3.1（原 1.3 已删除）：3.1 写出第一行 import 时一起 `go get`，verify 用"`go mod tidy` 后 `go.mod` 仍含该模块"把"依赖确实被用到"绑在一起，避免未被 import 的依赖被 tidy 删掉而留下空窗。

## 1. 配置

- [x] 1.1 改造 `internal/config`：移除 `SESSION_SECRET`、`SESSION_TTL`；新增必需的 `JWT_SECRET`（少于 32 字符视为不合格）与可选的 `JWT_TTL`（默认 24h）；错误信息只说明变量名与原因，不回显密钥值 — verify: `go test ./internal/config` 覆盖"缺失报错并列出 JWT_SECRET""31 字符报错""32 字符通过""JWT_TTL 缺省为 24h""错误文本不含密钥值"；不设 `JWT_SECRET` 运行 `go run ./cmd/server` 以非 0 退出且输出含 `JWT_SECRET`
- [x] 1.2 更新 `.env.example`（删 `SESSION_*`，加 `JWT_SECRET`、`JWT_TTL` 的占位值）并同步本机 `.env`（不提交） — verify: `grep -c SESSION_ .env.example` 为 0；`grep -n JWT_SECRET .env.example` 出现且值为占位；`git ls-files | grep -x .env` 无输出

## 2. 吊销表与数据层

- [x] 2.1 api 启动时幂等创建 `revoked_tokens`（`jti CHAR(32)` 主键、`user_id`、`expires_at`、`revoked_at`、`expires_at` 索引）；`internal/db` 增加 `RevokeToken`（`INSERT IGNORE`）、`IsRevoked`、`PurgeExpiredRevocations` — verify: 连续启动 api 两次均成功且 `SHOW CREATE TABLE revoked_tokens` 含上述列与索引；临时测试：吊销一个 `jti` 后 `IsRevoked` 为真、未吊销的为假；已过期的吊销行被 `PurgeExpiredRevocations` 删除而未过期的保留

## 3. Token 签发与校验

- [x] 3.1 引入依赖 `github.com/golang-jwt/jwt/v5`（`go get`）并实现签发：HS256、载荷含 `user_id`、`username`、`role`、`class_id`、`iss=campusclaw-api`、`iat`、`exp`（`iat + JWT_TTL`）、随机 128 位 `jti`；时钟可注入 — verify: `cd backend && go mod tidy` 后 `go.mod` 仍含 `github.com/golang-jwt/jwt/v5`，且 `go build ./...` 成功；单测解码后断言载荷键集合恰好是这八项、`exp - iat` 为 24h、两次签发的 `jti` 不同、载荷不含 password / hash 字样
- [x] 3.2 实现校验（只接受 HS256、必须有 `exp`、`iss` 必须匹配，并检查 `jti` 非空、`user_id>0`、`class_id>0`、`role∈{teacher,student}`） — verify: `go test ./internal/auth` 表驱动覆盖：正确 token 通过；他钥签名、载荷被改而签名未重算、`alg=none`、`HS512`、已过期（注入时钟）、`iss` 不符、分别缺 `jti/exp/role/class_id`、格式非三段，均返回错误；错误文本不含 token 内容

## 4. 中间件与端点

- [x] 4.1 用 `RequireAuth` 取代读 Cookie 的中间件：解析 `Authorization: Bearer`（scheme 大小写不敏感）→ 校验 → 查吊销表（查库出错返回 503，不当作 401）→ 把 `user_id/username/role/class_id` 写入现有请求上下文身份结构（`httpapi.SessionUserFromContext` 入口与字段名不变），所有认证失败共用同一个 401 响应体 — verify: 不带头、`Bearer` 后为空、`Basic xxx`、乱码 token 各得 401 且响应体逐字节相同；有效 token 调用 `/api/me` 得 200；把有效 token 放在 query（`?token=`）或 Cookie 中而不带头得 401；401 响应体不含任何材料字段
- [x] 4.2 改造 `POST /api/login`：限流、假哈希比较、统一 401 文案原样保留；成功后签发 token，响应 `{token, username, role}`，不再写会话表、不再 `Set-Cookie` — verify: `curl -i` 登录成功得 200、响应头无 `Set-Cookie`、JSON 含 `token`，解码载荷得到 `role`、`class_id`、`iss=campusclaw-api`；错误口令与不存在用户的响应逐字节相同；连续 5 次失败后正确口令仍得同样的 401 且不含 token
- [x] 4.3 `GET /api/me`：身份取自 token，仅 `class_name` 按 `class_id` 查库 — verify: teacher_a 的 token 返回用户名、`teacher`、班级 A 及班级名；用于篡改后的 token 得 401
- [x] 4.4 `POST /api/logout`：通过同一中间件后吊销当前 `jti`（`expires_at` 取 token 的 `exp`）并顺手清理已过期吊销行 — verify: 登出得 200；同一 token 再调用 `/api/me` 与再次 `POST /api/logout` 均得 401；`SELECT jti, expires_at FROM revoked_tokens` 有该行；同一用户登出前另取的第二个 token 仍能调用 `/api/me`
- [x] 4.5 重新登录签发新 token、旧 token 不受影响 — verify: 同一用户连续登录两次得到 `jti` 不同的两个 token，两者调用 `/api/me` 均得 200
- [x] 4.6 删除已无用的 Cookie 会话代码（`cookie.go`、读写 `sessions` 的函数与登录里的相关调用、`SetCookie` 引用），`sessions` 表保留不动 — verify: `go build ./... && go vet ./...` 通过；`grep -rniE "session_id|SetCookie|ReadSessionCookie|GetSessionUser" backend` 无输出；`SHOW TABLES` 仍含 `sessions`
- [x] 4.7 给整个 `/api` 路由外层加响应头中间件，使所有响应（含 401/404/503）带 `Cache-Control: no-store` — verify: `curl -i` 分别请求登录成功、`/api/materials`、原文件下载、无 token 的 401、不存在 ID 的 404，响应头均含 `Cache-Control: no-store`
- [x] 4.8 曲线层伪造矩阵 — verify: 脚本用 `JWT_SECRET` 与他钥各构造 token（篡改 `role=teacher`、篡改 `class_id`=B 班、`alg=none`、`alg=HS512`、`exp` 已过、`iss=other`、缺 `jti`），逐个调用 `GET /api/me`，全部得 401 且响应体逐字节相同
- [x] 4.9 迭代 1 关键行为回归（改用 Bearer 重跑）— verify: student_a1 上传得 403 且三表行数与 `uploads/` 不变；学生在表单 / Header 带 `role=teacher` 仍 403；把学生 token 的 `role` 改成 `teacher` 得 401；student_a1 列表只含 A 班且附带 `?class_id=<B 班>` 结果不变；teacher_a 请求 B 班材料 ID 与请求 `999999` 的详情和 `/file` 响应 `diff` 无输出；teacher_a 上传附带 `class_id=<B 班>` 仍归属 A 班
- [x] 4.10 数据库不可用时校验返回 503 — verify: `docker compose -f docker-compose.dev.yml stop db` 后带有效 token 调用 `/api/materials` 得 503（不是 401）；启动 db 后同一 token 再调用得 200
- [x] 4.11 `/health` 与 `/api/login` 无需 token — verify: 不带头 `curl $API/health` 得 200 `{"status":"ok"}`；不带头登录按各自规则响应而不是 401（正确口令得 200）

## 5. 原文件下载的文件名

- [x] 5.1 `GET /api/materials/{id}/file` 的 `Content-Disposition` 改为 RFC 5987 形式（`filename*=UTF-8''<百分号编码>`，并保留 ASCII 兜底 `filename=`），供前端 Blob 下载解码 — verify: 上传文件名为 `中文说明.md` 的材料后，`curl -i -H "Authorization: Bearer $TOKEN"` 下载，响应头含 `filename*=UTF-8''%E4%B8%AD%E6%96%87%E8%AF%B4%E6%98%8E.md`，响应体与原文件 `diff` 无输出

## 6. 前端

- [x] 6.1 `api/client.ts`：token 读写封装在 `sessionStorage`（固定键，值只存 token）；除登录外的每个请求加 `Authorization: Bearer`；去掉 `credentials: 'include'`；401 时清除 token 并触发既有的回登录页回调 — verify: `grep -rn "credentials" frontend/src` 无输出；`cd frontend && npm run build` 成功；浏览器登录后网络面板里 `/api/me`、`/api/materials` 请求都带 `Authorization`，而 `/api/login` 不带
- [x] 6.2 `auth/AuthContext.tsx`：启动时无 token 直接进登录页；有 token 则调用 `/api/me`，失败即清除 token 回登录页 — verify: 登录后刷新仍停在材料页；手工把 `sessionStorage` 里的 token 改成乱码后刷新，回到登录页且该键被清除
- [x] 6.3 登录页：成功后保存响应中的 `token` 再进入材料页，失败仍只显示"用户名或密码错误" — verify: 输入错误口令提示与该文案一致；正确登录后 DevTools → Application 中 `sessionStorage` 只有 token 一项、`localStorage` 为空、站点下没有任何 Cookie
- [x] 6.4 登出：调用 `POST /api/logout`（带头），无论结果如何都清除 token 并回登录页 — verify: 登出后浏览器后退，材料页无法取到数据并回到登录页；用登出前的 token 直接 `curl /api/me` 得 401
- [x] 6.5 401 统一处理 — verify: 登录后在数据库里向 `revoked_tokens` 手工插入当前 token 的 `jti`，再在页面执行搜索，页面回到登录页且 `sessionStorage` 中 token 已清除
- [x] 6.6 原文件下载改为带头请求 + Blob 保存（解码 `filename*`），移除 `<a href>` 直链 — verify: 点击下载时网络面板里该请求带 `Authorization`；保存内容与上传原文件一致（可在页面内对 Blob 与 `/api/materials/{id}` 的正文比对）；`curl` 不带头请求同一地址得 401；`grep -rn "fileDownloadUrl" frontend/src` 不再被用作链接 `href`
- [x] 6.7 确认前端不存在新的注入面 — verify: `grep -rnE "dangerouslySetInnerHTML|rehype-raw|innerHTML" frontend/src` 无输出
- [x] 6.8 前端回归 — verify: 浏览器里依次完成：student_a1 登录（无上传入口）→ 搜索 → 打开详情 → 下载；teacher_a 登录（有上传入口）→ 上传 `.exe` 显示失败原因 → 上传含表格与 `<script>alert(1)</script>` 的 `.md`，表格渲染且无弹窗；主题切换；登出

## 7. 浏览器端防护

- [x] 7.1 `frontend/nginx.conf` 在 server 级用 `add_header ... always` 加 CSP（`default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`），子 `location` 内不再定义 `add_header` — verify: `docker compose build web` 成功；`docker compose up -d` 后 `curl -sI http://localhost:8080/` 含 `Content-Security-Policy`，其中 `script-src 'self'` 且整行不含 `unsafe-inline`、`unsafe-eval`
- [x] 7.2 在 CSP 下用真实浏览器（生产 nginx，`http://localhost:8080`）走完整流程 — verify: 登录、列表、搜索、详情、上传、下载、主题切换、登出全部可用，浏览器控制台无 CSP 违规；若出现违规，收窄策略或修正代码，**不得**加 `unsafe-inline`

## 8. 部署、文档与收尾

- [x] 8.1 生产编排联调 — verify: `.env` 缺 `JWT_SECRET` 时 `docker compose up -d` 后 `docker compose logs api` 显示缺失 `JWT_SECRET` 并退出；补齐后三个服务正常，经 `http://localhost:8080` 登录返回 token，`docker compose port api 8081` 与 `docker compose port db 3306` 仍无输出
- [x] 8.2 更新 README：新变量 `JWT_SECRET/JWT_TTL`、curl 示例改用 Bearer、说明 token 存放在 `sessionStorage` 及其 XSS 取舍、旧 Cookie 登录失效需重新登录 — verify: 按 README 的 curl 示例从登录到取列表跑通一次
- [x] 8.3 编写 `docs/jwt-bearer-auth.md`：为何推翻迭代 1 的"否决 JWT"、存放位置取舍表与最终选择、新增风险（密钥泄露伪造身份、XSS 窃取、多 token 并存）、以及关键 Scenario 的命令与输出（伪造矩阵 401、登出后旧 token 401、重新登录两 token 并存、跨班 404、学生上传 403、DB 停止 503、下载需鉴权、响应头） — verify: 每条 Scenario 都有"通过 / 不通过"结论与命令输出
- [x] 8.4 在 `docs/iteration-1.md` 顶部加一句说明"登录会话方案已被 switch-to-jwt-bearer-auth 取代，Cookie 相关命令与决策 2 仅作历史记录" — verify: 打开文件首屏可见该说明，其余内容未改动
- [x] 8.5 运行 `openspec validate switch-to-jwt-bearer-auth --strict` — verify: 退出码 0 且无 error（"auth-upload 主规格尚不存在"的 INFO 提示属预期，见 8.7）
- [x] 8.6 确认仓库无真实密钥 — verify: `git ls-files | grep -x .env` 无输出；`git grep -n "JWT_SECRET="` 只出现在 `.env.example` 且值为占位；`git grep -n "Bearer ey"` 无输出
- [x] 8.7 归档本 change。**前置：`add-auth-rbac-class-knowledge` 已归档**；需使用者明确指示后执行 `/opsx:archive` — verify: `openspec list` 中无本 change；`openspec/specs/auth-upload/spec.md` 的「登录与会话」「角色权限」「班级隔离」「口令与密钥安全」「前端页面行为」已是 JWT 版本，并含「浏览器端防护响应头」 —— **2026-09-30 归档：5 处 MODIFIED 逐块替换、1 处 ADDED 追加（共 11 需求），替换块与 delta 逐字节一致，未触碰的 5 个需求保持原样；change 移至 `archive/2026-09-30-switch-to-jwt-bearer-auth/`**
