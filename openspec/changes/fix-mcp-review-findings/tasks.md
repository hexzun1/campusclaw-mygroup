> Apply 约定：一个 task 一轮 —— 读 task → 实现 → 对照 spec 审查 diff → 运行 verify → 通过后勾选 `[x]` → 提交。verify 未通过不得勾选，不得带入下一项。每组最后一项是该组的整体验证。
> 环境同 `add-mcp-tools-and-api-tests`：开发编排 `docker compose -f docker-compose.dev.yml up -d`：api `127.0.0.1:8081`（`$API`）、db `127.0.0.1:3307`、网关桩 `127.0.0.1:8091`（`$STUB`）、`mcp-demo` `127.0.0.1:8090`（`$MCP`）。`TOKEN` 为 teacher_a 的 Bearer，`MT=$(grep ^MCP_AUTH_TOKEN= .env | cut -d= -f2-)`。
> 证据统一记入 `docs/iteration-5.md`：命令、关键输出、"通过 / 不通过"。

## 1. 审计不丢失

- [x] 1.1 `runToolStage` 写审计改用 `context.WithoutCancel(r.Context())` 加 5 秒超时（design D1）— verify: `cd backend && go test ./internal/tutor -run Audit` 新增用例：请求上下文在工具阶段结束后被取消，`InsertToolCalls` 仍收到未取消的 context 并写入；开发栈上用 `curl --max-time` 在 `meta` 前中断一次命中 `formula_hint@campus-tools` 的提问，随后 `SELECT status FROM mcp_tool_calls ORDER BY id DESC LIMIT 1` 为 `ok`
- [x] 1.2 `auditInput` 按 rune 截断 `Tool` 到 64、`Server` 到 32（design D2）— verify: `go test ./internal/tutor -run AuditInput` 断言 100 字符的工具名被截到 64；`go test ./internal/db -run InsertToolCalls` 集成测试：同一批中一条正常、一条截断后的记录都成功落库
- [x] 1.3 本组验证 — verify: `cd backend && go vet ./... && go test ./internal/tutor/... ./internal/db/...` 通过；网关桩用 `/control` 让模型先调用 `formula_hint__campus-tools`、再调用 100 字符的函数名，`GET /api/mcp/calls?limit=2` 两条都在，后者 `tool` 的工具名部分长 64、`status=denied`、`reason=not_allowed`

## 2. 调用循环口径与结果脱敏

- [ ] 2.1 `ToolStage` 拆分 `attempts`（上限 10）与 `calls`（上限 3，候选确认后才计数）；第 11 个请求起合并为一条 `{tool:"*", denied, limit}` 并结束阶段（design D3）— verify: `go test ./internal/tutor -run ToolStage` 覆盖：「2 次候选外 + 3 次候选内」时候选内 3 次全部发往远端且无 `limit`；「4 次候选内」时第 4 次为 `limit`（原场景不变）；「25 次候选外」时记录恰 11 条，最后一条 `reason=limit`
- [ ] 2.2 `mcp.Client.Call` 对结果文本先执行 `redact(text, token)`，再交给 `cleanToolText`（design D4）— verify: `go test ./internal/mcp -run CallRedactsResult`：`httptest` 远端返回含令牌的文本，`CallResult.Text` 不含令牌且含 `***`；`go test ./internal/tutor -run ToolResultRedacted` 断言最终用户消息中无令牌
- [ ] 2.3 本组验证 — verify: `cd backend && go test ./internal/tutor/... ./internal/mcp/...` 通过；开发栈上让网关桩一次返回 25 个候选外调用，`meta.tool_calls` 长度为 11、回答正常完成，`mcp_tool_calls` 本轮新增 11 行

## 3. 客户端协议细节

- [ ] 3.1 `Accept` 改为只有 `application/json`（design D5）— verify: `go test ./internal/mcp -run AcceptHeader`，`httptest` 记录到的 `Accept` 恰为 `application/json`
- [ ] 3.2 `transportError` 增加阶段参数：握手超时为「连接超时」，`tools/call` 超时为「调用超时」（design D10）— verify: `go test ./internal/mcp -run Timeout`：远端在 `initialize` 上阻塞时 `Discover` 错误含「连接超时」；远端只在 `tools/call` 上阻塞时 `Call` 错误含「调用超时」且不含「连接超时」
- [ ] 3.3 本组验证 — verify: `gofmt -l backend mcp-server` 无输出；`cd backend && go vet ./internal/mcp && go test ./internal/mcp` 通过

## 4. 防 SSRF

- [ ] 4.1 新增 `internal/mcp/target.go` 的 `checkHost`：名称黑名单 `localhost`、`db`、`api`、`qdrant`、`web`（不区分大小写）；IP 字面量为回环、链路本地、未指定地址时拒绝；`validateBaseURL` 调用它并返回 400（design D6）— verify: `go test ./internal/mcp -run CheckHost` 表驱动覆盖 spec「被禁止的地址」中的 9 个地址均被拒、`http://[::ffff:127.0.0.1]` 被拒、`mcp-demo` 与 `10.0.0.5` 放行
- [ ] 4.2 `NewClient` 使用自定义 `http.Transport`：`Proxy=nil`、`net.Dialer.Control` 检查解析后的实际 IP、`CheckRedirect` 返回 `http.ErrUseLastResponse`；`post` 发请求前再执行一次 `checkHost`；测试专用的未导出字段 `allowLoopback` 由 `newTestClient` 打开，原有 `httptest` 用例改用它 — verify: `go test ./internal/mcp` 全部通过；新增用例 `NewClientRejectsLoopback`：生产构造的客户端连接 `httptest` 服务失败，且服务端未收到任何请求；`RedirectNotFollowed`：远端返回 302 指向第二个 `httptest`，第二个服务收到 0 次请求，`Discover` 报错
- [ ] 4.3 本组验证 — verify: 开发栈上分别以 `http://localhost:8090`、`http://169.254.169.254`、`http://qdrant:6333` 登记均得 400；以 `http://mcp-demo:8090` 登记得 201 且连接得 200；直接在库中把一台服务器的 `base_url` 改为 `http://db:3306` 后连接，得 502 且 `status=error`

## 5. 登记与发现的健壮性

- [ ] 5.1 令牌长度按 `len()` 字节计，文案改为「令牌长度必须在 1 到 512 字节之间」（design D9）— verify: `go test ./internal/mcp -run TokenBytes`；开发栈上 171 个汉字的令牌得 400、170 个汉字得 201，均非 503
- [ ] 5.2 `filterTools` 按名称去重，保留首次出现的工具并记日志（design D7）— verify: `go test ./internal/mcp -run DuplicateTools`：`httptest` 远端两次列出 `echo`，`Connect` 得 200，缓存中 `echo` 只有一条且说明为第一条的说明
- [ ] 5.3 `ReplaceServerTools` 失败时调用 `MarkServerError(..., "工具缓存写入失败，请重试连接")` 并返回 503（design D8）— verify: `go test ./internal/mcp -run ReplaceFailure`（注入失败的数据层）断言状态为 `error`、`last_error` 为该文案、响应为 503
- [ ] 5.4 本组验证 — verify: `cd backend && go test ./internal/mcp/...` 通过；swag 注解中 `POST /api/mcp/servers` 的 400 说明含「地址不允许、令牌按字节计」，`connect` 的 502/503 说明同步更新后 `swag init` 重新生成，`curl -s $API/openapi.json | python3 -c 'import sys,json;json.load(sys.stdin)'` 无错

## 6. 删除与保存技能互斥

- [ ] 6.1 新增 `db.DeleteServerIfUnreferenced`：一个事务内先 `FOR UPDATE` 锁服务器，再以 `FOR SHARE` 读本班技能，有引用返回技能名，否则删除；`DeleteServer` handler 改用它（design D11）— verify: `go test ./internal/db -run DeleteServerIfUnreferenced` 覆盖"有引用返回名单且未删除"与"无引用删除"；原有 409 与 204 的 apitest 用例（`apitest/cases/api/mcp/servers/_id_/DELETE.py`）仍通过
- [ ] 6.2 `db.CreateSkill`、`db.UpdateSkill` 增加被引用服务器名参数：事务开始后、锁技能行之前，按名称排序以 `FOR SHARE` 复核服务器存在且为 `connected`，失败返回 `ErrSkillServerMissing` 或 `ErrSkillServerNotConnected`；handler 映射为 400，文案与 `validateSkillMCPTools` 一致；`UpdateSkill` 的加锁顺序改为先服务器、后技能（design D11）— verify: `go test ./internal/db -run SkillServerLock` 集成测试：在事务外校验通过后删除服务器，再调用 `CreateSkill`，得 `ErrSkillServerMissing` 且技能未写入；`go test ./internal/tutor -run SkillSave` 断言映射后的文案与事务外校验的「服务器不存在」措辞一致
- [ ] 6.3 并发互斥 — verify: `go test ./internal/db -run DeleteVsSaveRace -count=20`：每次并发执行"删除 `campus-tools`"与"保存引用 `formula_hint@campus-tools` 的技能"，结束后断言"删除成功且保存失败"或"保存成功且删除得到引用名单"二者恰居其一，并且不存在引用已删除服务器的技能；全程无死锁错误（1213）
- [ ] 6.4 本组验证 — verify: `cd backend && go test ./internal/db/... ./internal/tutor/... ./internal/mcp/...` 通过；`python apitest/run_all.py` 中 skills 与 mcp servers 相关用例全部通过

## 7. apitest、README 与证据

- [ ] 7.1 apitest 新增用例：`apitest/cases/api/mcp/servers/POST.py` 加入被禁止的地址与多字节令牌；`apitest/cases/api/mcp/calls/GET.py` 加入超长函数名截断（需网关桩 `/control` 支持）；`apitest/cases/e2e/test_mcp_flow.py` 加入"候选外调用不占名额" — verify: `python apitest/run_all.py` 全部通过，新增用例出现在 HTML 报告中
- [ ] 7.2 README 增加升级说明：以 `localhost` 或回环地址登记的服务器需要删除后改用服务名重新登记；列出被禁止的主机 — verify: `grep -n "169.254.169.254" README.md` 有输出
- [ ] 7.3 本组验证 — verify: `docs/iteration-5.md` 记录 1.3、2.3、3.3、4.3、5.4、6.4、7.1 的命令与输出

## 8. 全量验收

- [ ] 8.1 运行 `openspec validate fix-mcp-review-findings --strict` — verify: 退出码 0 且无 error
- [ ] 8.2 全量自动化 — verify: `gofmt -l backend mcp-server` 无输出；`cd backend && go vet ./... && go test ./...`、`cd mcp-server && go test ./...`、`python apitest/run_all.py` 全部通过并记录报告文件名
- [ ] 8.3 原有同伴验证场景回归 — verify: `mcp-tools`「逐班授权与令牌保护」①–④ 按 `docs/iteration-4.md` 中的命令重跑，结果不变；对本次新增的被拒地址响应、连接失败响应与 `docker compose logs api` 检索 `$MT`，均无匹配
- [ ] 8.4 清理与机密检查 — verify: `git ls-files | grep -x .env` 无输出；对仓库检索 `$MT` 无输出
- [ ] 8.5 归档本 change（需使用者明确指示后执行 `/opsx:archive`）— verify: `openspec list` 中无本 change；`openspec/specs/mcp-tools/spec.md` 中被修改的 5 条 Requirement 已是新内容
