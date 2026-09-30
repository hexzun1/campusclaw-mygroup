## Why

迭代 1 用"服务端会话表 + HttpOnly Cookie"做登录态，并在 Non-goals 与 design 里明确否决了 JWT。现在需求改为让客户端持有自包含的 JWT Bearer token，并以 `Authorization` 头调用 API：身份（`role`、`class_id`）由服务端验签后读取，不再依赖浏览器自动携带的 Cookie。本变更**有意推翻**迭代 1 的这一决策，同时必须守住迭代 1 已验收的行为——班级隔离、学生上传 403、跨班 404、登录失败统一提示与限流。

## What Changes

- **登录返回 token**：`POST /api/login` 成功后在响应 JSON 中返回 `token`，不再设置会话 Cookie。token 为 HS256 签名的 JWT，载荷含 `user_id`、`username`、`role`、`class_id`、`iss=campusclaw-api`、`iat`、`exp`（24 小时），另带唯一的 `jti`。签名密钥只来自环境变量 `JWT_SECRET`，缺失（或过短）则 api 启动失败。
- **统一 Bearer 校验**：除 `POST /api/login` 与 `GET /health` 外，所有 `/api` 请求必须带 `Authorization: Bearer <token>`；缺失、格式错误、签名错误、算法不符、过期、`iss` 不符、载荷缺项，一律 401。`role` 与 `class_id` 只从已验签的 token 读取，请求参数、Header、请求体中的同名字段继续被忽略；放在 query 或 Cookie 里的 token 无效。
- **登出吊销**：登出时把该 token 的 `jti` 写入吊销表；校验时拒绝已吊销的 `jti`。因此校验仍要访问一次数据库，数据库不可用时沿用迭代 1 的约定返回 503，而不是 401。
- **前端**：登录后保存 token，并在每个 API 请求上加 `Authorization` 头；收到 401 清除 token 并回登录页；不再依赖 Cookie。**原文件下载不能再用 `<a href>`**（浏览器导航不会带 `Authorization` 头），改为带头请求后以 Blob 保存。
- **配套防护**：`/api` 响应加 `Cache-Control: no-store`（避免带鉴权头的响应被浏览器缓存后在登出后被重放）；静态页加内容安全策略（CSP）以限制脚本来源，缓解 token 存放在前端可读存储中带来的 XSS 风险。
- **行为变化（需注意）**：
  - **BREAKING**：迭代 1 已签发的会话 Cookie 全部失效，所有用户需重新登录；`SESSION_SECRET`、`SESSION_TTL` 不再使用，改为 `JWT_SECRET`、`JWT_TTL`（默认 24h）。
  - 重新登录不再使旧凭据失效：JWT 无服务端签发记录，同一用户可同时持有多个有效 token，旧 token 到 `exp` 或被登出吊销为止。迭代 1 的「登录签发新会话 → 旧会话 ID 失效」场景因此改写。
  - 前端不再禁止使用 Web Storage：token（且仅 token）存入 `sessionStorage`；理由与 XSS 取舍见 design.md。
- 班级隔离、学生上传 403、跨班与不存在同为 404、登录失败统一文案与"用户名 + IP"限流、DB 不可用返回 503 等既有行为**全部保持不变**。

## Capabilities

### New Capabilities

（无——不引入新的能力。）

### Modified Capabilities

- `auth-upload`：
  - 「登录与会话」：Cookie 会话改为 JWT Bearer；新增载荷内容、校验规则、吊销、每次请求的失败情形；「登录签发新会话」与「登出后旧 Cookie 失效」改写；并在此声明规约中"会话 / 会话班级 / 会话角色"指已验签 token 所表示的登录状态与身份。
  - 「角色权限」：授权依据由"服务端会话中的角色"改为"已验签 token 中的角色"。
  - 「班级隔离」：班级来源由"服务端会话"改为"已验签 token"。
  - 「口令与密钥安全」：会话密钥改为 `JWT_SECRET`，缺失或过短则启动失败。
  - 「前端页面行为」：token 存放与携带方式、下载方式、401 处理、不依赖 Cookie。
  - 新增（ADDED）「浏览器端防护响应头」：`/api` 不可缓存、静态页带 CSP。

> 前置与协调：
> - `openspec/specs/` 目前为空，`auth-upload` 主规格要等 `add-auth-rbac-class-knowledge` 归档后才会生成（其 tasks 9.5–9.7 未完成），本 change 的 MODIFIED 需在其归档之后再归档。
> - `add-traceable-vector-retrieval` 同样对 `auth-upload` 做 MODIFIED，但修改的是「材料上传与知识库入库」「预置核心数据」「Compose 部署与健康检查」，与本 change 修改的条目**互不重叠**，两者归档顺序不受限。为避免重叠，本 change 通过「登录与会话」中的术语声明让其它条目里的"会话"措辞继续成立，不去改它们。

## Impact

- **后端**（`backend/`）：新增 JWT 签发 / 校验；重写 `internal/auth` 的登录、登出、`/api/me` 与中间件；删除 Cookie 相关代码；`internal/db` 新增吊销表读写并停止使用 `sessions` 相关函数；`internal/config` 增加 `JWT_SECRET`、`JWT_TTL`，去掉 `SESSION_*`；`/api` 响应统一加 `Cache-Control: no-store`。新增依赖：`github.com/golang-jwt/jwt/v5`。
- **数据库**：新增 `revoked_tokens` 表（启动时幂等创建）；`sessions` 表保留但不再使用，删除留待后续迁移。
- **前端**（`frontend/`）：`api/client.ts`（Authorization 头、去掉 `credentials`、Blob 下载）、`auth/AuthContext.tsx`（启动时按 token 决定登录态）、`pages/MaterialsPage.tsx`（下载按钮）；`nginx.conf` 增加 CSP 头。
- **配置与文档**：`.env.example`、`README.md`（新变量、curl 示例改用 Bearer）；`docs/` 增加本次决策记录（推翻迭代 1 决策 2 的原因与新增风险）。
- **既有验收脚本**：迭代 1 文档里使用 `curl -c/-b` Cookie jar 的命令不再适用，需要改用 `Authorization` 头。

## Non-goals（非目标）

- **刷新令牌 / 滑动续期**：token 24 小时到期后重新登录，不做 refresh token，不做自动续期。
- **非对称签名与密钥轮换**：只用 HS256 单一密钥；不做 RS256/ES256、`kid`、多密钥轮换。
- **"登出所有设备"与登录即踢掉旧 token**：吊销粒度是单个 `jti`，不记录用户签发历史。
- **权限变更即时生效**：`role`、`class_id` 在签发时写入 token，24 小时内即使数据库里的用户信息改变，token 里的仍然有效（当前系统也没有改角色 / 换班级的功能）。
- **把 JWT 放进 HttpOnly Cookie**：这会保留 Cookie 的 CSRF 问题并偏离"Bearer 头"的要求，见 design 备选。
- **其它登录方案**：不做 OAuth、SSO、验证码、注册、找回密码、改密。
- **功能改动**：不改变班级隔离、上传、下载、检索等任何业务行为；不新增业务接口。
- **服务端渲染与跨域**：仍是同源部署，不引入 CORS。
