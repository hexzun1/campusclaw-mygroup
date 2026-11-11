# Tasks

> **仅实施阶段才可执行**：以下全部为待实施任务，本次仅编写规约，保持所有复选框未勾选。执行前须完成 proposal / design / specs / tasks 评审并取得用户明确的实施指令；本清单不授权 apply、archive、部署或提交。每项按相应 `specs/<能力>/spec.md` 的 Scenario 验收，未验证项不得声称通过。

## 1. MCP 每次调用的服务端授权

- [x] 1.1 在 `backend/internal/db` 增加调用专用的本班服务器/connected/工具缓存/凭据一致性读取，按 design D1 与断开、缓存替换共用服务器主键锁顺序；保留连接路径取凭据能力 — verify: 在 `mcp_integration_test.go` 覆盖「已连接服务器的正常调用」「过期或伪造的候选不能绕过服务端边界」「重新连接恢复后续合法调用」，并用事务屏障验证撤权先提交时不返回可用凭据。
- [x] 1.2 调整 `ToolCatalog` 与 `runCall` 在每次合法候选调用前执行即时准入，使用该次返回的地址/凭据；映射 denied/not_connected 与 error/remote_error，保留候选外、参数和预算规则 — verify: `toolstage_test.go` 覆盖「模型生成调用期间断开」「同批调用之间断开」「下一工具轮次重新复核授权」「授权读取故障与撤权可区分」，断言远端新增请求数、实际 round 与模型错误消息。
- [x] 1.3 补充准入后在途、重新连接与候选内拒绝的预算回归；核对被拒记录沿既有审计与 meta 路径投影且不含令牌 — verify: 屏障暂停远端响应时断开能完成、下一调用拒绝；三次候选内预算不因撤权重试扩大；在 `backend/` 执行 `go test ./internal/tutor ./internal/mcp`，相关用例通过。
- [ ] 1.4 扩展 `apitest/cases/e2e/test_mcp_flow.py` 及所需测试桩控制，在同一轮对话的模型暂停/两调用之间执行教师断开 — verify: 对照本 delta 12 个 Scenario，关联检查远端计数、SSE meta 与本班审计；重跑既有四项交叉验证，学生/他班请求仍被服务端拒绝；逐场景记录证据与未验证项。

## 2. 代理边界与登录限流隔离

- [x] 2.1 在 `internal/config` 实现 `TRUSTED_PROXY_CIDRS` 的可选配置、规范化及非法/全网列表拒绝，并把可信对端集合注入 auth — verify: `go test ./internal/config` 覆盖「代理信任配置必须显式且可校验」的所有输入；确认空列表不自动信任回环或私有网段。
- [x] 2.2 在 `internal/auth` 提取服务端客户端地址解析，按可信直接对端选择单值 X-Real-IP 或 RemoteAddr，拒绝可信代理的缺失/非法头并忽略 X-Forwarded-For/Forwarded — verify: 表驱动单测覆盖「直连伪造转发头不能换桶或封禁其他客户端」「可信代理缺失或非法地址不会污染共享桶」「等价 IP 与不同源端口不能绕过锁定」「直接对端无法解析时拒绝」，并断言失败解析不访问 limiter/口令校验。
- [x] 2.3 让 Login 的锁定、失败计数与成功清零使用同一用户名/IP key；给限流测试提供受控时钟 — verify: handler 与 limiter 测试覆盖两客户端 3+2 次失败不合并、攻击者 5 次不封禁受害者、同 IP 不同用户名隔离、成功清零及第 299/300 秒边界；锁定与错误口令仍为相同 401。
- [ ] 2.4 在实施验收记录中填写受控代理的实际来源范围及是否存在额外代理，并在后续授权范围内补充服务端配置说明与无真实值的示例 — verify: 对照 design D2/Open Questions，来源范围只包含受控入口，示例无密钥；当前 Nginx `/api/` 的覆盖头规则与生产 API 不映射宿主端口的条件都有证据。
- [ ] 2.5 在隔离测试入口准备两个不同真实源 IP，扩展 `apitest/cases/api/login/POST.py` 的代理场景，同时跑直连与入口伪造头用例 — verify: 「第三方不能通过同名账号封禁另一客户端」「经入口代理伪造受害者地址无效」均观察到 C1 锁定、C2 成功；`go test ./internal/auth ./internal/config` 与本 delta 27 个 Scenario 的相关登录/JWT 回归通过，缺少真实入口条件时明确记为未验证。

## 3. 重建索引的完整临界区

- [x] 3.1 重构 `internal/index` 的材料协调入口与内部索引核心，使普通索引、上传后索引、补偿索引和重建共用同一个按材料锁，重建不重复获取非重入锁 — verify: 用屏障测试「普通索引与重建共用互斥」「不同材料可独立推进且不跨班」，同材料不交错、其他材料可在屏障释放前完成，现有索引/补偿单测通过。
- [x] 3.2 将 `materials.Reindex` 的删除向量、切片替换、索引、状态收尾及响应统计快照放进同材料临界区，保留服务端角色/班级/参数校验 — verify: handler 测试覆盖「并发重建不留下旧代向量」「删除到替换之间也不能交错」「非法参数与伪造班级不进入重建流程」，操作日志显示 B 在 A 释放前没有破坏性操作。
- [x] 3.3 完善失败/超时/请求取消路径的锁释放与状态写入顺序，保持独立索引上下文的有界收尾；取消等待者开始执行前复核取消状态 — verify: 覆盖「向量删除或切片替换失败后可继续重建」「索引失败或超时的收尾在释放资格之前完成」「等待重建的请求被取消不执行破坏性操作」，断言后续请求成功且无前一任务延迟写入。
- [x] 3.4 增加真实 MySQL/Qdrant 的同材料重建集成用例，以确定性嵌入与屏障固定两种竞态时序 — verify: 对照当前材料/班级枚举全部点 ID，与 MySQL chunk ID 集合和数量完全一致、payload 匹配、旧代 ID 全消失；最终策略/状态与最后执行者一致，vector/hybrid 查询当前独有内容可命中。
- [x] 3.5 在 `backend/` 执行相关包 `go test -race ./internal/index ./internal/materials ./internal/db`，并在指定隔离依赖环境执行索引集成用例 — verify: 对照本 delta 13 个 Scenario 逐项记录结果，正常/失败重建、上传与补偿回归通过；需要依赖而被 skip 的测试不得算通过。

## 4. SSE 终止判定与页面收尾

- [x] 4.1 为 `streamConversation` 增加 meta/终态跟踪与终止 payload 校验，在 `parser.end()` 后检查无终止事件的 EOF，生成通用中断错误；保持有效 error 的回调语义 — verify: 新增 `frontend/src/api/client.stream.test.ts`，以可控 ReadableStream 覆盖正常 done、部分 delta 后 EOF、空流/注释/未知事件/仅 meta、error 后 EOF，断言 Promise 结果与回调次数。
- [x] 4.2 补齐终态重复/冲突守卫、终态立即结束读取、reader 故障/取消与资源清理，保留解析器尾帧兼容性 — verify: 同一缓冲区的 done/error 重复不二次通知；UTF-8/CRLF 跨块和无末尾空行的完整终止 JSON 不误判；截断/无效 payload、meta 前 done、reader 抛错走错误；主动 AbortError 不报缺终止事件。
- [ ] 4.3 核对并按需修正 `AssistantPage` 对本轮错误及取消的收尾，保持 partial answer 与 meta，移除 pending 并清理 controller — verify: 在页面注入 meta+delta+EOF，看到保留文本和中断提示、可再次提交；切换会话时旧流不得改写新会话，token 不被清除且无自动重试。
- [ ] 4.4 在 `frontend/` 沿用 Node 自带测试运行方式执行 `node --test src/api/client.stream.test.ts src/api/sse.test.ts src/api/client.login.test.ts`，并核对浏览器助手页 — verify: 对照本 delta 17 个 Scenario，记录回调/Promise、页面状态及原有逐段输出、meta 工具摘要、普通 JSON 401/503、同会话 409 的回归证据，不仅检查 parser 分帧。

## 5. 跨能力验收与交付核对

- [ ] 5.1 在实施验收环境串联“代理登录 → 本班材料重建 → 对话工具阶段断开 → SSE 正文完成/异常 EOF”流程 — verify: 同名账号的其他 IP 不受限流影响，索引点集合一致，撤权是 denied/not_connected 且审计完整，缺终止事件明确错误并解除 pending；检查服务端班级隔离与日志脱敏。
- [ ] 5.2 汇总四个 delta 的 69 个 Scenario 到实施验收记录，分别写明用例/命令、关键断言、通过/失败/未验证及环境 — verify: 每个场景均有对应证据，原有场景未丢失，没有以 skipped 代替通过；执行 `openspec validate fix-mcp-review-findings-legacy --strict --no-interactive`，交付范围与评审批准范围相符。
