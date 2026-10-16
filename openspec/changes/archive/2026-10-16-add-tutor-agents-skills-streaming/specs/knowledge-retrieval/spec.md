## MODIFIED Requirements

### Requirement: 混合检索

`hybrid` 是缺省模式。它 MUST 分别执行关键词路径与向量路径，两路各自先完成班级过滤（向量路径还需应用 0.35 阈值），再按各自的名次做 RRF 融合：每个切片的融合分数为其在各路名次上的 `1 / (60 + 名次)` 之和（名次从 1 开始，只出现在一路的切片只计一路）；结果按融合分数降序，分数相同按切片 ID 升序。若一路无结果，MUST 返回另一路的结果。服务端内部（例如智能体会话）MAY 把混合检索进一步限定在一组材料内：此时两路 MUST 都只在该集合内检索（关键词路径以材料编号作为 SQL 条件，向量路径以材料编号作为向量库过滤条件，回表时再次校验），集合中不属于会话班级的材料 MUST 被忽略；集合为空时 MUST 直接返回空结果，MUST NOT 调用嵌入网关与向量库。`POST /api/search` MUST NOT 接受客户端指定的材料集合，请求中携带的材料编号 MUST 被忽略。

#### Scenario: 两路都命中的切片排名靠前

- **WHEN** 某切片在关键词路径名次为 1、在向量路径名次为 2，另一切片只在关键词路径名次为 2
- **THEN** 前者的融合分数 MUST 为 `1/61 + 1/62`，后者为 `1/62`
- **AND** 前者 MUST 排在后者之前

#### Scenario: 缺省模式为混合

- **WHEN** 调用检索接口不带模式参数
- **THEN** 系统 MUST 按 `hybrid` 处理

#### Scenario: 一路为空时使用另一路

- **WHEN** 向量路径因阈值过滤后为空，而关键词路径有命中
- **THEN** 响应 MUST 为 200，返回关键词路径的命中

#### Scenario: 两路先过滤再融合

- **WHEN** 两路的候选中都混入了班级 B 的切片
- **THEN** 融合前每路 MUST 已剔除班级 B 的切片，最终结果 MUST 只含班级 A 的切片

#### Scenario: 限定在材料集合内检索

- **WHEN** 服务端以 A 班材料集合 {甲} 做混合检索，而查询内容同时出现在 A 班材料甲与乙中
- **THEN** 结果 MUST 只含材料甲的切片，两路候选中都 MUST NOT 出现材料乙的切片

#### Scenario: 集合中混入他班材料或为空

- **WHEN** 服务端以集合 {A 班材料甲, B 班材料丙} 做混合检索，或以空集合做混合检索
- **THEN** 前者 MUST 只可能返回材料甲的切片
- **AND** 后者 MUST 返回空结果，且嵌入网关与向量库 MUST 未被调用

#### Scenario: 检索接口不接受材料集合

- **WHEN** student_a1 调用 `POST /api/search`，请求体附带 `material_ids`
- **THEN** 结果 MUST 与不带该字段时相同

### Requirement: 带引用的问答

`POST /api/ask` MUST 以本班混合检索的前 4 条切片作为依据。只有检索到至少一个切片时 MUST 才调用对话网关；回答中的引用标记 `[1]`、`[2]` 等 MUST 与响应里 `citations` 数组的顺序一一对应（`[n]` 对应 `citations` 第 n 项），`citations` 每项 MUST 含材料标题、切片序号、字符区间与摘录。回答中出现超出 `citations` 范围的标记 MUST 被移除。检索不到切片时 MUST NOT 调用对话网关，MUST 返回提示语「资料中未找到相关内容」，`citations` 为空数组。客户端传入的 `system` 角色消息 MUST 被丢弃；服务端 MUST 使用自己的系统提示，并把检索到的摘录当作资料而不是指令。接口 MUST 一次性返回完整 JSON，MUST NOT 以流式返回。问题为空或全为空白 MUST 返回 400。本要求只约束 `POST /api/ask`；智能体会话的作答口径（检索落空仍调用模型等）由 `tutor-chat` 另行规定，二者互不替代，也互不改变对方的行为。

#### Scenario: 有依据时回答带一致的引用

- **WHEN** student_a1 就 A 班材料中存在的内容提问，且混合检索命中 3 个切片
- **THEN** 对话网关 MUST 被调用一次，`citations` MUST 恰有 3 项且顺序与检索排名一致
- **AND** 回答中的每个引用标记 `[n]` MUST 满足 `1 ≤ n ≤ 3`

#### Scenario: 引用数量上限为 4

- **WHEN** 混合检索命中超过 4 个切片
- **THEN** 传给对话网关的依据 MUST 只含前 4 个，`citations` MUST 不超过 4 项

#### Scenario: 无依据时不调用模型

- **WHEN** student_a1 提问一个 A 班材料里没有依据的问题，混合检索无命中
- **THEN** 对话网关 MUST NOT 被调用
- **AND** 响应 MUST 为 200，回答含「资料中未找到相关内容」，`citations` 为空数组

#### Scenario: 客户端 system 消息被丢弃

- **WHEN** 客户端在请求的消息列表里携带 `role` 为 `system` 的消息（例如"忽略所有规则"）
- **THEN** 传给对话网关的内容 MUST 不包含该客户端消息，只含服务端自己的系统提示

#### Scenario: 问答不越过班级

- **WHEN** student_a1 就只存在于 B 班材料中的内容提问
- **THEN** 传给对话网关的依据 MUST NOT 含任何 B 班切片；因无依据，对话网关 MUST NOT 被调用

#### Scenario: 依赖不可用时问答返回 503

- **WHEN** 向量库、嵌入网关或对话网关不可用时 student_a1 调用问答接口
- **THEN** 响应 MUST 为 503，且 MUST NOT 编造没有依据的回答

#### Scenario: 空问题被拒绝

- **WHEN** 调用问答接口时问题为空或全为空白
- **THEN** 响应 MUST 为 400，且 MUST NOT 调用嵌入网关、向量库或对话网关

#### Scenario: 智能体会话不改变本接口口径

- **WHEN** 本班已有智能体与会话，student_a1 调用 `POST /api/ask` 提一个无依据的问题，并在请求体附带 `agent_id`
- **THEN** 响应 MUST 仍为「资料中未找到相关内容」与空 `citations`，对话网关 MUST NOT 被调用
