# Design

## Context

`add-mcp-tools-and-api-tests` 的实现已合入 `main`（`02cfd74`），主规约 `openspec/specs/mcp-tools/spec.md` 已归档。这次 review 涉及的代码现状：

- `backend/internal/mcp/client.go`：
  - `NewClient` 使用零值 `http.Client{}`，会跟随重定向，并读取代理环境变量。
  - `post` 发送 `Accept: application/json, text/event-stream`，但拒收非 JSON 响应。
  - `transportError` 把所有 `context.DeadlineExceeded` 都归为「连接超时」。
  - `Call` 只对错误信息做 `redact`，不处理结果文本。
  - 文件未经 gofmt。
- `backend/internal/mcp/handlers.go`：
  - `validateBaseURL` 不检查主机。
  - 令牌长度按 rune 计。
  - `filterTools` 不去重。
  - `Connect` 在 `ReplaceServerTools` 失败时直接 503，不改状态。
  - `DeleteServer` 先查引用、再删除，两步之间不加锁。
- `backend/internal/tutor/toolstage.go`：
  - `runCall` 一进入就执行 `*calls++`，`not_allowed` 也占名额。
  - 没有请求总数上限。
- `backend/internal/tutor/stream.go:304`：`InsertToolCalls(r.Context(), ...)`。
- `backend/internal/db/mcp.go`：`InsertToolCalls` 是一个事务，任一行失败整体回滚。列宽：`tool VARCHAR(64)`、`server VARCHAR(32)`。
- `backend/internal/db/tutor.go`：
  - `CreateSkill`、`UpdateSkill` 各自开事务。
  - `UpdateSkill` 先 `FOR UPDATE` 锁技能行。
  - 引用校验 `validateSkillMCPTools` 在事务之外执行。
- 测试现状：
  - `backend/internal/mcp/client_test.go` 与 `handlers_test.go` 以 `httptest`（`127.0.0.1`）充当远端。
  - apitest 登记的地址都是 `http://mcp-demo:8090`。

## Goals / Non-Goals

**Goals:**
- 10 条 review 问题逐条落到规约 Scenario，并有可执行的验证。
- 不改数据库结构，不改 `meta.tool_calls` 与审计接口的响应结构。
- 预置的 `campus-tools`（`http://mcp-demo:8090`）与现有 apitest 用例不受影响。

**Non-Goals:**
- 主机白名单、私有网段封禁、令牌加密、SSE 支持（见 proposal）。
- 重写 MCP 客户端为长连接或会话复用。

## Decisions

### D1 审计写入脱离请求取消

`runToolStage` 写审计时改用 `context.WithoutCancel(r.Context())`，再套 5 秒超时。保留请求上下文里的值，只去掉取消信号；5 秒超时防止数据库卡住时无限挂起。

工具阶段本身仍用 `r.Context()`：客户端已经走了，就不再发起新的远端调用。已经发生的调用必须入账，这正是规约要求的。

备选：用 `context.Background()`，但会丢掉上下文中的值，不采用。

### D2 审计字段截断

在 `ToolCallRecord.auditInput` 中按 rune 截断：`Tool` 截到 64，`Server` 截到 32（`VARCHAR` 按字符计，表是 utf8mb4）。`Reason` 的取值是固定常量，无需处理。

截断放在投影到审计行的那一步，`meta.tool_calls` 中的 `tool` 保持原样（前端只显示）。

备选是逐行写入、单行失败跳过，但那会把"整轮原子写入"变成"部分写入"，而且失败原因仍在。截断从根上消除失败，因此采用截断。

### D3 调用计数口径与请求总数上限

`ToolStage.Run` 维护两个计数：

- `attempts`：模型提出的调用请求数，上限 10（新常量 `defaultToolAttempts`）。
- `calls`：通过候选集检查的调用数，上限 3。

`runCall` 在确认 `candidate` 存在之后才执行 `calls++`；`not_allowed` 只计入 `attempts`。

第 11 个请求出现时：本条及本轮剩余的请求都不再逐条处理，合并追加一条记录 `{Tool: "*", Status: denied, Reason: limit, Round: round}`，然后结束工具阶段。

- 不必给剩余的 `tool_call` 补工具消息：阶段到此结束，这些消息不会再发给网关；最终生成请求只含 `BuildMessages` 的结果。
- 合并记录的 `tool` 字段用 `*`。审计中 `server` 列为空字符串，审计接口显示为 `*@`。apitest 只断言条数、`status` 与 `reason`。

### D4 结果文本去除令牌

在 `mcp.Client.Call` 中，对拼接后的结果文本执行 `redact(text, token)`。放在客户端，是因为只有它同时持有令牌与原始文本，所有调用方自动受益。随后 `toolstage` 仍按原样执行 `cleanToolText`（去控制字符、截断）。

顺序上先脱敏、后截断，避免令牌恰好被截成两半时漏掉。

### D5 Accept 头

`post` 改为 `Accept: application/json`。非 JSON 的 2xx 响应仍判为协议错误，和规约「不使用 SSE」一致。

### D6 防 SSRF：登记时字面检查 + 拨号时实际 IP 检查 + 不跟随重定向

- **地址判定**：新建 `backend/internal/mcp/target.go`，提供 `checkHost(host string) error`。
  - 先去掉方括号与端口，转小写。
  - 主机名在黑名单 `localhost`、`db`、`api`、`qdrant`、`web` 中则拒绝。
  - 是 IP 字面量且属于回环、链路本地（单播或组播）、未指定地址则拒绝。`net.IP` 的 `IsLoopback` 能识别 `::ffff:127.0.0.1` 这类 IPv4 映射地址。
- **登记**：`validateBaseURL` 调用 `checkHost`，失败返回 400「服务器地址不允许指向本机、元数据或内部服务」。
- **连接与调用**：
  - 发请求之前，`Client.post` 对已存的 `base_url` 再执行一次 `checkHost`，覆盖本变更之前已登记的旧记录。
  - `http.Transport` 使用自定义 `net.Dialer`，其 `Control` 回调拿到解析后的实际 IP 再判定一次（覆盖域名解析到内网、DNS rebinding）。
  - `Transport.Proxy` 显式设为 `nil`，否则 `Control` 检查的将是代理地址。
- **重定向**：`CheckRedirect` 返回 `http.ErrUseLastResponse`。3xx 落入现有的非 2xx 分支，返回「服务器返回 HTTP 302」，Connect 返回 502 并置 `error`。
- **测试注入**：`Client` 增加未导出字段 `allowLoopback bool`。只有同包测试（`client_test.go`、`handlers_test.go`）通过测试辅助函数 `newTestClient` 打开它，生产路径 `NewClient` 始终为 `false`。黑名单中的服务名检查不受这个字段影响，测试另写用例覆盖。
- **备选**：白名单（`MCP_ALLOWED_HOSTS`）最严格，但登记外部服务器要改 `.env` 重启；只拒绝重定向则挡不住直接探测 `qdrant:6333`。用户已选定黑名单方案。

### D7 发现结果去重

`filterTools` 用 `seen map[string]bool` 记录已收下的名称，重复的跳过并写日志，保持首次出现的顺序。

### D8 写缓存失败也置 error

`Connect` 在 `ReplaceServerTools` 返回错误时，调用 `MarkServerError(..., "工具缓存写入失败，请重试连接")`，然后返回 503。`MarkServerError` 自身失败只写日志。

返回码保持 503 而不改成 502，因为这是本服务的内部故障，不是远端的问题。

### D9 令牌按字节计长度

`n := len(body.Token)`，并把错误文案改为「令牌长度必须在 1 到 512 字节之间」。

### D10 超时文案区分阶段

`transportError(err, phase)` 增加 `phase` 参数（`phaseConnect` 或 `phaseCall`）：

- `Discover` 的全部请求，以及 `Call` 中的握手请求，传 `phaseConnect`，超时为「无法连接到服务器：连接超时」。
- `Call` 中的 `tools/call` 请求传 `phaseCall`，超时为「调用超时」。

非超时的拨号失败仍为「无法连接到服务器」，D6 中被禁止的目标为「服务器地址不允许」。

### D11 删除与保存技能互斥：固定"先服务器、后技能"的加锁顺序

- **删除**：新增 `db.DeleteServerIfUnreferenced(ctx, conn, id, classID) ([]string, error)`。在一个事务内：
  1. `SELECT ... FROM mcp_servers WHERE id=? AND class_id=? FOR UPDATE`。
  2. `SELECT name, tools_json FROM skills WHERE class_id=? FOR SHARE`。必须用加锁读：在 REPEATABLE READ 下普通读可能读到旧快照。
  3. 若有引用，返回技能名列表（handler 返回 409）；否则 `DELETE`。

  原来的 `ListSkillsReferencingServer` 加 `DeleteServer` 两步调用被替换。
- **保存**：`db.CreateSkill`、`db.UpdateSkill` 增加参数 `refs []db.SkillServerRef`（服务器名去重后按名称排序）。事务开始后、锁技能行之前，对每个服务器：
  - `SELECT id FROM mcp_servers WHERE class_id=? AND name=?`（普通读，用于取 id）。
  - `SELECT status FROM mcp_servers WHERE id=? AND class_id=? FOR SHARE`（按主键加锁读）。
  - 缺失的服务器返回 `ErrSkillServerMissing{Name}`，非 `connected` 的返回 `ErrSkillServerNotConnected{Name}`。
  - handler 把它们映射为 400，文案与 `validateSkillMCPTools` 的 ② ③ 一致。
  - **为什么不直接 `WHERE class_id=? AND name=? FOR SHARE`**：那是唯一键 `uq_mcp_servers_class_name` 上的加锁读，会对二级索引记录与主键记录都加 S 锁；删除走主键 X 锁再改二级索引，两条路径的索引加锁顺序相反，实测在并发下触发 MySQL 1213 死锁。改成"快照读取 id、再按主键加锁读"后，保存与删除、断开、写缓存都只在主键上取锁，顺序一致。快照读可能读到已删除的行，随后的主键加锁读返回零行，正好落到 `ErrSkillServerMissing`。
- **加锁顺序**：`UpdateSkill` 现在先锁技能行。调整为先锁服务器、再锁技能，与删除的顺序一致，避免"删除持服务器 X 锁等技能，保存持技能锁等服务器"的死锁。断开、写缓存（`updateServerState`、`ReplaceServerTools`）本来就先锁服务器行，天然与保存串行。
- **兜底**：事务外的 `validateSkillMCPTools` 保留，用于给出包含 ④ ⑤ 的完整错误信息。事务内只复核 ② ③。④（工具在缓存中）不在事务内复核：重新连接导致工具消失属于运行时降级范围。
- **备选**：给 `skills` 加外键。但引用存在 JSON 数组里，无法建外键，需要拆表，改动过大，不采用。

### D12 gofmt

`gofmt -l backend mcp-server` 必须输出为空，这一步写进各组的验证命令。

### D13 网关桩的 `/control` 支持 `reset` 与一次性强制调用

apitest 的 7.1 要在同一次运行里既覆盖「超长函数名截断」又覆盖「候选外调用不占名额」。两者都要求网关桩按用例指定的内容返回 `tool_calls`，而且必须能恢复默认行为：现在的 `forcedToolCalls` 只能被覆盖为另一个非空列表，`{"tool_calls":[]}` 表示"不调用工具"，没有任何请求能把指针置回 `nil`。缺 `reset` 就只有重启容器一条路，用例之间会互相污染。

- `POST /control` 增加布尔字段 `reset_tool_calls: true`：把 `forcedToolCalls` 置回 `nil`，恢复"首轮调用第一个已声明工具、带 tool 消息后直接作答"的默认行为。与其他开关一样用指针字段，未出现时不改变现状。
- 强制列表只对**紧接的第一次**工具阶段生效一次，用掉后自动回到默认行为（`one_shot_tool_calls`，缺省 `true`）。理由：一次提问会连续发两次非流式请求（第 1 轮带候选工具，第 2 轮带工具结果），若强制列表不撤销，第 2 轮会再报一批同样的调用，用例断言到的记录数就取决于轮次而不是被测口径。

### D14 apitest 中需要网关桩的用例

`apitest` 通过 `API_BASE_URL` 只连 API，此前没有任何用例直接控制网关桩。新增 `apitest/lib/stub.py`，从环境变量 `STUB_BASE_URL`（缺省 `http://127.0.0.1:8091`）取地址，提供 `set_tool_calls(calls)`、`reset_tool_calls()`、`stats()` 三个函数；桩不可达时抛 `pytest.skip`，与现有 `_skip_if_gateway_unavailable` 的口径一致。`run_all.py` 的报告里这三个用例与其他用例一样出现。

- 超长函数名截断：`/control` 强制一次 100 个字符的函数名，提问后 `GET /api/mcp/calls?limit=1` 的 `tool` 中工具名部分恰为 64 个字符、`status=denied`、`reason=not_allowed`。
- 候选外调用不占名额：`/control` 强制 2 个候选外调用 + 3 个 `formula_hint__campus-tools`，断言前 2 条 `not_allowed`、后 3 条 `ok`，且没有 `limit`。

## Risks / Trade-offs

- [黑名单不完整，私有网段内的其它主机仍可被探测] → 规约已明确不封私有网段（`mcp-demo` 就在其中）；教师是受信角色；黑名单覆盖本编排内的敏感服务与云元数据地址。
- [已登记的 `localhost` 或 `127.0.0.1` 服务器在升级后连接失败] → 连接返回 502「服务器地址不允许」，教师删除后改用服务名重新登记；在 README 的升级说明中提示。
- [`allowLoopback` 测试开关被误用到生产] → 字段不导出，`NewClient` 不暴露该参数；增加测试，断言 `NewClient` 构造的客户端拒绝 `127.0.0.1`。
- [事务内 `FOR SHARE` 增加锁等待] → 只在保存含 MCP 引用的技能与删除服务器时出现，频率极低。死锁仍可能发生时（MySQL 1213），按 503 返回，可重试。
- [请求总数上限后合并的记录 `tool` 为 `*`] → 审计可读性稍差，但避免了模型刷出无界的审计行。

## Migration Plan

无数据库迁移。部署顺序：api 镜像重建后即生效，`mcp-demo` 不变。

回滚：恢复到 `02cfd74` 的 api 镜像即可，数据兼容。

升级说明（README）：以 `localhost` 或回环地址登记的服务器需要改用服务名重新登记。
