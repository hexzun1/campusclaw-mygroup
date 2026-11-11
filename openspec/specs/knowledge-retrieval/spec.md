# knowledge-retrieval Specification

## Purpose

在班级边界之内，让教师与学生按内容检索本班知识库，并让每条结果都能追溯到具体材料的具体片段；在同一范围内提供带引用的问答，且任何路径都不越过班级隔离。

## Requirements

### Requirement: 检索的权限与班级范围

已登录的教师与学生 MUST 都能检索、提问本班材料；上传与重建索引仍 MUST 仅限教师。班级 MUST 只取自服务端会话，请求中携带的 `class_id`（query、Header、请求体）MUST 被忽略。检索到的内容 MUST 只属于会话班级；他班内容检索不到时 MUST 表现为 200 的空结果，而不是 403 或 404。

#### Scenario: 学生与教师均可检索本班

- **WHEN** student_a1 与 teacher_a 分别携带有效会话调用检索接口，查询 A 班材料中存在的内容
- **THEN** 两者 MUST 都得到 200 且 `hits` 非空
- **AND** 每条命中 MUST 属于班级 A

#### Scenario: 未登录不能检索或提问

- **WHEN** 不带有效会话调用检索接口或问答接口
- **THEN** 响应 MUST 为 401，响应体 MUST NOT 包含任何材料标题、正文或摘录

#### Scenario: 篡改班级参数无效

- **WHEN** student_a1 调用检索接口，并在 query、Header 或请求体中附带班级 B 的 ID
- **THEN** 返回结果 MUST 与不带该参数时相同，只含班级 A 的命中

#### Scenario: 他班内容检索不到

- **WHEN** student_a1 用只出现在 B 班材料里的关键词，以 `keyword`、`vector`、`hybrid` 三种模式各检索一次
- **THEN** 每次响应 MUST 为 200 且 `hits` 为空
- **AND** 响应 MUST NOT 与"该内容是否存在于其他班级"有任何可区分的差异

### Requirement: 切片与向量索引

教师上传材料成功后，服务端 MUST 把知识库正文切成切片写入 MySQL，每个切片 MUST 记录切片文本、从 0 开始的序号、在正文中的起止字符偏移、所属班级与索引状态；随后 MUST 为切片生成向量并写入向量库集合 `campusclaw_chunks`（余弦距离）。向量的主键 MUST 等于该切片在 MySQL 中的主键；向量的 payload MUST 只包含 `class_id`、`material_id`、`knowledge_entry_id`、`chunk_id`、`chunk_index`，MUST NOT 包含正文。字符偏移按 Unicode 字符计数（不是字节），区间为左闭右开。

#### Scenario: 上传后切片入库并可回溯

- **WHEN** teacher_a 上传一份 2000 个字符的 `.md`（未指定切分参数）
- **THEN** MySQL 中 MUST 出现该材料的切片，每个切片带有序号、起止字符偏移、班级 A 与索引状态
- **AND** 未启用任何预处理时，切片文本 MUST 等于正文中 `[起, 止)` 区间内的字符

#### Scenario: 向量主键与 payload 约束

- **WHEN** 切片被成功写入向量库
- **THEN** 向量库中对应点的 ID MUST 等于该切片的 MySQL 主键
- **AND** 其 payload 的键集合 MUST 恰好为 `class_id`、`material_id`、`knowledge_entry_id`、`chunk_id`、`chunk_index`
- **AND** payload MUST NOT 含有正文或摘录

#### Scenario: 嵌入失败时材料保留

- **WHEN** teacher_a 上传合法文件，但嵌入网关或向量库在索引时失败
- **THEN** 上传响应 MUST 仍为 201，材料与切片 MUST 保留在 MySQL
- **AND** 受影响切片的索引状态 MUST 为 `failed`
- **AND** 该材料 MUST 仍可通过 `keyword` 模式检索到

### Requirement: 切分策略

系统 MUST 支持三种切分策略。`auto`（默认）：每片 800 个字符、相邻重叠 80 个字符。`custom`：每片 100–2000 个字符，重叠为片长的 0–50%，可选移除 URL、移除邮箱、折叠连续空白。`hierarchy`：按 Markdown 标题切分，切片 MUST NOT 跨越标题边界；标题之前的内容自成一片，超过 800 个字符的章节 MUST 再按 `auto` 规则继续切分。参数不合法（未知策略、片长或重叠越界、非布尔开关）MUST 返回 400，且 MUST NOT 保存材料、切片或文件。预处理 MUST 只作用于切片文本，MUST NOT 修改知识库正文；切片的字符区间 MUST 始终指向未经预处理的正文。预处理后为空的切片 MUST 被丢弃。

#### Scenario: auto 策略的切片边界

- **WHEN** teacher_a 上传一份恰好 2000 个字符的 `.txt`，不指定切分参数
- **THEN** 切片区间 MUST 依次为 `[0,800)`、`[720,1520)`、`[1440,2000)`
- **AND** 序号 MUST 依次为 0、1、2

#### Scenario: custom 策略的片长与重叠

- **WHEN** teacher_a 以 `custom`、片长 100、重叠 10% 上传一份 300 个字符的 `.txt`
- **THEN** 相邻切片的起点间隔 MUST 为 90 个字符，每个切片长度 MUST NOT 超过 100 个字符

#### Scenario: custom 参数越界被拒绝

- **WHEN** teacher_a 以片长 50、或重叠 60%、或未知策略名上传合法文件
- **THEN** 每次响应 MUST 为 400
- **AND** materials、knowledge_entries、knowledge_chunks 三表行数与上传目录 MUST 保持不变

#### Scenario: 预处理不改正文

- **WHEN** teacher_a 以 `custom`、开启"移除 URL"和"折叠空白"上传一份含 URL 与连续空白的 `.md`
- **THEN** 切片文本 MUST 不含 URL 且连续空白 MUST 被折叠
- **AND** 知识库正文 MUST 与上传文件内容逐字一致
- **AND** 切片的字符区间 MUST 指向原始正文中的对应位置

#### Scenario: hierarchy 策略不跨标题

- **WHEN** teacher_a 以 `hierarchy` 上传含两个一级标题、各带若干段落的 `.md`
- **THEN** 每个切片 MUST 只属于一个标题章节
- **AND** 没有任何切片的区间同时覆盖两个标题行

### Requirement: 索引状态与重建

每个切片 MUST 有索引状态：`pending`（已入库、尚未完成向量写入）、`indexed`（向量已写入）、`failed`（嵌入或向量写入失败）。材料详情 MUST 返回该材料的聚合索引状态（任一切片 `failed` 则为 `failed`，否则任一切片 `pending` 则为 `pending`，否则为 `indexed`）与切片数量。教师 MUST 能对本班某份材料按新的切分策略重建索引；重建 MUST 先删除该材料的旧向量，再删除旧切片，然后生成新切片并索引。学生调用重建 MUST 返回 403（在读取请求体之前）；跨班或不存在的材料 MUST 返回同样的 404。

在既有单 API 实例部署范围内，对同一材料的重建 MUST 按材料串行执行；互斥范围 MUST 从删除旧向量之前开始，持续覆盖切片事务替换、新向量全部写入、索引状态更新、失败标记与本次响应统计读取，完成这些步骤后才可允许下一次修改。同一材料的普通索引（含上传后的索引与补偿索引）MUST 遵守相同互斥边界，MUST NOT 在重建删除或替换后写回旧代切片的向量。不同材料 MUST NOT 因此共用覆盖远端 I/O 的全局锁。材料身份和班级 MUST 由服务端校验，MUST NOT 由请求携带的班级值选择操作范围。

同材料的并发重建均正常完成且所有依赖成功后，该材料在向量库中的点 ID 集合 MUST 恰等于最后一个进入临界区的重建所提交的当前 MySQL 切片 ID 集合，数量 MUST 一致，payload 的班级与材料 MUST 匹配，MUST NOT 有早先版本残留或缺失当前版本向量；当前切片 MUST 全部为 `indexed`。最后一个指串行执行顺序，MUST NOT 以请求到达或响应返回顺序假定胜者。每个成功请求的响应统计 MUST 对应该请求完成时的版本，不得混入下一请求的策略或切片数量。

所有成功、失败、超时和取消路径 MUST 释放其互斥资格；等待期间被取消的请求 MUST 在取得执行资格后再次检查取消状态，MUST NOT 开始删除/替换操作。删除向量失败时 MUST 返回 503 且不替换旧切片；替换事务失败时 MUST 回滚切片与策略并尽力标记仍存在的旧切片为 `failed` 后再释放资格；嵌入或向量写入失败 MUST 保留新切片并按既有索引规则标记受影响切片为 `failed`。失败收尾 MUST NOT 延迟到下一次重建之后修改其新切片状态，MUST NOT 回写已被下一次重建替换的向量。

#### Scenario: 详情展示索引状态

- **GIVEN** student_a1 已登录，A 班存在有切片及已知索引状态的材料
- **WHEN** student_a1 请求 A 班某份材料的详情
- **THEN** 响应 MUST 包含聚合索引状态与切片数量
- **AND** 响应 MUST NOT 含向量、向量库地址或服务器路径

#### Scenario: 按新策略重建

- **GIVEN** teacher_a 已登录，本班有正文超过 800 字且以 auto 完成索引的材料，所有依赖可用
- **WHEN** teacher_a 对已用 `auto` 索引的材料以 `custom`、片长 200 请求重建
- **THEN** 该材料旧的切片与旧向量 MUST 被删除，不再出现在检索中
- **AND** 新切片 MUST 按 `custom` 规则生成并索引
- **AND** 知识库正文 MUST 保持不变

#### Scenario: 用重建修复失败的索引

- **GIVEN** teacher_a 已登录，本班有因嵌入失败而标记 failed 的材料，向量库可用
- **WHEN** 某材料的切片为 `failed`，嵌入网关恢复后 teacher_a 对其请求重建
- **THEN** 新切片 MUST 变为 `indexed`，聚合索引状态 MUST 为 `indexed`

#### Scenario: 向量库不可用时重建不破坏现状

- **GIVEN** teacher_a 已登录，本班材料的正文、切片与策略已记录为基线，删除向量请求被模拟为失败且未执行删除
- **WHEN** 向量库不可用时 teacher_a 请求重建
- **THEN** 响应 MUST 为 503
- **AND** 该材料原有的切片与正文 MUST 保持不变

#### Scenario: 学生与跨班不能重建

- **GIVEN** student_a1 与 teacher_a 已登录，A、B 两班均有已索引材料且已有基线快照
- **WHEN** student_a1 请求重建 A 班材料，或 teacher_a 请求重建 B 班材料
- **THEN** 前者 MUST 返回 403，后者 MUST 返回与不存在的材料完全相同的 404
- **AND** 两种情况下切片与向量均 MUST 保持不变

#### Scenario: 并发重建不留下旧代向量

- **GIVEN** 本班材料 M 已索引，A 使用 custom 片长 200、B 使用 custom 片长 400，所有依赖成功；桩在 A 已读出新切片且即将写向量时设屏障
- **WHEN** A 持有执行资格并暂停时启动 B，在确认 B 已尝试重建后释放 A，等待 A、B 都结束
- **THEN** A 暂停期间 B MUST 尚未删除向量或替换切片；最终策略 MUST 为 B 的 custom 400，两个响应的切分策略与切片数 MUST 分别对应 A、B 自己完成时的版本
- **AND** 按 M 与班级过滤枚举全部向量点 ID 后 MUST 与当前数据库切片 ID 集合完全相等，所有 payload MUST 属于 M 与其班级，当前切片 MUST 全为 indexed
- **AND** 原版本与 A 版本的切片 ID MUST 在向量库中全部消失；使用确定性嵌入桩查询 B 中唯一片段时，vector 与 hybrid MUST 命中其当前切片，MUST NOT 被孤儿点占据候选名额而持续漏检

#### Scenario: 删除到替换之间也不能交错

- **GIVEN** A 正在重建 M，并在删除旧向量成功之后、替换切片之前暂停；B 准备对 M 重建
- **WHEN** 启动 B，确认其等待后释放 A 并等待两个请求结束
- **THEN** A 暂停期间 B 的向量删除、切片替换与索引操作次数 MUST 均为零
- **AND** 两次流程 MUST 整体串行，最终向量点集合 MUST 只对应 B 提交的切片，MUST NOT 出现“两次先删、随后交错替换和索引”的轨迹

#### Scenario: 普通索引与重建共用互斥

- **GIVEN** 普通索引任务 A 已读取 M 的 pending 切片并暂停在嵌入完成、向量写入之前；该任务分别来自上传后索引或补偿索引
- **WHEN** 教师同时请求对 M 重建为新策略 B，再释放 A
- **THEN** B MUST 等 A 的向量写入和状态收尾结束后才能删除向量及替换切片
- **AND** 完成 B 后向量 ID 集合 MUST 恰等于 B 的当前切片集合，MUST NOT 有 A 的旧 ID 被稍后写回

#### Scenario: 不同材料可独立推进且不跨班

- **GIVEN** A 班材料 M 的重建暂停于嵌入桩，A 班材料 N 与 B 班材料 P 的依赖正常，三个材料由其本班教师操作
- **WHEN** 保持 M 暂停并分别启动 N、P 的重建
- **THEN** N、P MUST 都可在释放 M 前完成，MUST NOT 等待 M 的远端 I/O
- **AND** 每次删除、替换与索引 MUST 只改变目标材料及其班级的数据，MUST NOT 删除或写入另两个材料的向量

#### Scenario: 向量删除或切片替换失败后可继续重建

- **GIVEN** A 持有 M 的重建资格，测试分别让删除向量失败或让切片替换事务失败，B 在同材料队列中等待且其依赖正常
- **WHEN** A 结束失败处理后允许 B 继续
- **THEN** A MUST 返回 503；删除失败分支的旧切片与策略 MUST 不变，事务失败分支 MUST 回滚切片与策略并在释放资格前尝试标记旧切片 failed
- **AND** B MUST 能取得资格并正常完成，最终向量集合 MUST 仅等于 B 的切片集合，B 的 indexed 状态 MUST NOT 被 A 的延迟失败标记覆盖

#### Scenario: 索引失败或超时的收尾在释放资格之前完成

- **GIVEN** A 已替换 M 的切片，测试分别在嵌入、向量写入中注入失败或超时，失败标记可被屏障暂停；B 等待同材料的重建资格
- **WHEN** A 进入失败标记阶段，再释放屏障并让 B 在依赖恢复后重建
- **THEN** B MUST 在 A 的索引工作与失败标记结束后才开始删除/替换，A 的受影响切片 MUST 被标记 failed，MUST NOT 回滚新切片
- **AND** B MUST 能正常完成且其当前切片全为 indexed，最终不存在 A 留下的向量或在 B 之后继续运行的 A 状态写入

#### Scenario: 等待重建的请求被取消不执行破坏性操作

- **GIVEN** A 持有 M 的重建资格，B 等待同一材料且尚未开始删除向量，C 随后也请求重建
- **WHEN** 取消 B 的请求上下文，再让 A 完成并允许 C 执行
- **THEN** B MUST 不执行向量删除、切片替换或索引，MUST 不阻止 C 后续成功完成
- **AND** 最终向量集合 MUST 等于 C 的当前切片集合

#### Scenario: 非法参数与伪造班级不进入重建流程

- **GIVEN** teacher_a 已登录，本班材料 M 与 B 班材料 P 都已索引，测试保存二者切片与向量快照
- **WHEN** 对 M 提交非法切分参数，再对 P 提交伪造 class_id 为 A 的重建请求
- **THEN** 前者 MUST 返回 400，后者 MUST 返回与不存在材料一致的 404，两者 MUST 不删除、替换或索引任何数据
- **AND** student_a1 以损坏请求体请求重建 M 时 MUST 在读取请求体前返回 403，MUST NOT 被改判为参数错误

### Requirement: 检索请求与结果

检索接口 MUST 接受查询文本、模式（`keyword` / `vector` / `hybrid`，缺省为 `hybrid`）与返回条数（缺省 5，范围 1–20）。查询文本去除首尾空白后为空、超过 500 个字符、模式未知或条数越界，MUST 返回 400。每条命中 MUST 包含：材料标题、材料 ID、切片 ID、切片序号、字符起止区间与摘录；摘录 MUST 取自 MySQL 中的切片文本（不取自向量库）。没有命中时 MUST 返回 200、`hits` 为空数组，并给出提示语「资料中未找到相关内容」。

#### Scenario: 命中结果可溯源

- **WHEN** student_a1 以 `hybrid` 检索 A 班材料中存在的内容
- **THEN** 每条命中 MUST 含材料标题、切片序号、字符起止区间与摘录
- **AND** 未启用预处理时，命中的摘录 MUST 是该材料详情正文中该字符区间内字符的前缀（摘录过长时可被截断）

#### Scenario: 无命中的统一表现

- **WHEN** student_a1 检索一个不存在于任何材料的查询
- **THEN** 响应 MUST 为 200，`hits` 为空数组
- **AND** 响应 MUST 含提示语「资料中未找到相关内容」

#### Scenario: 非法请求返回 400

- **WHEN** 调用检索接口时查询为空或全为空白、查询超过 500 个字符、模式为未知值、或返回条数为 0 或 21
- **THEN** 每次响应 MUST 为 400，且 MUST NOT 调用嵌入网关或向量库

### Requirement: 关键词检索

`keyword` 模式 MUST 只查询 MySQL 的全文索引（ngram 分词，适用于中文），并在 SQL 中按会话班级过滤；该模式 MUST NOT 调用嵌入网关或向量库。

#### Scenario: 关键词命中本班切片

- **WHEN** student_a1 以 `keyword` 模式检索 A 班材料中出现的中文词语
- **THEN** 响应 MUST 为 200，且命中的每个切片 MUST 属于班级 A 并包含该词语

#### Scenario: 依赖服务全部不可用时关键词仍可用

- **WHEN** 向量库与嵌入网关都不可用时 student_a1 以 `keyword` 模式检索
- **THEN** 响应 MUST 为 200，结果与依赖服务可用时相同

### Requirement: 向量检索

`vector` 模式 MUST 先为查询文本生成向量，在向量库中按会话班级过滤后做余弦相似度检索；余弦分数低于 0.35 的结果 MUST 被丢弃；保留下来的命中 MUST 回到 MySQL 取切片正文与材料标题，且回表查询 MUST 再次按会话班级过滤。向量库中不属于会话班级的点、以及在 MySQL 中已不存在的切片，MUST NOT 出现在结果里。

#### Scenario: 低于阈值的结果被丢弃

- **WHEN** student_a1 以 `vector` 模式检索与 A 班材料语义无关的内容，向量库返回的最高余弦分数低于 0.35
- **THEN** 响应 MUST 为 200，`hits` 为空数组，并含提示语「资料中未找到相关内容」

#### Scenario: 回表再次按班级过滤

- **WHEN** 向量库返回的某个点在 MySQL 中对应的切片属于班级 B，而调用者是班级 A 的用户
- **THEN** 该点 MUST 不出现在结果中
- **AND** 响应体 MUST NOT 含该切片的任何字段

#### Scenario: 已删除的切片不返回

- **WHEN** 向量库仍残留某个旧点，但其切片已在 MySQL 中被重建流程删除
- **THEN** 该点 MUST 不出现在结果中

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

### Requirement: 依赖不可用时的降级

向量库或嵌入网关不可用时，`keyword` 模式 MUST 仍然可用，`vector` 与 `hybrid` 模式 MUST 返回 503，且 MUST NOT 悄悄降级成 keyword 结果。503 响应 MUST NOT 暴露依赖服务的地址、密钥或内部错误详情，也 MUST NOT 清除或作废用户的会话。

#### Scenario: 向量库不可用

- **WHEN** 向量库不可用时 student_a1 分别以 `vector`、`hybrid`、`keyword` 检索
- **THEN** 前两者 MUST 返回 503，`keyword` MUST 返回 200
- **AND** 用户会话 MUST 仍然有效

#### Scenario: 嵌入网关不可用

- **WHEN** 嵌入网关不可用时 student_a1 分别以 `vector`、`hybrid`、`keyword` 检索
- **THEN** 前两者 MUST 返回 503，`keyword` MUST 返回 200

#### Scenario: 503 不泄露内部信息

- **WHEN** 任何检索或问答请求因依赖不可用返回 503
- **THEN** 响应体 MUST NOT 含依赖服务的主机名、端口、密钥或堆栈信息

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

### Requirement: 外部服务凭据与访问边界

嵌入网关、对话网关与向量库的地址、模型名与密钥 MUST 只来自服务端环境变量，缺失必需项时 api MUST 启动失败并输出缺失的变量名；这些服务 MUST 只由服务端调用，浏览器 MUST NOT 直接访问它们，也 MUST NOT 收到其密钥。仓库 MUST NOT 含真实密钥。

#### Scenario: 缺少必需配置时启动失败

- **WHEN** 未提供嵌入网关或对话网关的密钥等必需环境变量就启动 api
- **THEN** api MUST 启动失败，并输出缺失的变量名
- **AND** MUST NOT 使用内置默认密钥继续运行

#### Scenario: 响应与前端不含密钥

- **WHEN** 检查检索、问答、材料详情的响应，以及前端构建产物
- **THEN** 其中 MUST NOT 出现嵌入网关、对话网关或向量库的密钥

#### Scenario: 仓库只含占位值

- **WHEN** 检查已提交到 Git 的文件
- **THEN** `.env.example` MUST 只含这些新增变量的名称与占位值
- **AND** MUST NOT 存在含真实密钥的文件
