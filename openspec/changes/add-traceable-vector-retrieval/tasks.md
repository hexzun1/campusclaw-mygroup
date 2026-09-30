> Apply 约定：一个 task 一轮 —— 读 task → 实现 → 对照 spec 审查 diff → 运行 verify → 通过后勾选 `[x]` → 提交。verify 未通过不得勾选，不得带入下一项。
> 开发期约定：`docker-compose.dev.yml` 仅供本地调试，api 映射到 `127.0.0.1:8081`、db 到 `127.0.0.1:3306`、qdrant 到 `127.0.0.1:6333`、网关桩到 `127.0.0.1:8090`；下文 `$API` 指 `http://localhost:8081`，`$STUB` 指 `http://localhost:8090`。所有验收都使用网关桩，不接真实模型；最后一项冒烟才用真实网关。
> 账号沿用迭代 1：teacher_a / student_a1（A 班）、student_b1（B 班），口令取自本机 `.env`。本 change 不修改前端，检索与问答均用 curl 验收。

## 1. 配置与基础设施

- [x] 1.1 扩展 `internal/config`：新增必需项 `QDRANT_URL`、`EMBEDDING_BASE_URL/API_KEY/MODEL/DIM`、`CHAT_BASE_URL/API_KEY/MODEL`，可选项 `QDRANT_API_KEY`、`EMBEDDING_BATCH_SIZE`（32）、`GATEWAY_TIMEOUT_SECONDS`（30）、`INDEX_TIMEOUT_SECONDS`（120）；`EMBEDDING_DIM` 必须是正整数 — verify: `go test ./internal/config` 覆盖"缺任一必需项报错且列出变量名""DIM 非数字报错""全部提供则通过"；不设 `EMBEDDING_API_KEY` 运行 `go run ./cmd/server` 以非 0 退出，输出含 `EMBEDDING_API_KEY`
- [x] 1.2 `.env.example` 补充上述全部变量名与占位值，README 之外不写任何真实值 — verify: `grep -c = .env.example` ≥ 29；`git ls-files | grep -x .env` 无输出
- [x] 1.3 编写开发 / 验收用确定性网关桩 `backend/cmd/stubgateway`（标准库）：`POST /embeddings` 把字符二元组哈希进 `EMBEDDING_DIM` 维并归一化，`POST /chat/completions` 按收到的资料编号回显并输出 `[n]`，`GET /stats` 返回两个接口各自的调用次数与最近一次请求体，`POST /control` 可切换"嵌入失败 / 对话失败 / 输出越界引用 [9]" — verify: `go test ./cmd/stubgateway` 断言"相同文本向量相同、维度等于 DIM、相似文本余弦 > 0.35、无关文本余弦 < 0.35"；启动后 `curl $STUB/stats` 显示计数，切换失败开关后 `/embeddings` 返回 500
- [x] 1.4 `docker-compose.yml` 增加 `qdrant`（固定版本标签、命名卷保存 `/qdrant/storage`、不写 `ports`，api 通过 `http://qdrant:6333` 访问，`depends_on` 仅要求已启动）；`docker-compose.dev.yml` 增加 `qdrant`（仅绑 `127.0.0.1:6333`）与 `stubgateway` 服务 — verify: `docker compose up -d qdrant` 后 `docker compose port qdrant 6333` 无映射、宿主机 `nc -z localhost 6333` 失败；dev 编排下 `curl -s 127.0.0.1:6333/collections` 返回 200

## 2. 迁移与数据层

- [x] 2.1 实现启动时迁移执行器：`go:embed` 内置 `migrations/*.sql`，`schema_migrations(version)` 记录已执行版本，按文件名顺序执行未记录的文件，迁移连接单独启用 `multiStatements` — verify: 在含迭代 1 数据的现有数据卷上启动 api 两次，`schema_migrations` 每个版本恰一行，`users`、`materials` 行数与启动前相同
- [x] 2.2 编写 `002_knowledge_chunks.sql`：新建 `knowledge_chunks`（含 `FULLTEXT ... WITH PARSER ngram`、`UNIQUE(knowledge_entry_id, chunk_index)`、`KEY(class_id, index_status)`，`class_id` 非空）；`knowledge_entries` 增加 `chunk_strategy`（默认 `auto`）与 `chunk_params` — verify: `SHOW CREATE TABLE knowledge_chunks` 含 `ngram` 与上述键；`SELECT DISTINCT chunk_strategy FROM knowledge_entries` 对存量行只返回 `auto`
- [x] 2.3 `internal/db` 增加切片读写：批量插入、按材料替换（事务内删旧插新并更新策略字段）、按材料列出、更新状态、聚合状态与计数、按 ID 集合取切片（**强制 `class_id` 条件并 JOIN `materials` 比对班级**） — verify: 临时测试对班级 A 传入 B 班切片 ID 集合，`GetChunksByIDs` 返回 0 行；替换操作对 A 班材料执行后旧切片 ID 全部不存在
- [x] 2.4 `internal/db` 增加关键词查询：`MATCH ... AGAINST` 自然语言模式，`WHERE class_id = ?`，取前 50；单字符查询走转义后的 `LIKE` 兜底，同样带班级条件 — verify: 临时测试对班级 A 查询只出现在 B 班预置材料里的词，返回 0 行；查询 A 班预置材料里的双字词返回 ≥ 1 行；单字符查询同样只返回 A 班行

## 3. 切分

- [x] 3.1 在 `internal/knowledge` 实现 `auto` / `custom` 窗口切分（对 `[]rune` 操作，区间为左闭右开的字符偏移） — verify: `go test ./internal/knowledge` 断言 2000 字 `auto` 得到 `[0,800) [720,1520) [1440,2000)`；片长 100、重叠 10% 的 300 字得到起点间隔 90 且每片 ≤ 100；短于窗口的文本得到单片；含中文与 emoji 的文本偏移按字符而非字节
- [x] 3.2 实现切分参数解析与校验（`strategy` ∈ auto/custom/hierarchy，缺省 auto；`chunk_size` 100–2000；`overlap_percent` 0–50；三个布尔开关只接受 true/false） — verify: 表驱动单测覆盖片长 50、重叠 60、未知策略、开关为 `maybe` 均报错，合法与缺省参数通过
- [x] 3.3 实现预处理（移除 URL → 移除邮箱 → 折叠空白，空结果切片丢弃并重新连续编号），区间仍指向原始正文 — verify: 单测样本含 URL、邮箱、连续空白：`chunk_text` 已处理，原字符串逐字未变，区间截取原文与预处理前一致，序号连续
- [x] 3.4 实现 `hierarchy`（围栏代码块内的 `#` 不算标题；标题前内容自成一片；超过 800 字的章节在其区间内按 `auto` 再切） — verify: 单测：两个一级标题得到"前言 + 两章节"的切片，无切片区间同时覆盖两个标题行；代码块里的 `# x` 不产生切分；长章节的子片偏移为全文绝对偏移

## 4. 外部服务客户端

- [x] 4.1 实现嵌入客户端（OpenAI 兼容 `/embeddings`、按 `EMBEDDING_BATCH_SIZE` 分批、超时、校验返回维度，网络 / 5xx / 维度不符统一归为"依赖不可用"错误，不把地址与密钥写进错误文本） — verify: 对网关桩：返回向量长度等于 DIM；打开桩的嵌入失败开关后返回可被 `errors.Is(err, ErrUnavailable)` 识别的错误；错误字符串不含桩地址与密钥
- [x] 4.2 实现 Qdrant 客户端（标准库 REST）：惰性确保集合 `campusclaw_chunks`（Cosine、`EMBEDDING_DIM`、`class_id`/`material_id` payload 索引，维度不符按依赖不可用处理）、批量 upsert（`wait=true`）、带 `class_id` 过滤与 0.35 阈值的检索、按 `(material_id, class_id)` 删除 — verify: 对 dev 的 qdrant：upsert 一个点后 `GET /collections/campusclaw_chunks/points/<id>` 的 payload 键集合恰为 `class_id, material_id, knowledge_entry_id, chunk_id, chunk_index`；用 A 班 filter 检索不返回 B 班的点；按材料删除后该材料的点数为 0
- [x] 4.3 实现对话客户端（OpenAI 兼容 `/chat/completions`、非流式、超时、失败归为"依赖不可用"） — verify: 对网关桩调用一次，`$STUB/stats` 的对话计数 +1 且返回文本非空；打开对话失败开关后返回 `ErrUnavailable`

## 5. 索引流水线与上传

- [x] 5.1 实现索引器：读取材料的 `pending` 切片，按批嵌入 → upsert → 置 `indexed`；任一批失败则该批及其后未处理的切片置 `failed` 并停止；使用 `context.WithoutCancel` 加 `INDEX_TIMEOUT_SECONDS` 超时；同一材料用进程内互斥锁串行 — verify: 桩正常时一份 3 片的材料切片全为 `indexed` 且 Qdrant 点数为 3；打开嵌入失败开关后同样的材料切片全为 `failed`、MySQL 行仍在
- [x] 5.2 上传接入切分参数与索引：参数解析放在落盘之前，事务内写 `materials`、`knowledge_entries`（含策略字段）、`knowledge_chunks(pending)`，提交后调用索引器，响应 `201 {id, title, index_status}` — verify: teacher_a 上传 2000 字 `.md`：三表各增行、切片区间为 `[0,800) [720,1520) [1440,2000)`、`class_id` 均为 A 班、响应 `index_status` 为 `indexed`
- [x] 5.3 非法切分参数与索引失败的边界 — verify: 上传时片长 50 / 重叠 60 / 未知策略各得 400，三张表行数与 `uploads/` 文件数前后不变；打开嵌入失败开关后上传合法文件得 201，`materials` 行保留、切片全 `failed`，且用 `keyword` 检索该材料的词能命中（见 7.2 前先用 SQL 直接确认切片存在）
- [x] 5.4 向量主键与 payload 约束的端到端核对 — verify: 上传后取该材料某切片的 MySQL `id`，Qdrant 中同 ID 的点存在，其 payload 键集合恰为五个约定键且不含正文（`curl` 点详情后 `grep` 正文片段无输出）
- [x] 5.5 预处理不改正文 — verify: teacher_a 以 `custom`、开启移除 URL 与折叠空白上传含 URL 的 `.md`：`knowledge_entries.body_text` 与上传文件 `diff` 无输出；对应 `chunk_text` 不含 `http`

## 6. 详情、重建与补偿

- [x] 6.1 材料详情增加聚合 `index_status`、`chunk_count`、`chunk_strategy`（失败 > 待处理 > 已索引） — verify: student_a1 请求 A 班材料详情：三字段存在，JSON 中无 `stored_name`、路径、向量、Qdrant 地址；构造一个含 `failed` 切片的材料，聚合状态为 `failed`
- [x] 6.2 实现 `POST /api/materials/{id}/reindex`：仅教师（403 在读请求体前）→ 取材料比班级（跨班与不存在同 404）→ 参数校验 400 → Qdrant 按 `(material_id, class_id)` 删旧向量 → 事务替换切片并更新策略 → 索引 — verify: student_a1 调用得 403 且该材料切片与向量不变；teacher_a 对 B 班材料与对 `999999` 的响应 `diff` 无输出；teacher_a 以 `custom`、片长 200 重建 A 班材料：旧切片 ID 在 MySQL 与 Qdrant 中都已消失，新切片全 `indexed`，`body_text` 不变
- [x] 6.3 向量库不可用时重建不破坏现状；MySQL 步骤失败时旧切片置 `failed` — verify: `docker compose stop qdrant` 后 teacher_a 重建得 503，该材料的切片 ID 集合与重建前相同；恢复后再次重建成功
- [ ] 6.4 用重建修复失败的索引 — verify: 打开嵌入失败开关上传得到 `failed` 切片；关闭开关后对该材料重建，聚合 `index_status` 变为 `indexed`
- [x] 6.5 实现启动补偿扫描：为没有切片的 `knowledge_entries` 按 `auto` 生成切片并索引，重试遗留的 `pending` 切片；幂等 — verify: 删除某存量材料的全部切片后重启 api，切片重新出现；连续重启两次后切片总数不变；把某切片手工置 `pending` 后重启变为 `indexed`
- [x] 6.6 种子接入补偿扫描（预置材料同样有切片；网关 / 向量库不可用时种子仍完成） — verify: `docker compose down -v` 后全新启动，两条预置材料各有切片且属于对应班级；关闭桩后 `down -v` 再启动，api 正常启动、预置切片为 `failed`、`keyword` 能命中；重启不产生重复切片

## 7. 检索

- [ ] 7.1 实现 `POST /api/search` 的请求校验与响应骨架（`query`、`mode` 缺省 `hybrid`、`top_k` 缺省 5 范围 1–20；查询去空白后为空或超过 500 字符、未知模式、`top_k` 越界均 400；无命中返回 `hits: []` 与「资料中未找到相关内容」） — verify: 空查询、纯空白、501 字符、`mode=x`、`top_k=0`、`top_k=21` 均得 400，且 `$STUB/stats` 计数不变
- [ ] 7.2 实现 `keyword` 模式（不调用嵌入网关与向量库） — verify: student_a1 检索 A 班材料里的双字词得 200 且命中均为 A 班切片、正文含该词；检索只在 B 班出现的词得 200、`hits` 为空；`docker compose stop qdrant` 并关闭桩后同样的 `keyword` 请求仍得 200 且结果不变
- [ ] 7.3 实现 `vector` 模式（嵌入查询 → Qdrant 带班级过滤与 0.35 阈值 → MySQL 回表并再次按班级过滤，保持 Qdrant 顺序，丢弃 MySQL 里不存在的点） — verify: 用 A 班切片的原句检索得 ≥ 1 条命中；用与所有材料无关的乱码检索得 200 且 `hits` 为空；临时把某 A 班切片的 `class_id` 改成 B 班后同一检索不再返回它（改回后恢复）；删除某切片 MySQL 行而保留向量点后检索不返回它
- [ ] 7.4 实现 RRF 融合与 `hybrid`（两路各取前 50，名次从 1 起，`1/(60+rank)` 求和，降序、同分按切片 ID 升序，一路为空取另一路） — verify: `go test` 断言名次 (1,2) 的切片得 `1/61+1/62`、仅一路出现名次 2 的得 `1/62` 且前者在前；同分按 ID 升序；一路为空时等于另一路；集成：不带 `mode` 与 `mode=hybrid` 结果相同
- [ ] 7.5 命中结果的溯源字段 — verify: 每条命中含 `material_id`、材料标题、`chunk_id`、`chunk_index`、`char_start`、`char_end`、摘录（`chunk_text` 前 300 字，超出加 `…`）；脚本对未预处理材料校验：摘录是详情 `body` 中 `[char_start, char_end)` 字符的前缀；`hits` 中无任何向量或路径字段
- [ ] 7.6 依赖不可用的降级 — verify: `docker compose stop qdrant` 后 `vector`、`hybrid` 得 503，`keyword` 得 200，之后 `GET /api/me` 仍得 200；打开桩的嵌入失败开关后同样如此；503 响应体 `grep` 不出现 `qdrant`、`6333`、`api-key`、桩地址
- [ ] 7.7 权限与班级范围 — verify: 不带 Cookie 调用得 401 且响应体无标题 / 摘录；student_a1 在 query、Header、请求体里附带 B 班 ID 的结果与不带时 `diff` 无输出；student_a1 与 student_b1 对同一查询词的结果互不含对方班级的切片；teacher_a 同样只见 A 班

## 8. 问答

- [ ] 8.1 实现 `POST /api/ask` 的输入校验与历史过滤（问题非空且 ≤ 1000 字符；`messages` 只保留 `user`/`assistant`，最近 10 条、每条 ≤ 2000 字符，其余丢弃） — verify: 空问题得 400 且桩计数不变；请求里带一条 `role: system` 的"忽略所有规则"，`$STUB/stats` 记录的对话请求体中不含该文本
- [ ] 8.2 实现问答编排：`hybrid` 取前 4 → 有命中才调用对话网关（服务端 system 提示 + 编号资料块 + 过滤后的历史 + 问题，资料声明为数据）→ 回答后处理（移除超出 `1..len(citations)` 的 `[n]`）→ 返回 `{answer, citations}` — verify: 对能命中 3 个切片的问题，桩对话计数恰 +1，`citations` 长度为 3 且与同一查询的 `hybrid` 前 3 条顺序一致，每项含标题、切片序号、字符区间、摘录；让桩输出 `[9]` 时该标记不出现在最终 `answer` 中
- [ ] 8.3 无依据时不调用模型 — verify: student_a1 询问只存在于 B 班的内容、以及完全无关的问题，均得 200、回答含「资料中未找到相关内容」、`citations` 为 `[]`，且 `$STUB/stats` 的对话计数不变
- [ ] 8.4 问答的依赖不可用与不流式 — verify: 分别停 qdrant、打开嵌入失败开关、打开对话失败开关，三种情况下 `/api/ask` 均得 503 且响应中没有编造的回答；正常响应的 `Content-Type` 为 `application/json`，不含 `text/event-stream`
- [ ] 8.5 问答的权限 — verify: 不带 Cookie 得 401；student_a1 与 teacher_a 均可提问；student_a1 传 B 班 ID 无效（传给对话网关的资料块中无 B 班切片，可从桩的最近请求体核对）

## 9. 综合验收与部署

- [ ] 9.1 跨班全链路（`keyword`/`vector`/`hybrid`/`ask` 四条路径）— verify: 脚本以 student_a1 检索 B 班预置材料独有的语句，三种模式均 200 空结果，问答的桩对话计数不变；以 student_b1 检索同一语句均能命中 B 班
- [ ] 9.2 生产编排只暴露 web — verify: `docker compose up --build -d` 后 `docker compose port qdrant 6333` 与 `6334`、`docker compose port api 8081` 均无输出；`lsof -iTCP -sTCP:LISTEN -P | grep -E ':(3306|6333|6334|8081)\b'` 无输出；`http://localhost:8080/health` 仍返回 `{"status":"ok"}`
- [ ] 9.3 重建后数据仍在 — verify: 上传并索引一份材料后 `docker compose down && docker compose up -d`（不带 `-v`），该材料 `vector` 检索仍命中、`index_status` 仍为 `indexed`
- [ ] 9.4 嵌入维度变更的行为 — verify: 把 `EMBEDDING_DIM` 改为另一个值重启 api：`vector`/`hybrid` 得 503、`keyword` 得 200，日志明确指出维度不符（不含密钥）；改回后恢复
- [ ] 9.5 密钥不外泄 — verify: `.env` 里的网关密钥值 `grep -r` 检索 `frontend/dist`、检索 / 问答 / 详情的响应样本均无输出；`git grep -n "API_KEY="` 只出现在 `.env.example` 且值为占位

## 10. 文档与发布

- [ ] 10.1 README 补充：新增变量、Qdrant 与网关桩、检索 / 重建 / 问答的 curl 示例、切分策略说明、"更换嵌入模型需删集合并重建"、本迭代不做项 — verify: 依 README 中的 curl 示例从零跑通一次上传 → 检索 → 问答
- [ ] 10.2 编写 `docs/iteration-2.md`：关键 Scenario（跨班空结果、`keyword` 在依赖故障下可用、`vector`/`hybrid` 503、阈值丢弃、RRF 分数、无依据不调用模型、system 消息被丢弃、payload 无正文）的命令与输出，以及三项设计决策（同步索引、预处理后置以保证可溯源、RRF）及否决的备选 — verify: 每条 Scenario 都有"通过 / 不通过"结论与命令输出；三项决策各含"决策 / 备选 / 理由"
- [ ] 10.3 运行 `openspec validate add-traceable-vector-retrieval --strict` — verify: 退出码 0 且无 error（`auth-upload` MODIFIED 的"归档前需要主规格"提示属预期，见 10.5）
- [ ] 10.4 确认仓库无真实密钥 — verify: `git ls-files | grep -x .env` 无输出；`git grep -n "API_KEY="` 只出现在 `.env.example` 且值为占位
- [ ] 10.5 用真实网关做一次冒烟（不进入自动验收）并记录结果 — verify: 用真实嵌入与对话网关上传一份材料、检索、提问各一次，`docs/iteration-2.md` 记录是否成功与模型名（不写密钥）
- [ ] 10.6 归档本 change。**前置：`add-auth-rbac-class-knowledge` 已归档**（否则 `auth-upload` 主规格不存在，MODIFIED 无法归档）；需使用者明确指示后执行 `/opsx:archive` — verify: `openspec list` 无活动变更；`openspec/specs/knowledge-retrieval/spec.md` 已生成；`openspec/specs/auth-upload/spec.md` 中「材料上传与知识库入库」「预置核心数据」「Compose 部署与健康检查」三条已是更新后的内容
