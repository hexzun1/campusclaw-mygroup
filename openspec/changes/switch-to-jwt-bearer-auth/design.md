## Context

迭代 1 的登录态是服务端会话：`sessions` 表存随机会话 ID，`internal/auth` 的中间件读 Cookie、`db.GetSessionUser` 联表取 `user_id / role / class_id / 班级名`，写入请求上下文；`internal/materials` 的所有 handler 只通过 `httpapi.SessionUserFromContext` 读取身份。前端 `api/client.ts` 用 `credentials: 'include'`，原文件下载是 `<a href>` 链接。数据库不可用时业务接口返回 503（不是 401）。

约束与已有事实：

- 建表靠 MySQL 镜像的 `initdb.d`（只在数据卷为空时执行），后端**没有**迁移执行器；`add-traceable-vector-retrieval` 计划引入执行器，但尚未实施。
- 迭代 1 的 Non-goals 与 design Decision 2 明确否决了 JWT、并禁止前端使用 Web Storage 保存 token；本变更有意推翻这两点，见 `proposal.md`。
- 可验收行为见 `specs/auth-upload/spec.md`；动机与范围见 `proposal.md`。

## Goals / Non-Goals

**Goals:**

- 认证凭据由客户端以 `Authorization` 头携带，服务端验签后读取身份；`internal/materials` 及其它业务 handler **不改动**（继续从请求上下文读身份），把回归面压到认证层。
- 登出可立即让 token 失效；所有认证失败对外一律 401 且不可区分原因。
- 既有的班级隔离、403/404 约定、登录统一提示与限流、DB 不可用返回 503 保持不变。
- 明确记录 token 存放位置的取舍与 XSS 风险，并用低成本措施收敛风险。

**Non-Goals:**

- 见 `proposal.md` 的 Non-goals。
- 不追求"无状态"：为满足登出即失效，每个请求仍要查一次库（吊销表），这是需求的直接结果。

## Decisions

### Decision 1：JWT 库与校验参数 —— `golang-jwt/jwt/v5`，白名单式解析

使用 `github.com/golang-jwt/jwt/v5`，解析时固定：只接受 `HS256`（`WithValidMethods`）、必须有 `exp`（`WithExpirationRequired`）、`iss` 必须等于 `campusclaw-api`（`WithIssuer`）、`iat` 校验开启；密钥由 keyfunc 返回 `[]byte(JWT_SECRET)`。解析成功后再自行检查 `jti` 非空、`user_id > 0`、`class_id > 0`、`role ∈ {teacher, student}`，任何一项不满足都当作无效 token。`alg=none`、`HS384/HS512`、算法与密钥类型不匹配都被白名单挡掉。

**备选：** 用 `crypto/hmac` 自己实现 HS256 —— 代码量不大，但 header 解析、`alg` 混淆、base64url 填充、常量时间比较都是容易写错的安全细节，交给维护中的库并显式收紧参数更稳妥；它不是 Web 框架，不违背"不引入框架"的原则。

### Decision 2：token 内容与身份来源

载荷：`user_id`、`username`、`role`、`class_id`、`iss=campusclaw-api`、`iat`、`exp = iat + JWT_TTL`（默认 24h）、`jti`（128 位随机数的十六进制，`crypto/rand`）。载荷不含口令、哈希、班级名。

- 中间件验签通过后，把 `user_id / username / role / class_id` 填进现有的请求上下文身份结构（保持 `httpapi.SessionUserFromContext` 这一入口与字段名，**业务 handler 无需改动**）；`ClassName` 不在 token 里，只有 `GET /api/me` 需要时再按 `class_id` 查库补上。
- `role` 与 `class_id` 只来自 token；query、Header、表单里的同名字段依旧没有任何代码路径去读取。
- 用户被删除或改班 / 改角色时，24 小时内 token 仍然有效——当前系统没有这类功能，记为已知权衡（见 Risks）。

### Decision 3：登出吊销 —— `revoked_tokens` 表，逐请求查询，失败即关

```
revoked_tokens(
  jti        CHAR(32) PRIMARY KEY,
  user_id    INT      NOT NULL,
  expires_at DATETIME NOT NULL,          -- 取自 token 的 exp
  revoked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_revoked_expires (expires_at)
)
```

- 中间件顺序：取头 → 验签与声明 → 查 `revoked_tokens`。**查库出错返回 503，不当作 401**（延续迭代 1 约定，也避免数据库抖动时把有效 token 误判为无效；同时"失败即关"——查不了就不放行）。
- 登出：先通过同一中间件（无效 / 已吊销 token 调用登出得 401），再 `INSERT IGNORE` 该 `jti`，并顺手 `DELETE ... WHERE expires_at < NOW()` 清理已自然过期的行（吊销记录只需保留到 token 本来的过期时间）。
- 建表方式：**api 启动时执行幂等的 `CREATE TABLE IF NOT EXISTS revoked_tokens`**，而不是新增 `migrations/*.sql`。原因：现有数据卷不会重跑 `initdb.d`，而迁移执行器属于另一个尚未实施的 change；这里选最小、幂等的启动期建表，并避免与那个 change 的 `002_*.sql` 编号冲突。待迁移执行器落地后，可把这段并入迁移（不影响行为）。
- `sessions` 表保留、不再读写；删除它需要迁移能力，留待后续。这也让**回滚**简单：回退代码即可恢复 Cookie 登录。

**备选：** 只维护"用户级 `tokens_valid_after` 时间戳"以便登录即踢掉旧 token 或"登出所有设备"——需要改用户表并改变多设备语义，超出本次范围；`jti` 黑名单正是需求指定的做法。

### Decision 4：token 存放位置 —— `sessionStorage`（取舍与 XSS 风险）

这是本变更最实质的安全取舍：迭代 1 的 HttpOnly Cookie **对页面脚本不可读**，换成 JWT 后，token 必须能被前端 JS 读到才能放进 `Authorization` 头，因此**只要页面出现一次 XSS，攻击者就能读走 token**。这是选择 Bearer 方案的固有代价，无法靠选存放位置消除。

| 方案 | 页内 XSS 能否读走 | 刷新后保留 | 关闭标签页 / 浏览器后 | 跨标签页 | 其它特点 |
| --- | --- | --- | --- | --- | --- |
| `localStorage` | 能 | 是 | **仍保留**（直到 24h 过期或登出） | 共享 | 暴露窗口最长；被浏览器扩展、共享电脑遗留、其它同源页面读取的面最大 |
| `sessionStorage` | 能 | 是 | 关闭标签页即清除 | 每个标签页独立 | 暴露窗口 = 标签页寿命；新开标签页需重新登录 |
| 仅内存（模块变量） | 能（可 hook `fetch`，或直接调用应用自己的 API 函数），但拿不走静态存储 | **否**（刷新即登出） | 清除 | 独立 | 对"存储被静态读取"最稳，但可用性最差，且与"刷新后停留在材料页"的既有体验冲突 |
| HttpOnly Cookie 存放 JWT（备选） | 不能读，但同页 XSS 仍可**发请求** | 是 | 视 Cookie 设置 | 共享 | 重新引入 Cookie 自动携带带来的 CSRF 问题，且偏离"Bearer 头、不再依赖 Cookie"的要求 |

**决定：`sessionStorage`，键固定、值只放 token 字符串。** 理由：需求要求"登录后保存 token"且刷新后应保持登录，排除纯内存；相比 `localStorage`，`sessionStorage` 把暴露窗口从"24 小时且跨标签页 / 跨浏览器重启"收窄到"当前标签页的生命周期"，代价只是新开标签页要重新登录，对教学演示场景可接受。需要说明的是，三种前端可读存储在**页内 XSS**面前防护力相同——`sessionStorage` 的收益在于缩小非 XSS 场景下的暴露面，而不是防住 XSS 本身。

**对 XSS 本身的缓解（纵深防御）：**

1. **CSP**（`nginx.conf` 响应头）：`default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`。不含 `unsafe-inline` / `unsafe-eval`，注入的内联脚本无法执行；外部图片被禁也顺带阻止了 Markdown 图片的外联追踪。Vite 生产构建只产出外链脚本与样式，前端未使用内联 `style` 属性，预计兼容，须在浏览器中实测（见 tasks）。
2. **保持无注入面**：`react-markdown` 不启用原始 HTML（`rehype-raw` 保持关闭，迭代 1 已验收）；代码中不使用 `dangerouslySetInnerHTML`（用检索确认）；材料标题、正文一律作为文本节点渲染。
3. **缩短并可撤销**：token 24 小时过期，登出立即吊销；发现泄露时可通过更换 `JWT_SECRET` 使全部 token 失效。
4. **不落地**：token 不进 URL（下载不用 query 传 token）、不写日志、不进错误响应；`/api` 响应 `Cache-Control: no-store`。

### Decision 5：原文件下载 —— 带头请求 + Blob 保存

`<a href>` 导航无法带 `Authorization` 头。前端改为：`fetch(下载地址, {headers: Authorization})` → `response.blob()` → 临时对象 URL → 触发下载 → `URL.revokeObjectURL`。文件上限 2 MB（可配置），内存 Blob 可接受。401 走统一的"清 token 回登录页"处理。

- 文件名：前端从 `Content-Disposition` 读取。迭代 1 里服务端把 UTF-8 文件名直接放在 `filename="..."` 中，浏览器按 Latin-1 解码会得到乱码（已在迭代 1 的验证中观察到）；因此同时把服务端改为 RFC 5987 的 `filename*=UTF-8''<百分号编码>`（并保留 ASCII 兜底 `filename=`），前端解码该字段。

**备选：** ① 签发一次性 / 短时效的下载 URL（token 放 query）—— 需要新接口、新的过期与吊销逻辑，且 URL 会进浏览器历史、代理与访问日志；② 下载仍用 Cookie —— 与"不再依赖 Cookie"矛盾。

### Decision 6：登录、`/api/me` 与限流的改动范围

- 登录：限流、假哈希比较、统一 401 文案**原样保留**；成功后不再 `DeleteSessionsForUser` / `CreateSession` / `Set-Cookie`，改为签发 token，响应 `{token, username, role}`（`username`、`role` 为便利字段，前端仍以 `/api/me` 为准）。请求里带的旧 Cookie 被忽略。
- `GET /api/me`：`user_id / username / role / class_id` 取自 token，`class_name` 按 `class_id` 查库；其余不变。
- 所有认证失败（缺头、格式错、签名错、算法错、过期、`iss` 错、载荷缺项、已吊销）对外**同一个** 401 响应体（沿用现有文案），日志里可记录内部原因，但不含 token 内容。

### Decision 7：配置与响应头的落点

- `internal/config`：移除 `SESSION_SECRET`、`SESSION_TTL`；新增必需的 `JWT_SECRET`（少于 32 字符启动失败，错误信息只说明原因、不回显值）和可选的 `JWT_TTL`（默认 24h）。旧 `.env` 只有 `SESSION_SECRET` 时，启动会明确报缺少 `JWT_SECRET`。
- `Cache-Control: no-store`：在 Go 的 `/api` 路由外层加一个响应头中间件（覆盖登录、业务与所有错误响应，也覆盖本地开发的直连场景）。这同时堵住了迭代 1 验证时观察到的"`http.ServeContent` 带 `Last-Modified`，浏览器缓存后在登出后仍能重放 200"的问题——现在缓存键不含 `Authorization`，更需要显式禁用缓存。
- CSP：写在 `frontend/nginx.conf` 的 server 级 `add_header ... always`，子 `location` 内不再定义 `add_header`（避免 nginx 的继承覆盖）。

### Decision 8：验收方式

- Go 单元测试覆盖 token 校验矩阵：正确签名通过；他钥签名、篡改载荷、`alg=none`、`HS512`、过期、`iss` 不符、缺 `jti/exp/role/class_id`、已吊销、query / Cookie 携带，全部得到 401；使用可注入的时钟避免真实等待。
- curl 层用几行脚本手工构造伪造 token（HMAC 签名）验证同样的矩阵，并验证 DB 停止时得 503 且恢复后同一 token 仍可用。
- 前端在浏览器中实测：Authorization 头、刷新保持、401 回登录页、登出后回退、Blob 下载、CSP 下无违规报告。

## Risks / Trade-offs

- **[XSS 可窃取 token]** 相比 HttpOnly Cookie 是实质性倒退 → `sessionStorage` 缩短暴露窗口、CSP 禁内联脚本、Markdown 不渲染原始 HTML、24h 过期 + 登出吊销；仍无法保证"永不失守"，已在 spec 中落成可验收的防护头。
- **[`JWT_SECRET` 泄露的后果更大]** 会话方案下密钥泄露无法伪造身份，JWT 下泄露即可伪造任意班级的教师 → 仅环境变量、≥32 字符、不入日志、不入仓库；泄露时更换密钥即让所有 token 失效（所有人重新登录）。
- **[身份在 24h 内不随数据库变化]** 角色 / 班级变更不会立即生效，被删除用户的 token 到期前仍有效 → 当前系统没有这些功能；日后引入时需要在中间件加"用户仍存在且角色 / 班级一致"的校验，或缩短有效期。
- **[多个有效 token 并存]** 重新登录不踢掉旧 token → 这是"不记录签发历史"的必然结果，已在 spec 中改写场景；需要时另开变更做 `tokens_valid_after`。
- **[每个请求仍查一次库]** 吊销校验依赖 DB，DB 不可用时业务全部 503 → 与迭代 1 的会话查询同等代价，行为一致。
- **[标签页独立登录]** `sessionStorage` 使新标签页需重新登录 → 可接受，换取更小暴露面。
- **[启动期建表与未来迁移执行器并存]** → 语句幂等；执行器落地后并入迁移即可。
- **[CSP 误伤]** 严格策略可能阻止意外的内联样式 / 资源 → 用真实浏览器走完整流程并检查控制台；有问题时收窄策略而不是加 `unsafe-inline`。
- **[迭代 1 文档过期]** `docs/iteration-1.md` 中的 Cookie 命令与"否决 JWT"的决策将与现状不符 → 保留为历史记录，新增说明文档指向本变更，README 的 curl 示例改用 Bearer。

## Migration Plan

1. 先按迭代 1 流程归档 `add-auth-rbac-class-knowledge`（否则 `auth-upload` 主规格不存在，本 change 的 MODIFIED 无法归档）。
2. 在 `.env` 中增加 `JWT_SECRET`（≥32 字符随机串）与可选 `JWT_TTL`，删除 `SESSION_SECRET`、`SESSION_TTL`。
3. `docker compose up --build -d`。api 启动时幂等建立 `revoked_tokens`；发布后所有人需重新登录（旧 Cookie 被忽略，前端无 token 直接进入登录页）。
4. **回滚**：回退代码与镜像即可；`sessions` 表未被改动，旧版本可直接恢复 Cookie 登录；`revoked_tokens` 留在库中无害。

## Open Questions

- 何时删除废弃的 `sessions` 表？（需要迁移执行器，不影响本变更行为，后续再定。）
- 未来是否需要刷新令牌或缩短 `JWT_TTL`？（当前明确不做，见 Non-goals。）
