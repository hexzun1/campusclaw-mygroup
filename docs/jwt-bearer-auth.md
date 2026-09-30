# JWT Bearer 登录（switch-to-jwt-bearer-auth）

规约：`openspec/changes/switch-to-jwt-bearer-auth/`（`specs/auth-upload/spec.md` 的「登录与会话」「角色权限」「班级隔离」「口令与密钥安全」「前端页面行为」「浏览器端防护响应头」）

本文记录三件事：为什么推翻迭代 1 的「否决 JWT」、token 存放位置的取舍与据此新增的风险、以及各关键 Scenario 的命令与实测输出。命令均以生产编排为入口（`docker compose up -d`，web 映射到 `localhost:8080`），并在本次实施中逐条跑过。

## 1. 为什么推翻迭代 1 的「否决 JWT」

迭代 1 在 `proposal.md` 的 Non-goals 与 `design.md` 的 Decision 2 里明确否决了 JWT，并禁止前端使用 Web Storage 保存 token，理由是「HttpOnly Cookie 对脚本不可读」。本次需求要求客户端持自包含 token、以 `Authorization` 头调用 API，这两条就必须推翻：

| | 迭代 1（会话 Cookie） | 本次（JWT Bearer） |
| --- | --- | --- |
| 凭据存放 | 服务端 `sessions` 表 + HttpOnly Cookie | 自包含 token，存 `sessionStorage` |
| 携带方式 | 浏览器自动带 Cookie | 每次请求显式加 `Authorization` 头 |
| 页内 XSS 能否读走凭据 | 不能（HttpOnly） | **能**，这是 Bearer 方案的固有代价 |
| 登出即失效 | 删会话行即可 | 需要吊销表按 `jti` 记一笔 |
| 身份变更即时性 | 每次请求联表，即时 | 24h 内沿用 token 里的 `role`/`class_id` |

推翻的同时守住了迭代 1 已验收的行为：班级隔离、学生上传 403、跨班与不存在同为 404、登录失败统一文案与限流、DB 不可用返回 503——本文第 4 节的 Scenario 就是这些行为的回归记录。

`sessions` 表保留未删（删除需要迁移能力，本 change 不引入迁移执行器），只是不再读写；`revoked_tokens` 由 api 启动时幂等建表。

## 2. token 存放位置：取舍与最终选择

token 必须能被页面脚本读到才能放进 `Authorization` 头，所以「页内 XSS 就能读走 token」无法靠选存放位置消除，只能比较暴露窗口：

| 方案 | 页内 XSS 能否读走 | 刷新后保留 | 关闭标签页后 | 跨标签页 | 其它 |
| --- | --- | --- | --- | --- | --- |
| `localStorage` | 能 | 是 | **仍保留**（直到 24h 过期或登出） | 共享 | 暴露窗口最长 |
| **`sessionStorage`（选用）** | 能 | 是 | 清除 | 每个标签页独立 | 暴露窗口收敛为标签页寿命，代价是新开标签页要重新登录 |
| 仅内存变量 | 能（可 hook `fetch`） | **否** | 清除 | 独立 | 可用性最差，刷新即登出 |
| JWT 放 HttpOnly Cookie | 不能读，但同页 XSS 仍可发请求 | 是 | 视设置 | 共享 | 重新引入 CSRF，且偏离「Bearer 头」要求 |

**选择 `sessionStorage`，键固定为 `campusclaw_token`，值只放 token**：满足「登录后保存 token 且刷新保持登录」，同时把非 XSS 场景下的暴露面从「24 小时且跨浏览器重启」收窄到「当前标签页寿命」。除 token 外不存用户名、角色、会话 ID；不使用 `localStorage`；不依赖任何 Cookie。

## 3. 新增风险与缓解

- **`JWT_SECRET` 泄露的后果比会话密钥大**：会话方案下密钥泄露无法伪造身份，JWT 下泄露即可伪造任意班级的教师。缓解：只从环境变量读取、少于 32 字符直接启动失败、不入日志与错误响应、不入仓库；泄露时更换密钥即让全部 token 失效（所有人重新登录）。
- **XSS 可窃取 token**：缓解为纵深防御——严格 CSP（`script-src 'self'`，无 `unsafe-inline`/`unsafe-eval`，禁 `object`，禁被嵌入框架）、Markdown 不渲染原始 HTML（`react-markdown` 不启用 `rehype-raw`）、代码中无 `dangerouslySetInnerHTML`、token 24h 过期且登出立即吊销。
- **多个有效 token 并存**：不记录签发历史，重新登录不会踢掉旧 token（这是「登出即失效」用 `jti` 黑名单实现的必然结果）。需要「登出所有设备」时应另开变更引入 `tokens_valid_after`。
- **24h 内身份不随数据库变化**：token 里的 `role`/`class_id` 在有效期内固定；当前系统没有改角色/换班级功能，日后引入时需在中间件补「用户仍存在且角色班级一致」的校验。
- **每个请求仍查一次库**：为满足登出即失效，每次校验都要查 `revoked_tokens`，DB 不可用时业务全部 503（与迭代 1 的会话查询同等代价）。
- **`sessionStorage` 使新标签页需重新登录**：可接受的可用性代价。

## 4. 关键 Scenario 验收

以下命令的输出为本次实施中的实测结果（口令取自本机 `.env`，文中以 `<PASSWORD>` 代替；token 以 `$TOKEN` 代替）。

### 4.1 登录返回 token、且不设置 Cookie

```
$ curl -s -i -H 'Content-Type: application/json' \
    -d '{"username":"teacher_a","password":"<PASSWORD>"}' \
    http://localhost:8080/api/login
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
Cache-Control: no-store

{"token":"<JWT>","username":"teacher_a","role":"teacher"}
```

解码载荷后键集合恰好为八项 `class_id,exp,iat,iss,jti,role,user_id,username`，`alg=HS256`、`iss=campusclaw-api`、`exp-iat=24h`，响应头无 `Set-Cookie`。

**结论：通过**。

### 4.2 伪造矩阵 → 全部 401，且响应体逐字节相同

用本机 `JWT_SECRET` 与他钥分别现场构造 token（`hmac` + `base64url`，见 `openspec/changes/switch-to-jwt-bearer-auth/tasks.md` 顶部约定），逐个调用 `GET /api/me`：

| 用例 | 结果 |
| --- | --- |
| 他钥签名 | 401 |
| 篡改 `role=teacher`（签名未重算） | 401 |
| `alg=none` | 401 |
| `alg=HS512`（真钥） | 401 |
| `exp` 已过 | 401 |
| `iss=other` | 401 |
| 缺 `jti` | 401 |
| 缺 `exp` | 401 |
| 对照：真签名的 student token | 200 |

```
$ python3 verify_scenarios.py      # 本次实施用的验收脚本
PASS  8 类伪造 token 全部 401  — 去重后响应体 1 种
PASS  伪造 401 响应体与未登录一致
PASS  学生 token 篡改 role 后得 401  — HTTP 401
```

所有 401 的响应体去重后只有一种：`{"error":"未登录或会话已失效"}`。

**结论：通过**。

### 4.3 token 只认 `Authorization` 头

```
$ curl -s -o /dev/null -w '%{http_code}\n' "http://localhost:8080/api/me?token=$TOKEN"
401
$ curl -s -o /dev/null -w '%{http_code}\n' -H "Cookie: campusclaw_token=$TOKEN" http://localhost:8080/api/me
401
```

`Bearer` 前缀大小写不敏感；`Bearer` 后为空、`Basic xxx`、乱码 token 也都是 401，且响应体与不带头发起时逐字节相同；`role`、`class_id` 只取自已验签的 token，表单与 Header 里的同名字段没有代码路径读取（迭代 1 回归中「学生上传附带 `role=teacher`」仍为 403）。

**结论：通过**。

### 4.4 登出后旧 token 立即失效，其它 token 不受影响

```
$ curl -s -X POST -H "Authorization: Bearer $T1" http://localhost:8080/api/logout
{"ok":true}
$ curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $T1" http://localhost:8080/api/me
401
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST -H "Authorization: Bearer $T1" http://localhost:8080/api/logout
401
$ curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $T2" http://localhost:8080/api/me
200
```

`revoked_tokens` 中该行 `expires_at` 取自 token 的 `exp`（实测比 `revoked_at` 晚整 24 小时）；登出时顺手清理已自然过期的吊销行。

**结论：通过**。

### 4.5 重新登录签发新 token，两个 token 并存

```
PASS  重新登录签发不同 jti 的两个 token
PASS  同一用户其它 token 不受影响  — HTTP 200
```

同一用户连续登录两次，`jti` 不同，两者调用 `/api/me` 均为 200（行为变化：迭代 1 的「登录签发新会话 → 旧会话失效」不再成立）。

**结论：通过**。

### 4.6 权限与班级隔离（迭代 1 回归）

```
$ printf '# 学生上传测试\n' > student-upload.md
$ curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $STUDENT_TOKEN" \
    -F "file=@student-upload.md" -F 'title=学生上传测试' http://localhost:8080/api/materials
403
$ curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $STUDENT_TOKEN" \
    -F "file=@student-upload.md" -F 'role=teacher' http://localhost:8080/api/materials   # 表单伪造角色
403
$ curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $STUDENT_TOKEN" \
    -H 'role: teacher' -F "file=@student-upload.md" http://localhost:8080/api/materials  # Header 伪造角色
403
```

三次尝试前后 `materials`、`knowledge_entries` 行数均为 `16 / 16`（`SELECT COUNT(*)` 实测），上传目录也没有新增文件。

```
PASS  A 班学生列表只含 A 班  — 15 条
PASS  B 班学生列表只含 B 班  — 1 条
PASS  跨班详情 404 且与不存在 ID 响应一致  — HTTP 404/404
```

`?class_id=` / Header 里的班级参数被忽略；teacher_a 上传时附带班级 B 的 ID，新材料仍归属班级 A。

**结论：通过**。

### 4.7 数据库不可用时返回 503（不是 401）

```
$ docker compose stop db
$ curl -s -i -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/materials
HTTP/1.1 503 Service Unavailable

{"error":"服务暂不可用，请稍后重试"}

$ docker compose start db     # 等 db healthy 后
$ curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/materials
200
```

吊销表查不动时既不放行也不判为无效：查库出错返回 503，恢复后同一 token 仍有效。

**结论：通过**。

### 4.8 原文件下载需鉴权，文件名按 RFC 5987 解码

```
$ curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/api/materials/$ID/file   # 不带头
401
$ curl -s -i -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/materials/$ID/file | grep -i content-disposition
Content-Disposition: attachment; filename="____________.md"; filename*=UTF-8''%E4%B8%AD%E6%96%87%E8%AF%B4%E6%98%8E.md
```

响应体与原文件 `diff` 无输出。前端不再用 `<a href>` 直链，改为 `fetch` + `Authorization` → `blob()` → 临时对象 URL 触发下载（`grep -rn "fileDownloadUrl" frontend/src` 无输出）。

**结论：通过**。

### 4.9 浏览器端防护响应头

```
$ curl -sI http://localhost:8080/ | grep -i content-security-policy
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'

$ curl -sI -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/materials | grep -i cache-control
Cache-Control: no-store
```

`/api` 的成功、401、404、503 响应全部带 `Cache-Control: no-store`（`/health` 不受影响）；CSP 声明在 nginx `server` 级且带 `always`，子 `location` 不重复声明 `add_header`（否则会覆盖继承）。

CSP 正向对照（headless Chrome，站在 `http://localhost:8080/`）：

```
PASS  内联 <script> 被 CSP 阻止（未执行）
PASS  内联事件处理器被 CSP 阻止（未执行）
PASS  控制台记录了 CSP 拒绝  — 2 条
      error: security: Executing inline script violates the following Content Security Policy directive 'script-src 'self''
      error: security: Executing inline event handler violates the following Content Security Policy directive 'script-src 'self''
```

**结论：通过**。

## 5. 前端行为验收（真实浏览器）

用 headless Chrome + CDP 驱动 Vite 开发服务器与生产 nginx（带 CSP）各跑一遍完整流程，共 24 + 8 + 15 项断言全部通过，覆盖：

- 登录成功后进入材料页；`/api/login` 不带 `Authorization`，`/api/me`、`/api/materials` 都带；`sessionStorage` 只有 `campusclaw_token` 一项，`localStorage` 为空，站点下没有任何 Cookie；
- 刷新仍停留在材料页；把 `sessionStorage` 里的 token 改成乱码后刷新，回到登录页且该键被清除；无 token 时不会发出 `/api/me` 请求；
- 错误口令只提示「用户名或密码错误」，不区分账号是否存在；
- 登出调用 `POST /api/logout`（带头），清除 token 并回登录页；登出后直接访问 `/materials` 回登录页；用登出前的 token 请求 `/api/me` 得 401；
- 手工向 `revoked_tokens` 插入当前 token 的 `jti` 后执行搜索，页面回登录页且 token 已清除；
- 下载请求带 `Authorization`，`filename*` 被解码为 `中文说明.md`，保存内容与上传原文件逐字节一致；不带头的同一下载地址得 401；
- 学生登录看不到上传入口；teacher 上传 `.exe` 显示「不支持的文件类型」；上传含表格与 `<script>alert(1)</script>` 的 `.md`，表格渲染、脚本作为文本显示、无弹窗；主题切换与登出正常；生产 CSP 页面上无控制台违规。

**结论：通过**。

## 6. 回滚

1. 回退代码与镜像；`sessions` 表未被改动，旧版本可直接恢复 Cookie 登录。
2. `revoked_tokens` 留在库中无害。
3. 旧 `.env` 需要补回 `SESSION_SECRET`（回退后 api 会要求它），`JWT_SECRET`/`JWT_TTL` 可保留不用。

## 附：与迭代 1 文档的关系

`docs/iteration-1.md` 中的 Cookie 命令与「否决 JWT」的决策均为历史记录，已被本 change 取代，见该文档顶部的说明。
