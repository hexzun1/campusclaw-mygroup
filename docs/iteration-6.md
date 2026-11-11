# 迭代 6 验收记录：MCP 评审问题修复（legacy 变更）

对应 OpenSpec 变更：`openspec/changes/archive/2026-11-11-fix-mcp-review-findings-legacy/`
验收日期：2026-11-11
对应主规约：`openspec/specs/{auth-upload,knowledge-retrieval,mcp-tools,tutor-chat}`（本次已同步 35 条新场景）

## 0. 环境与约定

本次验收没有复用开发编排，而是用「原生 api + 容器化依赖」搭了一套隔离环境，原因有两个：

- 登录限流按真实源地址分桶，只有 api 直接监听宿主网卡时，`127.0.0.1` 与宿主 LAN 地址才是两个不同的源 IP；Docker Desktop 的端口转发会把宿主连接折叠成 `192.168.65.1`。
- 原生运行不占用开发编排的 8080/8081/3307/8090，验收期间用户的开发栈保持可用。

| 组件 | 地址 | 说明 |
| --- | --- | --- |
| api | `http://127.0.0.1:18081`、`http://10.7.83.41:18081` | `go run ./cmd/server`，`TRUSTED_PROXY_CIDRS` 为空 |
| db | `127.0.0.1:3308` | 容器 `ccverify-db`，MySQL 8.0，api 启动时自动迁移并 seed |
| qdrant | `127.0.0.1:6334` | 容器 `ccverify-qdrant` |
| stubgateway | `http://127.0.0.1:18090` | 原生运行，确定性嵌入/对话桩 |
| mcp-demo | `http://10.7.83.41:18092` | 原生运行，`MCP_AUTH_TOKEN=mcp-test-token` |

apitest 通过环境变量接入（未写入 `.env`）：

```bash
APITEST_DB_HOST=127.0.0.1 APITEST_DB_PORT=3308
API_BASE_URL=http://127.0.0.1:18081
API_BASE_URL_ALT=http://10.7.83.41:18081   # 第二个真实源地址
STUB_BASE_URL=http://127.0.0.1:18090
MCP_SERVER_BASE_URL=http://10.7.83.41:18092
```

`API_BASE_URL_ALT`、`MCP_SERVER_BASE_URL` 是本次新增的可选配置：未配置时用例跳过或回落到 `http://mcp-demo:8090`，开发编排下的行为不变。

## 1. 结论

| 能力 | 场景数 | 通过 | 未验证 |
| --- | --- | --- | --- |
| auth-upload | 27 | 27 | 0 |
| knowledge-retrieval | 13 | 13 | 0 |
| mcp-tools | 12 | 12 | 0 |
| tutor-chat | 17 | 17 | 0 |
| 合计 | 69 | 69 | 0 |

全量命令结果：

| 命令 | 结果 |
| --- | --- |
| `go test ./...`（backend） | 全部 ok |
| `go test -race ./internal/index ./internal/materials ./internal/db` | 全部 ok |
| `python -m pytest`（apitest 全量） | 58 passed |
| `node --test src/api/client.stream.test.ts src/api/sse.test.ts src/api/client.login.test.ts` | 28 passed |
| Chrome CDP 页面脚本（`/api/client.ts` 的页面行为） | 7 项全部符合预期 |

## 2. auth-upload（27）

既有场景（1–17）由 `go test ./internal/auth ./internal/config ./internal/jwt ./internal/httpapi` 与 apitest `cases/api/**` 覆盖，本次全量重跑通过；下表只列关键断言。

| # | 场景 | 证据 | 结果 |
| --- | --- | --- | --- |
| 1 | 登录成功 | apitest `login/POST.py::test_login_success_returns_token_and_identity_matching_db`；浏览器实测登录后进入助手页 | 通过 |
| 2 | 登录失败提示统一 | apitest `test_wrong_password_returns_401_without_token`、`test_unknown_user_uses_the_same_401` | 通过 |
| 3 | 连续失败触发限流 | `auth.TestLoginLimiterThresholdAndLockBoundary` | 通过 |
| 4 | 携带旧 token 再次登录 | apitest `logout`/`login` 组；`auth` 全量 | 通过 |
| 5 | 重新登录签发新 token | 同上 | 通过 |
| 6 | 登录失败不影响旧 token | apitest 登录组 | 通过 |
| 7 | 登出后旧 token 失效 | apitest `logout` 组 + `db.EnsureRevokedTokens` | 通过 |
| 8 | 未登录访问受保护接口 | apitest 各 GET 用例的 401 断言 | 通过 |
| 9 | 签名错误的 token 被拒绝 | `jwt.Parse` 表驱动 `TestParseMatrix` | 通过 |
| 10 | 过期的 token 被拒绝 | `TestParseExpiryUsesInjectedClock` | 通过 |
| 11 | 发行方或算法不符的 token 被拒绝 | `TestParseMatrix` | 通过 |
| 12 | 载荷缺项的 token 被拒绝 | `TestIssuePayloadIsExactlyTheFiveClaims`、`TestParseMatrix` | 通过 |
| 13 | 旧格式 token 被拒绝 | `TestParseMatrix` | 通过 |
| 14 | 用户被删除后 token 立即失效 | `db.TestGetIdentity*`、`db.TestTutorConversationReadRequiresOwner` 系列 | 通过 |
| 15 | token 只认 Authorization 头 | apitest 未带头的 401 断言 | 通过 |
| 16 | 登录与健康检查无需 token | `GET /health` 200（直连与经 nginx 均实测） | 通过 |
| 17 | 校验依赖的数据库不可用时返回 503 | `db.TestGetIdentityDatabaseUnavailable` | 通过 |
| 18 | 可信代理后的两个客户端计数互不影响 | `auth.TestLoginTrustedProxyClientsKeepSeparateBuckets`；apitest `login/POST.py::test_login_lock_is_isolated_between_source_addresses`（C1 连错 5 次后正确口令仍 401，C2 正确口令 200） | 通过 |
| 19 | 第三方不能通过同名账号封禁另一客户端 | `auth.TestLoginAttackerCannotLockVictim`；同上 apitest 用例 | 通过 |
| 20 | 同一客户端的不同用户名独立 | `auth.TestLoginSameIPDifferentUsernamesAreIsolated` | 通过 |
| 21 | 直连伪造转发头不能换桶或封禁其他客户端 | `auth.TestLoginDirectClientCannotForgeForwardingHeaders`；apitest 用例中直连客户端伪造 `X-Real-IP` 仍 401 | 通过 |
| 22 | 经入口代理伪造受害者地址无效 | 真实入口双客户端实测：A(172.31.240.3) 伪造 `X-Real-IP: 172.31.240.4` 连续 5 次仍 401，B(172.31.240.4) 仍 200；apitest 直连伪造用例同向 | 通过 |
| 23 | 可信代理缺失或非法地址不会污染共享桶 | `auth.TestLoginInvalidProxyHeaderRejectedBeforeLimiterOrLookup`、`auth.TestClientIPTrustedProxyRejectsInvalidXRealIP` | 通过 |
| 24 | 等价 IP 与不同源端口不能绕过锁定 | `auth.TestLoginDifferentSourcePortsShareBucket`、`TestLoginEquivalentAddressFormsShareBucket` | 通过 |
| 25 | 成功重置与锁定过期只作用于当前桶 | `auth.TestLoginSuccessResetsOnlyItsBucketAndLockBoundary`、`auth.TestLoginLimiterResetOnlyClearsOneKey` | 通过 |
| 26 | 代理信任配置必须显式且可校验 | `config.TestLoadTrustedProxyCIDRsDefaultEmpty/Normalized/RejectsInvalidAndGlobal` | 通过 |
| 27 | 直接对端无法解析时拒绝 | `auth.TestClientIPRejectsUnparseableDirectPeer`、`TestLoginUnparseableRemoteAddrRejected` | 通过 |

## 3. knowledge-retrieval（13）

| # | 场景 | 证据 | 结果 |
| --- | --- | --- | --- |
| 1 | 详情展示索引状态 | `index.TestIndexPipelineIndexesEveryChunk`、`materials.TestIndexMaterialMarksEveryChunkIndexed`、apitest materials 组 | 通过 |
| 2 | 按新策略重建 | `materials.TestReindexHandlerRebuildsAndReportsItsOwnStats` | 通过 |
| 3 | 用重建修复失败的索引 | `index.TestReindexAfterVectorDeleteFailureCanProceed`、`TestReconcileFinishesInterruptedIndexing` | 通过 |
| 4 | 向量库不可用时重建不破坏现状 | `materials.TestReindexHandlerVectorDeleteFailureKeepsOldChunks`；集成 `TestReindexIntegrationDeleteFailureKeepsOldGeneration`（真实 Qdrant） | 通过 |
| 5 | 学生与跨班不能重建 | `materials.TestReindexRejectsStudentBeforeReadingTheBody`、`TestReindexHandlerCrossClassIsTheSameNotFound`；串联脚本实测学生/他班均 403 | 通过 |
| 6 | 并发重建不留下旧代向量 | `index.TestConcurrentReindexesLeaveOnlyTheLastGeneration`；集成 `TestReindexIntegrationConcurrentRebuildsLeaveOnlyTheLastGeneration` | 通过 |
| 7 | 删除到替换之间也不能交错 | `index.TestReindexWaitsBetweenDeleteAndReplace`；集成用例在 A 暂停于 Embed 时断言 B 未改动任何状态 | 通过 |
| 8 | 普通索引与重建共用互斥 | `index.TestIndexMaterialAndReindexShareTheMaterialLock` | 通过 |
| 9 | 不同材料可独立推进且不跨班 | `index.TestReindexesOfDifferentMaterialsProceedIndependently` | 通过 |
| 10 | 向量删除或切片替换失败后可继续重建 | `index.TestReindexAfterVectorDeleteFailureCanProceed`、`TestReindexAfterReplaceFailureCanProceed` | 通过 |
| 11 | 索引失败或超时的收尾在释放资格之前完成 | `index.TestIndexFailureMarkingCompletesBeforeLockRelease` | 通过 |
| 12 | 等待重建的请求被取消不执行破坏性操作 | `index.TestCanceledReindexWaiterDoesNoDestructiveWork`、`TestReindexWithAnAlreadyCanceledContextDoesNothing` | 通过 |
| 13 | 非法参数与伪造班级不进入重建流程 | `materials.TestReindexHandlerRejectsInvalidParamsWithoutChanges`、`index.TestReindexRechecksClassUnderTheLock` | 通过 |

## 4. mcp-tools（12）

| # | 场景 | 证据 | 结果 |
| --- | --- | --- | --- |
| 1 | ① 服务器隔离 | `db.TestMCPCrossClassReadsAreNotFound`；apitest `cases/api/mcp/servers/**` | 通过 |
| 2 | ② 令牌不回传 | `db.TestMCPListServersNeverReturnsTheToken`；apitest `assert_no_secret` 断言响应体与响应头 | 通过 |
| 3 | ③ 断开即失效 | `db.TestMCPDisconnectAndErrorClearTheCache`；e2e `test_mcp_tool_flow_and_disconnect_denies` | 通过 |
| 4 | ④ 审计只看本班 | `db.TestMCPToolCallsAreClassScoped`；串联脚本中 B 班学生访问审计接口 403 | 通过 |
| 5 | 已连接服务器的正常调用 | `db.TestGetCallCredentialReturnsCredentialForConnectedServer`（真实 MySQL）；e2e 正常调用 `status=ok`、`round=1` | 通过 |
| 6 | 模型生成调用期间断开 | e2e `test_disconnect_while_the_model_is_generating_denies_the_call`：桩暂停模型请求，暂停期间断开，释放后调用 `denied/not_connected` 且写入审计；单测 `tutor.TestToolCallDisconnectDuringGenerationIsDenied` | 通过 |
| 7 | 同批调用之间断开 | `tutor.TestToolCallDisconnectBetweenCallsOfOneBatchIsDenied` | 通过 |
| 8 | 下一工具轮次重新复核授权 | `tutor.TestToolCallNextRoundRechecksAuthorization` | 通过 |
| 9 | 过期或伪造的候选不能绕过服务端边界 | `tutor.TestToolCallUsesCredentialNotTheCandidateSnapshot`、`TestToolCallCandidateFromAnotherClassIsDenied`；`db.TestGetCallCredentialReturnsCredentialForConnectedServer` 的未缓存工具/他班分支 | 通过 |
| 10 | 授权读取故障与撤权可区分 | `tutor.TestToolCallAuthorizationReadFaultIsRemoteError` | 通过 |
| 11 | 已准入调用不向后续调用传递资格 | `tutor.TestToolCallDeniedByRevocationStillConsumesTheBudget`；`db.TestGetCallCredentialBlocksUntilDisconnectCommits`（行锁屏障，撤权先提交时不返回可用凭据） | 通过 |
| 12 | 重新连接恢复后续合法调用 | `db.TestGetCallCredentialReturnsCredentialForConnectedServer` 的重连分支 | 通过 |

## 5. tutor-chat（17）

| # | 场景 | 证据 | 结果 |
| --- | --- | --- | --- |
| 1 | 事件顺序与增量 | apitest SSE 断言（首帧 meta、末帧 done、中间只有 delta）；页面实测正文渲染并完成，逐帧粒度由 `client.stream.test.ts` 覆盖 | 通过 |
| 2 | 响应头 | apitest 流式响应头断言；真实入口实测 `Content-Type: text/event-stream` | 通过 |
| 3 | 流开始前的失败是普通 JSON | 桩 `fail_before_first_delta` 用例；页面注入 503 JSON 后显示“服务暂不可用”并保持可提交 | 通过 |
| 4 | 生成中途失败 | 桩 `abort_after_deltas` 用例：上游中断后收到 error 终态且已生成正文保留 | 通过 |
| 5 | 同一会话并发提问 | apitest 409 用例；页面注入 409 JSON 后显示服务端消息 | 通过 |
| 6 | 经 Web 入口逐段到达 | 经 nginx（`localhost:8080`）实测事件到达时刻 0 / 0.019 / 0.039 / 0.062 / 0.082 s，逐段而非一次性 | 通过 |
| 7 | meta 中的工具调用摘要 | e2e meta 断言；页面注入 meta 后渲染 `formula_hint@campus-tools · 成功 · 第 1 轮` 与 `unit_convert@campus-tools · 被拒绝（not_allowed） · 第 1 轮` | 通过 |
| 8 | 无工具时省略字段 | `tutor.TestStreamMetaToolCallsOmitEmpty`、`TestStreamMetaToolCallsCarryNoArgumentsOrResults` | 通过 |
| 9 | 正常 done 只完成一次且不等待 EOF | `client.stream.test.ts::valid done completes once and ends consumption without waiting for EOF` | 通过 |
| 10 | meta 与部分 delta 后正常 EOF | `meta and partial deltas followed by EOF reject as interrupted`；页面注入同场景后保留“已经生成的部分仍然要保留”并显示“回答生成中断，请重试”，pending 移除、可再次提交 | 通过 |
| 11 | 空流或仅元信息也必须结束 pending | `EOF without a terminal rejects` 参数化用例；页面场景同上 | 通过 |
| 12 | error 后 EOF 不重复通知 | `a valid error notifies once and EOF neither rejects nor notifies again` | 通过 |
| 13 | 缓冲尾帧与跨块终止事件保持兼容 | `a done split at a CRLF boundary with no final blank line is accepted`、`an error with a Chinese message split inside UTF-8 and at CRLF is accepted` | 通过 |
| 14 | 截断或无效终止事件不代表成功 | `invalid terminal rejects` 参数化矩阵（截断 JSON、缺字段、0/负数/字符串 message_id、空 error） | 通过 |
| 15 | 未收到 meta 的 done 不能冒充成功 | `done before meta is not accepted` | 通过 |
| 16 | reader 抛错同样收尾 | `a reader error rejects with the generic interruption` | 通过 |
| 17 | 主动取消不误报断流或污染新会话 | `an active abort stays an AbortError`；页面实测生成中切换会话：活动会话由 0 变 1，新会话无 pending、无错误、无旧问题文本，token 未清除 | 通过 |

## 6. 浏览器验收方法

无头 Chrome + CDP 驱动真实页面（React 页面、vite dev 代理到 18081）：

| 检查 | 观察结果 |
| --- | --- |
| 正常流式 | 助手页打开最近会话，提交后正文出现并完成 |
| meta 工具摘要 | 两条工具标签、状态中文、轮次均来自 meta |
| meta + 部分 delta + EOF | 正文保留、`回答生成中断，请重试`、pending 消失、输入框可用 |
| 中断后再提交 | 新问题正常完成，无残留错误 |
| JSON 401 | 跳转 `/login`，`sessionStorage` 中 token 被清除 |
| JSON 503 / 409 | 页面显示错误文本，页面保持可用 |
| 生成中切换会话 | 新会话不被旧流改写，token 保留，无自动重试 |

## 7. 受控代理来源范围（任务 2.4）

仓库交付的编排（`docker-compose.yml`）：

- 只有 `web` 映射宿主端口；`api`、`db`、`qdrant` 均不映射（api 仅 `depends_on` 内网可达）。
- `web` 固定在 `edge` 网络 `172.31.240.10`，`api` 的 `TRUSTED_PROXY_CIDRS` 默认值与 `.env.example` 示例都只写这一个地址（`172.31.240.10/32`），注释明确禁止改成整个网段。
- `frontend/nginx.conf` 只在 `/api/`、`/swagger/`、`/openapi.json` 设置 `X-Real-IP $remote_addr`；登录在 `/api/` 下，因此 api 看到的是 nginx 记录的客户端地址。

结论：**本仓库交付范围内不存在额外代理**，可信来源集合只有 web 容器这一个地址。若实际部署在 web 前面再加一层代理（例如学校统一入口），nginx 的 `$remote_addr` 会变成该代理地址，所有浏览器会落进同一个登录桶；届时需要用 `realip` 模块信任该代理，或把代理地址加入 `TRUSTED_PROXY_CIDRS`，并重新跑 `login/POST.py` 的双入口用例。

`.env.example` 中所有示例值均为占位符，无真实密钥。

## 8. 临时环境清理

- 停止并删除 `ccverify-db`、`ccverify-qdrant` 容器
- 停止原生 api / stubgateway / mcp-demo / vite / headless Chrome 进程
- 删除 `frontend/vite.config.verify.ts`
- 验收期间新建的会话与上传目录不进入仓库
