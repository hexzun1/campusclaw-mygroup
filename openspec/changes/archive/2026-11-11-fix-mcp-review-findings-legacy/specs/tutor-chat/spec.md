## MODIFIED Requirements

> 差异说明：主规约已约定 meta/delta/done、流中 error 与「助手页」的错误展示；缺口是客户端正常 EOF 不等于收到终止事件。本 delta 保留八个协议场景，补齐终止判定与页面错误收尾；助手页既有展示要求继续适用，不另造 SSE Requirement。

### Requirement: 流式响应协议

`POST /api/conversations/{id}/stream` 接收 `{question}`（去除首尾空白后 2–500 个字符），以 SSE 返回。事件 MUST 依次为：`meta`（`{status:"ok", skill, citations, tool_calls?}`，最先到达）、若干 `delta`（`{text}`，每个只含新增片段）、`done`（`{message_id}`）。`tool_calls` 是可选数组：本轮涉及 MCP 工具时 MUST 出现，元素为 `{tool:"工具名@服务器名", status:"ok"|"error"|"denied", reason?, round, duration_ms}`，MUST NOT 含调用参数或结果原文；本轮未涉及 MCP 工具时 MUST 省略该字段。工具阶段 MUST 在写出 `meta` 之前完成。所有 `delta` 按序拼接 MUST 等于落库的助手消息正文；上游分多段返回时，系统 MUST 逐段推送，MUST NOT 合并成一次。响应头 MUST 为 `Content-Type: text/event-stream`、`Cache-Control: no-cache`、`Connection: keep-alive`、`X-Accel-Buffering: no`，每写一帧 MUST 立即刷出。第一个事件写出之前能判定的失败（401、404、400、同一会话已有回答在生成时的 409、数据库或检索不可用以及对话网关在产出第一段文字之前失败时的 503）MUST 以普通 JSON 错误返回；为此服务端 MUST 在得到第一段可推送的文字后才写出 200 与 `meta`。工具阶段本身的失败按 `mcp-tools` 的降级规则处理，MUST NOT 单独导致非 200 响应。事件流开始后若生成失败或客户端断开：已推送的内容 MUST 连同引用与技能名落库，失败时 MUST 补发 `error` 事件（通用文案，不含内部细节）。经 Web 入口（Nginx）访问时，该路径 MUST 不被缓冲或缓存，读超时 MUST 不短于 120 秒。对话网关在该路径上的正文生成 MUST 以流式方式调用，其密钥 MUST 只在服务端。

客户端 MUST 将流的传输结束与业务终止区分：仅在已收到 `meta` 后解析到 `message_id` 为正整数的有效 `done` 时才能成功完成并调用完成回调；有效 `error` 的 `message` MUST 为非空字符串，收到后 MUST 进入错误终态并通知错误回调。终止通知 MUST 至多发生一次；一旦进入终态，后续 delta、重复或相反的终止事件 MUST NOT 再改变正文、触发第二次终止通知或把错误改为成功。终态生效 MUST NOT 等待远端关闭连接。

事件流 HTTP 200 正常 EOF 时，客户端 MUST 先完成缓冲解析，再判断是否已收到有效终止事件。若仍无终态（包括空流、只有注释/未知事件、仅 meta、meta 加部分 delta），流式请求 MUST 以错误拒绝，MUST NOT 当作正常完成或调用完成回调；通用用户提示 MUST 为「回答生成中断，请重试」。终止数据不是 JSON、done 缺少有效 message_id、error 缺少有效 message，或在 meta 之前收到 done 时也 MUST 走此错误路径。已处理 error 后的 EOF MUST NOT 再制造第二个错误。字节分块、UTF-8 字符跨块、LF/CRLF 与最终缓冲刷新 MUST NOT 导致有效终止事件漏判；保持现有兼容行为：EOF 前尾帧 JSON 完整但没有最后空行时，缓冲解析所得的有效终止事件仍有效。

由无终止事件、无效终止数据或流读取故障产生的错误 MUST 使助手页本轮状态从 `pending` 转为 `error`，保留已显示文字、已收到的引用/技能/工具摘要并显示中断提示，释放本轮读取资源与请求登记，允许用户再次提问。客户端 MUST NOT 为这些情况伪造 message_id、补 done、自动重发问题或清除有效登录 token。用户切换会话或离开页面触发的主动取消 MUST 继续按取消处理，MUST NOT 误报缺失终止事件或更新新会话。

#### Scenario: 事件顺序与增量

- **GIVEN** 学生已登录且拥有该会话，检索、数据库及对话网关可用，网关桩可控制分段输出
- **WHEN** 学生提问且对话网关分 5 段返回回答
- **THEN** 客户端 MUST 先收到 `meta`，再收到至少 2 个 `delta`，最后收到 `done`
- **AND** 每个 `delta` MUST 只含新增片段，全部 `delta` 拼接 MUST 等于 `done` 所指消息的落库正文

#### Scenario: 响应头

- **GIVEN** 测试持有有效 Bearer token、本人会话编号和合法 question，所有依赖可用
- **WHEN** 用 `curl -N -i` 调用流式接口
- **THEN** 响应头 MUST 含 `Content-Type: text/event-stream`、`Cache-Control: no-cache`、`Connection: keep-alive`、`X-Accel-Buffering: no`

#### Scenario: 流开始前的失败是普通 JSON

- **GIVEN** 测试可分别设置各项失败条件，其余依赖正常且请求尚未收到任何 SSE 事件
- **WHEN** 分别以无 token、他人会话、1 个字的问题、向量库不可用、对话网关在首段之前失败的条件调用流式接口
- **THEN** 响应 MUST 依次为 401、404、400、503、503，`Content-Type` MUST 为 JSON 而非事件流

#### Scenario: 生成中途失败

- **GIVEN** 学生对本人会话发送合法问题，网关桩可在第二段后触发服务端能够捕获的错误
- **WHEN** 对话网关已推送 2 段后中断
- **THEN** 客户端 MUST 收到 `meta`、2 个 `delta` 与一个 `error` 事件
- **AND** 会话详情中 MUST 出现正文等于这 2 段拼接的助手消息

#### Scenario: 同一会话并发提问

- **GIVEN** 已登录用户拥有会话，第一条合法问题正在生成且尚未终止
- **WHEN** 同一会话的一次回答尚在生成时再次调用流式接口
- **THEN** 第二个请求 MUST 返回 JSON 的 409，第一个回答 MUST 不受影响

#### Scenario: 经 Web 入口逐段到达

- **GIVEN** 已登录用户拥有会话，Web 入口使用现有 Nginx 配置，网关桩分段输出并由屏障控制结束时机
- **WHEN** 经 `http://localhost:8080` 调用流式接口
- **THEN** 第一个 `delta` 到达客户端的时间 MUST 早于 `done`，事件 MUST 逐段到达而不是在结束时一次性到达

#### Scenario: meta 中的工具调用摘要

- **GIVEN** 已登录用户拥有会话，本轮技能同时引用一个已连接工具与一个已断开服务器的工具
- **WHEN** 一次提问在工具阶段成功调用 `formula_hint@campus-tools` 一次，并有一个已断开服务器的工具引用
- **THEN** `meta.tool_calls` MUST 含一条 `status:"ok"`、`round:1` 与一条 `status:"denied"`、`reason:"not_connected"` 的记录，每条含 `duration_ms`
- **AND** `meta` 仍 MUST 是第一个事件，`tool_calls` 的任何元素 MUST NOT 含参数或结果原文

#### Scenario: 无工具时省略字段

- **GIVEN** 已登录用户拥有会话，本轮命中技能不包含任何 MCP 引用，依赖可用
- **WHEN** 本轮命中的技能不含 MCP 工具
- **THEN** `meta` MUST NOT 含 `tool_calls` 字段，其余字段与事件顺序不变

#### Scenario: 正常 done 只完成一次且不等待 EOF

- **GIVEN** 已登录用户发起对话，响应为 200 SSE，依次提供合法 meta、两个 delta 与有效 done，传输在 done 后仍保持打开
- **WHEN** 客户端解析到 done
- **THEN** 完成回调 MUST 恰调用一次且参数为该 message_id，错误回调 MUST 不被调用，本轮 MUST 立即退出 pending 并完成资源清理
- **AND** 若同一缓冲区还包含重复 done、error 或额外 delta，它们 MUST 不再触发回调或更改已显示的正文

#### Scenario: meta 与部分 delta 后正常 EOF

- **GIVEN** 响应为 200 SSE，已提供含引用、技能与工具摘要的合法 meta 及两个 delta，本轮页面仍为 pending
- **WHEN** reader 正常结束且没有任何 done 或 error 事件
- **THEN** 流式请求 MUST 以「回答生成中断，请重试」的错误拒绝，完成回调 MUST 不被调用
- **AND** 页面 MUST 转为 error，保留两个片段的拼接正文与 meta 信息，显示中断提示，释放 reader 与本轮请求登记；用户再次提问 MUST 可发起新请求
- **AND** MUST NOT 自动重试本问题、伪造成功消息编号或清除登录 token

#### Scenario: 空流或仅元信息也必须结束 pending

- **GIVEN** 测试分别提供 200 SSE 的空响应、只有注释、只有未知事件、只有合法 meta 四种响应
- **WHEN** 每种响应均正常 EOF
- **THEN** 每个流式请求 MUST 以中断错误拒绝，完成回调 MUST 为零次，页面 MUST 退出 pending、显示错误并清理请求资源

#### Scenario: error 后 EOF 不重复通知

- **GIVEN** 响应包含 meta、部分 delta 和合法 error，其 message 为「回答生成中断，请重试」
- **WHEN** 客户端处理 error，随后在同一缓冲区读到重复 error、done 并到达 EOF
- **THEN** 错误回调 MUST 仅调用一次，完成回调 MUST 为零次，MUST NOT 再因 EOF 拒绝一次请求或将状态改为成功
- **AND** 页面 MUST 保留已显示内容并处于 error，清理资源而无需等待新的终止事件

#### Scenario: 缓冲尾帧与跨块终止事件保持兼容

- **GIVEN** 在合法 meta、delta 之后，测试将有效 done 或 error 帧按任意字节边界拆分，包含 CRLF 拆分与 error 中文 message 的 UTF-8 跨块，并分别保留或省略最后空行
- **WHEN** 客户端读取所有分块并完成 EOF 缓冲刷新
- **THEN** 每个输入 MUST 恰按有效 done 完成一次或按有效 error 通知一次，MUST NOT 误判为没有终止事件

#### Scenario: 截断或无效终止事件不代表成功

- **GIVEN** 已收到合法 meta 和部分 delta，测试分别追加 JSON 截断、done 缺少 message_id、message_id 为 0/负数/字符串、error 缺少 message 或 message 为空字符串的终止帧
- **WHEN** 客户端解析该帧或到达 EOF
- **THEN** 每个流式请求 MUST 以通用中断错误拒绝，完成回调 MUST 为零次，页面 MUST 保留已显示内容并从 pending 转为 error
- **AND** MUST NOT 把无效帧视为已成功终止来绕过 EOF 检查，MUST NOT 显示 JSON 异常或内部栈信息

#### Scenario: 未收到 meta 的 done 不能冒充成功

- **GIVEN** 200 SSE 响应没有 meta，但包含 message_id 为正整数的 done
- **WHEN** 客户端解析该事件
- **THEN** 请求 MUST 以通用中断错误拒绝，完成回调 MUST 不被调用，页面 MUST 退出 pending

#### Scenario: reader 抛错同样收尾

- **GIVEN** 客户端已收到 meta 与部分 delta，尚未收到终止事件，用户未主动取消
- **WHEN** reader 的下一次读取抛出网络错误
- **THEN** 请求 MUST 以中断错误拒绝，页面 MUST 转为 error 并保留内容，读取锁与本轮请求登记 MUST 释放

#### Scenario: 主动取消不误报断流或污染新会话

- **GIVEN** 会话 A 正在生成且已有部分 delta，用户切换到会话 B 或离开助手页
- **WHEN** 页面主动取消 A 的请求，随后 A 的 reader 抛出 AbortError 或结束
- **THEN** 客户端 MUST 按主动取消收尾并释放读取资源，MUST NOT 调用成功回调或展示“缺少终止事件”错误
- **AND** A 的迟到事件 MUST NOT 更新 B；B MUST 不残留 A 的 pending 或请求登记
