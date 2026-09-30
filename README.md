# CampusClaw

- **价值**：面向中小学的教研智能体，把分散在群文件与网盘里的教学材料集中进按班级隔离的知识库，让班内成员能按内容检索，并获得带材料出处的回答。
- **场景**：教师登录后上传并管理本班教学材料；学生登录后只读查看、下载并检索本班材料，也可向本班知识库提问；不同班级之间数据互不可见。
- **本学期不做**：作业流程、注册与找回密码、OAuth/SSO、平台超级管理员、跨班检索、重排序、流式对话、多轮记忆与 Agent、PDF/Word 解析、公网部署与多副本高可用。

## 当前迭代

- 迭代 1（v0.1.0-auth-upload）：登录与角色权限、班级数据隔离、材料上传与知识库入库。规约见 `openspec/changes/add-auth-rbac-class-knowledge/`。
- 迭代 2（v0.2.0-vector-retrieval）：正文切片与向量索引、班级内检索（关键词 / 向量 / 混合）、带引用的问答。规约见 `openspec/changes/add-traceable-vector-retrieval/`。

- 技术栈：React + TypeScript + Vite（前端）/ Go net/http（后端）/ MySQL 8.0 / Qdrant 向量库 / Nginx / Docker Compose
- 部署形态：单实例 Docker Compose，只暴露 web 端口（默认 8080）
- 跨班访问：材料接口访问其他班级的材料与访问不存在的材料，统一返回 **404**；检索与问答则返回 **200 空结果**（不泄露"该内容是否存在"）

登录凭据已从「服务端会话 + HttpOnly Cookie」改为 **JWT Bearer token**（见 `openspec/changes/switch-to-jwt-bearer-auth/`）：登录接口在响应 JSON 中返回 `token`，之后每个请求用 `Authorization: Bearer <token>` 携带，登出即吊销该 token。

## 快速开始

```bash
cp .env.example .env
# 编辑 .env：JWT_SECRET（至少 32 个字符）/ DB_PASSWORD / MYSQL_ROOT_PASSWORD / SEED_*_PASSWORD，
# 以及迭代 2 的外部服务：QDRANT_URL、EMBEDDING_BASE_URL/API_KEY/MODEL/DIM、
# CHAT_BASE_URL/API_KEY/MODEL（填你的真实网关地址与密钥；Qdrant 在 Compose 内固定为
# http://qdrant:6333，无需修改）
docker compose up --build -d
```

- 登录地址：http://localhost:8080 （`WEB_PORT` 未设置时默认 8080；被占用可在 `.env` 中修改 `WEB_PORT`）
- 健康检查：http://localhost:8080/health → `{"status":"ok"}`，无需登录
- 共四个服务：web、api、db、qdrant。只有 web 映射宿主端口；db（3306）与 qdrant（6333/6334）只在 Compose 网络内可达

### 本地开发：不接真实模型（确定性网关桩）

没有真实嵌入 / 对话网关时，用开发编排也能跑通上传、检索与问答：

```bash
docker compose -f docker-compose.dev.yml up -d
```

- 端口只绑 `127.0.0.1`：api → 8081、db → 3306、qdrant → 6333、网关桩 → 8090
- 网关桩（源码在 `backend/cmd/stubgateway`）是确定性实现：`/embeddings` 把字符二元组哈希成 `EMBEDDING_DIM` 维向量；`/chat/completions` 回显服务端给出的资料编号；`GET /stats` 查看两个接口的调用计数与最近一次请求体；`POST /control` 切换故障：`{"embed_fail":true}`、`{"chat_fail":true}`、`{"out_of_range_citation":true}`
- 用桩时把 `.env` 里的 `EMBEDDING_BASE_URL` 与 `CHAT_BASE_URL` 改为 `http://stubgateway:8090`，密钥随意；桩只存在于开发编排与源码，不进正式镜像

### 用 curl 调接口

```bash
API=http://localhost:8080   # 开发编排（dev）下改为 http://localhost:8081

# 1) 登录取 token（口令取自本机 .env，不要写进命令历史或文档）
TOKEN=$(curl -s -H 'Content-Type: application/json' \
  -d '{"username":"teacher_a","password":"<SEED_TEACHER_A_PASSWORD>"}' \
  $API/api/login | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

# 2) 带 Bearer 头取本班材料列表
curl -s -H "Authorization: Bearer $TOKEN" $API/api/materials

# 3) 上传材料（教师；切分参数可选，缺省 auto）
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -F 'file=@unit3.md;type=text/markdown' -F 'title=第三单元笔记' \
  -F 'strategy=custom' -F 'chunk_size=200' -F 'overlap_percent=10' \
  $API/api/materials
# → 201 {"id":11,"title":"第三单元笔记","index_status":"indexed"}

# 4) 检索本班知识库（mode 缺省 hybrid，可换 keyword / vector）
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"query":"光合作用的产物","mode":"hybrid","top_k":5}' \
  $API/api/search
# → {"mode":"hybrid","hits":[{"material_id":11,"material_title":"第三单元笔记","chunk_id":..,
#     "chunk_index":..,"char_start":..,"char_end":..,"excerpt":"..","score":..}]}

# 5) 按新策略重建某份材料的索引（教师）
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"strategy":"hierarchy"}' \
  $API/api/materials/11/reindex
# → 200 {"id":11,"title":"第三单元笔记","index_status":"indexed","chunk_count":..,"chunk_strategy":"hierarchy"}

# 6) 带引用的问答（取本班最相关的 4 个切片；没有依据时不调用对话网关）
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"question":"光合作用在哪里进行？"}' \
  $API/api/ask
# → {"answer":"...[1]...","citations":[{"material_id":11,"material_title":"第三单元笔记",
#     "chunk_index":..,"char_start":..,"char_end":..,"excerpt":"..","score":..}]}

# 7) 当前身份；8) 登出（该 token 立即失效）
curl -s -H "Authorization: Bearer $TOKEN" $API/api/me
curl -s -X POST -H "Authorization: Bearer $TOKEN" $API/api/logout
```

- 回答中的 `[n]` 与 `citations` 一一对应（`citations[n-1]`）。`/api/ask` 可带 `messages` 历史，但只保留 `user` / `assistant` 角色（客户端的 `system` 消息一律丢弃）。
- 检索与问答的班级范围只取 token 所属班级；在 query、请求体或请求头里带 `class_id` 一律被忽略。
- **不再使用 `curl -c/-b` Cookie jar**：token 放在 query、Cookie 或请求体里都会被忽略，只有 `Authorization` 头有效。
- `JWT_TTL` 可选，默认 `24h`；到期后需重新登录（不做刷新令牌）。重新登录不会踢掉旧 token，旧 token 到自己过期或被登出吊销为止一直有效。

### 切分策略

上传（multipart 字段）与重建（JSON 字段，名字相同）都接受以下参数，缺省为 `auto`：

| 策略 | 行为 | 可用参数 |
| --- | --- | --- |
| `auto`（缺省） | 每 800 字一片，相邻片重叠 80 字 | 无 |
| `custom` | 自定义窗口切分 | `chunk_size` 100–2000；`overlap_percent` 0–50；`remove_url` / `remove_email` / `collapse_whitespace` 开关 |
| `hierarchy` | 按 Markdown 标题切分（围栏代码块里的 `#` 不算标题），超过 800 字的章节再按 `auto` 细切 | 同 `custom` 的三个开关 |

- 预处理开关只影响切片文本，`knowledge_entries.body_text` 与磁盘文件始终是原文。
- 重建先删该材料的旧向量，再替换切片并按新策略重新索引；索引失败不回滚材料，受影响切片标记为 `failed`，再次重建即可恢复。
- 切分参数不合法一律 400，且不留下任何记录或文件。

## 预置账号

| 用户名 | 角色 | 班级 | 密码 |
| --- | --- | --- | --- |
| teacher_a | 教师 | A 班 | 来自 `.env` 的 `SEED_TEACHER_A_PASSWORD` |
| student_a1 | 学生 | A 班 | 来自 `.env` 的 `SEED_STUDENT_A1_PASSWORD` |
| student_b1 | 学生 | B 班 | 来自 `.env` 的 `SEED_STUDENT_B1_PASSWORD` |

首次启动会自动建表，写入以上账号与两班的示例材料（班 B 有两条，其中一条只含 B 班独有内容，用于验证跨班隔离），并为材料生成切片与向量索引；种子与索引过程幂等，重启不会重复插入或覆盖已上传内容。索引依赖不可用时种子照常完成，预置切片标记为 `failed`，重建即可恢复。

## 已知行为与限制

- **token 存放在 `sessionStorage`**：前端能读到 token 才能把它放进 `Authorization` 头，这是 Bearer 方案的固有代价——页面一旦出现 XSS，token 就可能被读走。因此只把 token 存进 `sessionStorage`（关闭标签页即清除，且不跨标签页共享），不存 `localStorage`，不存用户名/角色；服务端再用 `Cache-Control: no-store` 与严格 CSP（`script-src 'self'`，无 `unsafe-inline`/`unsafe-eval`）收敛风险。发现泄露时更换 `JWT_SECRET` 即可让全部 token 失效。
- **从迭代 1 升级需重新登录**：旧的会话 Cookie 不再被接受（`sessions` 表保留但已不再读写），`SESSION_SECRET`/`SESSION_TTL` 由 `JWT_SECRET`/`JWT_TTL` 取代。
- **跨班访问**：材料相关接口（列表 / 详情 / 下载 / 重建）对不存在的 ID 与他班 ID 返回相同的 404；检索与问答对他班内容返回 200 空结果（`message` 为「资料中未找到相关内容」，`hits`/`citations` 为空），不区分"不存在"与"不属于你"。
- **检索依赖的降级**：Qdrant 或嵌入网关不可用时 `keyword` 检索仍可用（只查 MySQL 全文索引），`vector`、`hybrid` 与 `/api/ask` 返回 **503**；对话网关不可用时 `/api/ask` 返回 503。错误响应不暴露依赖地址与密钥。
- **更换嵌入模型需删集合并重建**：向量与模型绑定，`EMBEDDING_DIM` 必须与集合一致。更换模型或维度后旧向量与新向量不可比——需删除 Qdrant 集合（或数据卷）并对材料逐个重建索引；维度不符时 api 按依赖不可用处理（`vector`/`hybrid` 503，日志写明维度不符），`keyword` 不受影响。集合在首次索引时自动创建。
- **单实例部署**：当前只支持单个 api/db 实例（登录限流计数在内存中，重启会清零），不做多副本、公网证书与 CI/CD，详见 `openspec/changes/add-auth-rbac-class-knowledge/proposal.md` 的 Non-goals。
- **MySQL 首次启动较慢**：db 容器首次初始化建表可能需要几十秒，此时访问 web 可能短暂看到 502，等待 `docker compose ps` 中 db 变为 `healthy` 后即可正常访问。
- **修改端口**：如需更换对外端口，在 `.env` 中设置 `WEB_PORT` 后重新 `docker compose up -d` 即可，无需改动 compose 文件。
- **迭代 2 不做**：重排序（结果只按 RRF 融合）、跨班检索（含教师）、流式输出（`/api/ask` 一次性返回 JSON）、多轮记忆与 Agent、PDF/Word 等新文件类型（仍只支持 `.txt` / `.md`）、检索与问答的前端页面（接口可用，界面未做）、异步索引队列（上传请求内同步完成索引）。
