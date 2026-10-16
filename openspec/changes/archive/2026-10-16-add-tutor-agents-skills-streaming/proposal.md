## Why

前几次迭代已经把登录、角色、班级隔离、上传入库与本班混合检索做扎实，但对学生只有"检索 + 一问一答"：没有可配置的助教角色，没有学科引导方式，也没有多轮会话和逐字输出。本变更（迭代 3）在不重做既有能力的前提下增量加入：教师可配置的**智能体**、按适用条件命中的**技能**、带历史与引用的**流式会话**。

同时，现行 JWT 把 `role`、`class_id` 写进载荷，导致在数据库里调整用户的角色或班级后，旧 token 在 24 小时内仍按旧值授权，删除用户也不能立即生效。智能体与会话都以"登录用户的班级"为边界，这个延迟会直接变成越权窗口，因此**先**把身份来源改为"每次请求按 token 的 `sub` 回库读取"。

## What Changes

- **身份来源调整（先做，MODIFIED `auth-upload`）**
  - **BREAKING**：JWT 载荷只保留 `sub`（用户 ID）、`jti`、`iat`、`exp`、`iss=campusclaw-api`；不再写 `user_id`、`username`、`role`、`class_id`。旧格式 token 一律 401，前端清除后重新登录，不做兼容。
  - 校验顺序：取 Bearer → 验签与有效期（不查库，失败 401）→ 查吊销表（已吊销 401，库错 503）→ 按 `sub` 回库读用户并连 `classes` 取角色与班级（查无此人 401，库错 503）→ 写入请求上下文。角色与班级**只**来自这一步的数据库结果。
  - 效果：改库里的 `role` / `class_id` 下一次请求即生效；删除用户后其 token 立即 401。
  - 换发：登录请求若携带尚未过期的有效旧 token，登录成功时先吊销旧 `jti` 再签发新 token；前端登录时携带手中已有的 token。
  - `POST /api/login` 响应体、`GET /api/me` 字段、限流、统一 401 文案、只认 `Authorization` 头等保持不变。
- **新增能力 `tutor-chat`**
  - 存储：`tutor_agents`、`tutor_agent_skills`、`tutor_agent_materials`、`skills`、`conversations`、`messages` 六张表，幂等建表；预置 A 班「数学解题引导」「语文解题引导」两份技能与已启用的默认智能体，按 `seed_key` 幂等。
  - 技能文件：单个纯文本（键行 + 空行 + 说明正文），严格校验，错误以面向教师的可读文字返回 400。
  - 接口：`/api/agents`、`/api/skills`、`/api/conversations`、`/api/conversations/{id}/stream`（SSE）。班级一律取自登录用户；教师接口对学生 403；他人、他班、未启用或不存在的对象统一 404。
  - 作答链路：会话锁定智能体 → 绑定且启用的技能中做**词法路由**（至多一个）→ 只在智能体绑定的材料内做 hybrid 检索、取前 4 条 → 按技能的资料范围过滤并连续编号 → 推 `meta` → 组装消息（固定顺序的系统消息 + 带编号资料的用户消息 + 最近 6 条历史）→ 流式生成推 `delta` → 落库推 `done`。
  - 与 `/api/ask` 口径不同：会话**即使检索落空也调用模型**作答（引用为空、剥掉所有 `[n]`）；`/api/ask` 维持"无依据返回固定文案、不调模型"。两者写成两条独立的 Requirement。
  - SSE：`meta → delta×n → done`，失败发 `error`；专用响应头与逐帧 Flush；第一个事件写出前的失败返回普通 JSON 错误；Nginx 对该路径关闭缓冲、读超时放宽到 120 秒；对话网关在该路径上改用 `stream: true`。
  - 接口文档：用处理器上的代码注解生成 OpenAPI，挂载 `/swagger` 页面，可携带 Bearer 试调主要接口；文档路由先于业务路由注册。
  - 前端：所有登录用户可用的「助手」页（会话列表、新建会话选智能体、逐段显示、出处与命中技能、出处可点开材料详情）；教师专用的「智能体管理」「技能管理」页。
- **对既有规约的其它修改（MODIFIED）**
  - `knowledge-retrieval`「混合检索」：增加"由服务端限定在一组本班材料内检索"的用法，供智能体会话使用；对外的 `/api/search` 行为不变。
  - `knowledge-retrieval`「带引用的问答」：明确只约束 `POST /api/ask`，内容不变；会话的作答口径由 `tutor-chat` 单独规定。
  - `auth-upload`「浏览器端防护响应头」：SSE 响应改用 `Cache-Control: no-cache`；`/swagger` 页面同样处于内容安全策略之下。
  - `knowledge-qa-ui`「页面导航与访问控制」：导航由三个标签扩展为「材料」「知识检索」「知识问答」「助手」，教师另见「智能体管理」「技能管理」。

## Capabilities

### New Capabilities

- `tutor-chat`：智能体、技能（文件格式、管理、路由）、会话与消息、单次回答的编排与消息组装、会话作答口径、SSE 流式协议、接口文档、助手页与教师管理页。

### Modified Capabilities

- `auth-upload`：「登录与会话」「角色权限」「班级隔离」「前端页面行为」「浏览器端防护响应头」。
- `knowledge-retrieval`：「混合检索」「带引用的问答」。
- `knowledge-qa-ui`：「页面导航与访问控制」（新增导航标签，原标签与行为不变）。

## Impact

- **后端**：`internal/auth`（签发 / 解析 / 中间件改为回库取身份、登录换发）、`internal/db`（按 ID 取用户连班级、六张新表的读写）、新增 `internal/tutor`（技能解析、路由、编排、消息组装、引用标注过滤）、`internal/search`（限定材料集合的 hybrid）、`internal/vector`（按材料集合过滤）、`internal/gateway`（流式对话）、`cmd/stubgateway`（流式响应）、`cmd/server`（路由与 Swagger）、新增迁移 `003_tutor.sql`、种子扩展。
- **新依赖**：`github.com/swaggo/swag`（注解生成文档，CLI 固定版本）、`github.com/swaggo/files/v2`（Swagger UI 静态资源）。生成的 `backend/docs/` 随仓库提交。
- **前端**：新增助手页、智能体管理页、技能管理页、SSE 帧解析模块；导航扩展；材料详情抽成可复用组件；登录请求携带已有 token。
- **部署**：`frontend/nginx.conf` 增加 SSE 路径与 `/swagger/` 的反代；Compose 服务不变。
- **兼容性**：发布后所有已签发 token 失效，所有用户需重新登录；`/api/search`、`/api/ask`、材料相关接口的请求与响应不变。
- **文档**：README、迭代说明、同伴交叉验证记录。

## Non-goals（非目标）

- 多技能同时注入；由模型自主选择技能；语义路由（向量 / 模型判断）。
- MCP 或任何真实工具调用；不向模型注入工具定义，技能的「工具」字段只做校验与展示。
- WebSocket；会话删除与重命名；历史摘要压缩。
- 跨班共享智能体或技能；技能目录形式的 `SKILL.md`。
- 刷新令牌；改用 HttpOnly Cookie 存放 token；RS256 等非对称签名。
- 重做登录、角色校验、上传入库、本班混合检索；改变 `/api/ask` 的口径。
