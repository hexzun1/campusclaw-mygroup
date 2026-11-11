# Proposal

## Why

一次对抗性评审发现四个已存在于当前代码中的缺陷：MCP 撤权遗漏本轮后续调用、反向代理后的登录限流误伤其他客户端、并发重建索引残留旧向量、SSE 提前结束使页面停留在 pending。本变更补全既有承诺的验收边界，先提交规约供评审，不代表这些缺陷已修复或实现已获批准。

## What Changes

- **MCP（high）**：`backend/internal/tutor/toolstage.go:463–472` 在模型生成调用后直接取令牌并调用远端。触发顺序为 Resolve 收入候选 → 等待模型 → 教师断开服务器 → runCall 仍调用；根因是候选快照被当作持续授权，`backend/internal/db/mcp.go:155–166` 的 `GetServerToken` 只查编号与班级。补充每次调用准入时的服务端状态复核，断开后的本轮后续调用以 `denied/not_connected` 拒绝，区别于候选外调用与远端失败。
- **登录限流（high）**：`backend/internal/auth/handlers.go:47–52` 用 `RemoteAddr` 构造 key；生产 Nginx 已覆盖 `X-Real-IP`，但后端未使用。攻击者对受害者用户名输错口令 → 累积在 Nginx 的 IP 上 → 另一客户端的正确登录被封禁默认 300 秒。补充可信代理边界与真实客户端 IP 的取值规则，保留“用户名 + IP”的阈值、时长与统一失败提示。
- **材料索引（medium）**：`backend/internal/materials/reindex.go:92–104` 删除旧向量、替换切片时未持有 `backend/internal/index/index.go:103–105` 的索引锁。两个请求可先后删除向量，随后 A 读出旧代切片、B 替换切片、A 再写入失效 ID 的向量；回表虽过滤孤儿点，它们仍会占据向量候选名额。补充按材料串行化整个重建过程及与普通索引共用互斥的约束。
- **SSE（medium）**：`frontend/src/api/client.ts:517–522` 在 reader 正常 EOF 后仅执行 `parser.end()`，不检查是否收到终止事件。`meta → 部分 delta → EOF` 因而成功返回且不触发 `onDone/onError`；`frontend/src/pages/AssistantPage.tsx:166–191` 的 finally 只清理 controller，本轮仍为 pending。补充终止状态判定、缺失终止事件的错误路径与页面收尾。

### 组织方式与既有规约的关系

采用一个变更目录、四个既有能力的 delta。`2026-10-26-add-mcp-tools-and-api-tests` 与 `2026-10-16-add-tutor-agents-skills-streaming` 均以单变更组织多个能力；本次四项来自同一次评审，集中评审并按能力验收更符合该习惯。沿用归档 `2026-10-27-fix-mcp-review-findings` 的 proposal/design 标题、`MODIFIED Requirements` 与分组编号任务格式；`legacy` 用于区别前次已归档修复，不表示本次引入缺陷。

四项均已有相关 Requirement，全部采用 **MODIFIED**，不新建重复能力或同义 Requirement。每个 delta 标注基线与差异，完整保留被修改 Requirement 的既有场景，并补齐 Given；未被修改的主规约继续适用，本次不同步主规约或改写归档记录。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `mcp-tools`：「逐班授权与令牌保护」。已有“断开即失效”，补齐候选形成后、同轮多次调用之间和跨轮撤权；「工具调用循环」「调用审计」的计数、轮次、降级与脱敏规则继续适用。
- `auth-upload`：「登录与会话」。已有“连续失败触发限流”，补齐可信 `X-Real-IP`、伪造头、客户端隔离、地址规范化与非法代理头的处理。
- `knowledge-retrieval`：「索引状态与重建」。已有删旧向量再替换切片的顺序，补齐全流程互斥、普通索引竞争、失败收尾与最终集合一致性。
- `tutor-chat`：「流式响应协议」。已有 `done/error` 与助手页错误展示，补齐客户端终止判定、EOF/坏终止数据、一次性通知、取消与 pending 收尾；不另建 SSE 能力。

## Impact

- 后续实现涉及后端 `internal/tutor`、`internal/db`、`internal/auth`、`internal/config`、`internal/materials`、`internal/index`，前端 `src/api/client.ts` 与必要的助手页收尾逻辑，以及相邻单元/集成测试。
- 代理信任列表拟由服务端环境变量 `TRUSTED_PROXY_CIDRS` 配置，默认空列表；实施部署前须确定仅代表受控 Nginx 的来源范围。当前 `frontend/nginx.conf` 已覆盖登录路径的 `X-Real-IP`，现有生产 api 不映射宿主端口的约定继续适用。
- 不变更数据库结构、JWT 格式、MCP/SSE 事件字段或增加第三方依赖。兼容性收紧：来自已信任代理但缺失/非法 `X-Real-IP` 的登录请求返回 400；缺少有效 SSE 终止事件的响应不再按完成处理。
- **本次交付范围**仅为此目录中的 Markdown 规约文件；不修改源码、配置、迁移、依赖，不执行构建、迁移、部署、提交、apply 或 archive，任务全部保持未勾选。

## Non-goals

- MCP：不撤销已准入在途调用已经产生的远端副作用，不改变工具预算、协议、令牌存储方式，不加入新审计状态或 reason；不把前端隐藏入口当作授权。
- 登录：不增加验证码、账号全局封禁、设备指纹或分布式限流；不区分共享同一出口 IP 的不同设备，不解析多级 `X-Forwarded-For` 链。
- 索引：不实现跨进程分布式锁、MySQL/Qdrant 分布式事务、无停顿切换或历史全库孤儿向量清理；不改检索排序与召回算法。
- SSE：不新增自动重试、断点续传或会话恢复，不由前端推断服务器是否已落库，不修改 `/api/ask` 或上游模型协议。
