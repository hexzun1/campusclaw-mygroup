## MODIFIED Requirements

> 差异说明：主规约已要求“断开即失效”，既有场景只在断开后开始新的保存/提问；本 delta 保留四项交叉验证，补齐候选快照形成后的逐次授权、拒绝原因与在途边界，不重复建立授权 Requirement。

### Requirement: 逐班授权与令牌保护

工具服务器、工具缓存与调用审计 MUST 以会话班级为边界：他班对象 MUST 表现为不存在，审计 MUST 只返回本班记录；已登记服务器的令牌 MUST 永不回传；服务器断开后，其工具在保存与调用两条路径上 MUST 立即不可用。编号 ①–④ 的既有场景仍是同伴交叉验证的必验项。

本轮候选集 MUST NOT 作为持续有效的授权凭据。每次候选内、参数合法且未超出调用上限的工具调用，MUST 在服务端准入时按服务器编号与会话班级读取当前授权：服务器属于本班、状态为 `connected`、对应工具仍在当前发现缓存中，才可取得该次调用的凭据并进入远端调用。状态/班级/缓存校验与取凭据 MUST 基于同一一致性读取，MUST NOT 复用此前候选解析或前一次调用的授权结果。该即时检查成功为单次调用的准入点；撤权以断开事务提交为生效点。撤权先于准入时，包含同批调用列表的下一条与下一工具轮次在内的所有后续调用 MUST 被拒绝；准入先于撤权的在途调用允许结束，但其资格 MUST NOT 延续到下一次调用。

服务器已断开、为 `error`、不存在、不属于本班或工具已从缓存移除时，调用 MUST 不取得可用凭据且 MUST NOT 发起该次远端请求，MUST 记录 `status: denied`、`reason: not_connected`，并向模型返回「调用被拒绝：工具服务器未连接或工具已不可用」。数据库故障导致无法判断授权时也 MUST 不调用远端，但 MUST 记录 `status: error`、`reason: remote_error` 并返回通用失败信息，MUST NOT 假报撤权；候选集外仍为 `denied/not_allowed`。MUST NOT 向调用者泄露他班对象是否存在、令牌或数据库错误细节。

这些记录 MUST 按「调用审计」写入本班审计，并进入 `meta.tool_calls`；运行中被拒的 `round` MUST 是实际工具轮次 1 或 2，只有候选解析时拒绝才使用 0。被拒的调用仍按「工具调用循环」的候选内计数规则占用调用名额，MUST NOT 通过重试绕过预算；撤权本身 MUST NOT 使正文生成失败。明确执行教师连接操作并成功恢复授权后，新准入的调用才可恢复；连接/发现路径 MUST 仍能读取本班已断开服务器的凭据以完成重新连接。

#### Scenario: ① 服务器隔离

- **GIVEN** A 班存在已连接的 campus-tools，B 班教师已登录且本班没有同名服务器
- **WHEN** B 班教师对 A 班服务器的编号调用 `connect`、`tools`、`disconnect` 与 `DELETE`，并保存引用 `formula_hint@campus-tools` 的技能
- **THEN** 前四者 MUST 都返回与不存在的编号一致的 404，A 班服务器 MUST 无任何变化
- **AND** 保存技能 MUST 返回 400

#### Scenario: ② 令牌不回传

- **GIVEN** 教师已登记带测试令牌的本班服务器，测试可捕获接口响应及双方日志
- **WHEN** 对列表、登记、连接（含令牌错误导致的失败）、工具清单接口的响应体，以及 api 与 `mcp-demo` 的日志检索令牌原文
- **THEN** 均 MUST 检索不到

#### Scenario: ③ 断开即失效

- **GIVEN** campus-tools 已连接，A 班已有启用且绑定其工具的技能，teacher_a 已登录
- **WHEN** teacher_a 断开 `campus-tools` 后，先保存一个引用 `formula_hint@campus-tools` 的新技能，再让已绑定该工具的技能在对话中被命中
- **THEN** 保存 MUST 返回 400
- **AND** 对话的 `meta.tool_calls` 中该工具 MUST 为 `denied`、`reason: not_connected`，审计中 MUST 有对应的 denied 记录，回答 MUST 正常完成

#### Scenario: ④ 审计只看本班

- **GIVEN** A、B 两班分别有已登录教师，A 班具有可调用的本班工具
- **WHEN** A 班产生调用记录后，B 班教师调用 `GET /api/mcp/calls`
- **THEN** 结果 MUST NOT 含任何 A 班记录

#### Scenario: 已连接服务器的正常调用

- **GIVEN** A 班服务器为 connected、工具在发现缓存与本轮候选集中，模型参数合法且未达到预算，数据库与远端可用
- **WHEN** 本轮模型请求调用该工具且直到准入检查结束授权均有效
- **THEN** 远端 MUST 恰好收到一次该工具调用并使用本班服务器的凭据，结果 MUST 为 ok
- **AND** `meta.tool_calls` 与本班审计 MUST 记录实际轮次，响应、模型上下文与日志 MUST NOT 含令牌原文

#### Scenario: 模型生成调用期间断开

- **GIVEN** 本轮已把 connected 服务器的工具提供给模型，网关桩在返回工具调用前被屏障暂停，调用预算尚未用尽
- **WHEN** teacher_a 完成断开并收到成功响应后释放屏障，模型返回参数合法的候选内调用
- **THEN** 该调用 MUST 不取得可用凭据，远端握手与 tools/call 的新增请求数 MUST 都为零
- **AND** `meta.tool_calls` 与审计 MUST 含该工具的 denied/not_connected、round 为 1，模型收到可读的调用拒绝信息，MUST NOT 记为 not_allowed 或 remote_error
- **AND** 网关后续正常时，正文 MUST 继续生成并以 done 完成

#### Scenario: 同批调用之间断开

- **GIVEN** 模型在同一个工具轮次返回两个对同一 connected 服务器的合法调用，第一个已准入并完成，第二个尚未准入
- **WHEN** teacher_a 的断开事务提交后才允许处理第二个调用
- **THEN** 远端工具调用总数 MUST 恰为 1；第一条记录 MUST 为 ok，第二条 MUST 为 denied/not_connected，两条 round MUST 均为 1
- **AND** 两条记录 MUST 均进入 meta 与审计，第二条失败 MUST 作为工具消息回传，最终正文仍可完成

#### Scenario: 下一工具轮次重新复核授权

- **GIVEN** 第一工具轮次已成功调用服务器，第二轮模型响应尚未返回，仍有候选内调用预算
- **WHEN** 教师完成断开后，第二轮返回对相同候选的合法调用
- **THEN** 第二轮 MUST 不调用远端，记录 MUST 为 denied/not_connected、round 为 2，MUST NOT 复用第一轮取得的凭据或授权

#### Scenario: 过期或伪造的候选不能绕过服务端边界

- **GIVEN** 测试分别提供指向 error 状态服务器、已不存在服务器、他班服务器、connected 但工具已移出缓存的候选，各请求参数合法且在预算内
- **WHEN** 宿主处理这些候选对应的调用
- **THEN** 每个调用 MUST 不取得可用凭据且不发起远端请求，记录 MUST 为 denied/not_connected
- **AND** 他班服务器与不存在服务器的对外拒绝信息 MUST 一致，MUST NOT 泄露他班令牌或新增他班审计

#### Scenario: 授权读取故障与撤权可区分

- **GIVEN** 存在合法候选且未超过预算，测试让调用准入时的数据库读取失败
- **WHEN** 模型调用该候选工具
- **THEN** 远端新增请求数 MUST 为零，记录 MUST 为 error/remote_error，模型收到通用失败信息，MUST NOT 标成 denied/not_connected
- **AND** 数据库在写审计前恢复后，该 error 记录 MUST 与 meta 中的状态一致并写入审计

#### Scenario: 已准入调用不向后续调用传递资格

- **GIVEN** 第一次调用的即时授权检查已成功且进入远端调用，远端桩暂停其响应，第二次调用尚未准入
- **WHEN** 教师在第一次响应返回前完成断开，再释放响应并处理第二次合法调用
- **THEN** 第一次调用允许按远端实际结果完成，断开操作 MUST NOT 等待该远端响应
- **AND** 第二次 MUST 被记为 denied/not_connected 且不调用远端，MUST NOT 因第一次仍在途而继续获得授权

#### Scenario: 重新连接恢复后续合法调用

- **GIVEN** 本班服务器已断开且其工具已被拒绝，远端与数据库均可用
- **WHEN** 教师调用 connect 完成握手与发现，再开始命中该工具的新一轮对话
- **THEN** connect MUST 成功、服务器 MUST 为 connected，新的合法工具调用 MUST 正常执行并记为 ok
- **AND** 此前的 denied 审计 MUST 保留，MUST NOT 将“仅 connected 可调用”的限制错误应用到重新连接的凭据读取
