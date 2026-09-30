## Why

迭代 1 让教师能把材料上传进按班级隔离的知识库，但学生和教师只能靠标题在列表里翻找，材料正文（`knowledge_entries.body_text`）没有被真正用起来。本变更（迭代 2）在不破坏班级边界的前提下，让本班成员能按内容检索材料，并且每条结果都能追溯到"哪份材料的哪一段"，为问答提供可核对的依据。

## What Changes

- **切片入库**：上传（及重建）后，正文按策略切成切片写入 MySQL `knowledge_chunks`（切片文本、序号、在 `body_text` 中的字符偏移、`class_id`、`index_status`）；切片再经嵌入写入 Qdrant 集合 `campusclaw_chunks`（余弦距离）。向量主键 = `knowledge_chunks.id`，payload 只含 `class_id`、`material_id`、`knowledge_entry_id`、`chunk_id`、`chunk_index`，**不含正文**。
- **三种切分策略**：`auto`（800 字、重叠 80）、`custom`（100–2000 字、重叠 0–50%、可移除 URL / 邮箱、可折叠空白）、`hierarchy`（按 Markdown 标题）。预处理只作用于切片文本，不修改 `body_text`。教师可按新策略重建某份材料的索引，重建先删旧向量与旧切片。
- **失败不丢材料**：嵌入或向量写入失败时材料与切片保留，切片 `index_status` 标记为 `failed`，可通过重建恢复。
- **班级内检索**：新增检索接口，学生与教师均可调用。三种模式：`keyword`（只查 MySQL FULLTEXT ngram）、`vector`（Qdrant 余弦，低于 0.35 丢弃，再回 MySQL 取正文）、`hybrid`（默认；两路各自先过滤，再按名次做 RRF，k=60）。向量路径、关键词路径与回表都按会话班级过滤，请求里的 `class_id` 一律忽略。
- **结果可溯源**：每条命中给出材料标题、切片序号、字符区间与摘录（摘录取自 MySQL）。无命中返回「资料中未找到相关内容」且 `hits` 为空；他班内容检索不到，表现为 200 空结果（不是 403/404）。
- **降级与错误**：Qdrant 或嵌入网关不可用时 `keyword` 仍可用，`vector` / `hybrid` 返回 503；空查询返回 400。
- **带引用的问答** `POST /api/ask`：用混合检索取本班前 4 条切片，**有切片才**调用对话网关；回答中的 `[1][2]` 与 `citations` 顺序一致；无切片时不调用模型、`citations` 为空；丢弃客户端传入的 `system` 消息；不做流式。
- **外部服务与部署**：嵌入与对话网关只在服务端调用，密钥走环境变量；Qdrant 加入 Docker Compose，不映射宿主端口，数据持久化。
- **对迭代 1 规约的扩展（MODIFIED）**：上传入库要求增加切片与索引步骤；预置数据要求增加切片；Compose 要求从三个服务扩展为四个服务。

## Capabilities

### New Capabilities

- `knowledge-retrieval`：班级范围内的知识库切分与索引、三种检索模式、可溯源的检索结果、索引状态与重建、带引用的问答、外部服务凭据与降级行为。

### Modified Capabilities

- `auth-upload`：
  - 「材料上传与知识库入库」：入库事务在 `materials`、`knowledge_entries` 之外同时写入 `knowledge_chunks`；向量索引在事务之后进行，其失败不回滚材料。
  - 「预置核心数据」：预置材料同样生成切片。
  - 「Compose 部署与健康检查」：服务由 web / api / db 扩展为 web / api / db / qdrant；qdrant 端口不映射宿主机，向量数据持久化。

> 前置依赖：`openspec/specs/` 目前为空，`auth-upload` 主规格要等 `add-auth-rbac-class-knowledge` 归档后才会生成（该 change 的 tasks 9.5–9.7 尚未完成）。上述 MODIFIED 的基线取自该 change 的 delta；本 change 需在其归档之后再归档。

## Impact

- **后端**（`backend/`）：新增切分、嵌入与对话网关客户端、Qdrant 客户端、检索与问答模块；改造上传流程与种子；新增迁移 `002_*.sql`（`knowledge_chunks`、FULLTEXT ngram 索引、`knowledge_entries` 的切分策略字段）。
- **新增 API**：`POST /api/search`、`POST /api/ask`、`POST /api/materials/{id}/reindex`；`POST /api/materials` 增加可选切分参数；`GET /api/materials/{id}` 增加索引状态。
- **基础设施**：`docker-compose.yml` / `docker-compose.dev.yml` 增加 `qdrant` 服务与数据卷；`.env.example` 增加 Qdrant、嵌入网关、对话网关的变量名与占位值。
- **前端**：本变更**不含**前端页面改动，接口以 curl 验收（见 Non-goals）。
- **依赖**：Go 侧只用标准库 `net/http` 调用 Qdrant REST 与网关（不引入 SDK 与框架）。

## Non-goals（非目标）

- **重排序**：不做 rerank 模型或二次排序，混合结果只按 RRF。
- **框架**：不引入 LangChain / LlamaIndex 等编排框架，不引入 Web 框架。
- **流式对话**：`/api/ask` 一次性返回完整 JSON，不做 SSE / 流式。
- **跨班检索**：任何角色（含教师）都只能检索会话所属班级；不做平台管理员的跨班检索。
- **多轮记忆与 Agent**：不做对话历史持久化、工具调用、Agent 编排；`/api/ask` 无服务端会话状态。
- **新文件类型**：仍只支持 `.txt` / `.md`，不解析 PDF、Word、图片。
- **前端检索页面**：不做检索与问答的前端界面。
- **异步索引队列**：索引与上传同一请求内同步完成，不引入消息队列或后台任务系统。
