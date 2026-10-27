# CampusClaw

- **价值**：面向中小学的教研智能体，把分散在群文件与网盘里的教学材料集中进按班级隔离的知识库，让班内成员能按内容检索，并获得带材料出处的回答。
- **场景**：教师登录后上传并管理本班教学材料；学生登录后只读查看、下载并检索本班材料，也可向本班知识库提问；不同班级之间数据互不可见。
- **本学期不做**：作业流程、注册与找回密码、OAuth/SSO、平台超级管理员、跨班检索、重排序、由模型自主选择技能（技能仍按「适用条件」路由）、跨班共享智能体或技能、会话删除与重命名、PDF/Word 解析、公网部署与多副本高可用。

## 当前迭代

- 迭代 1（v0.1.0-auth-upload）：登录与角色权限、班级数据隔离、材料上传与知识库入库。规约见 `openspec/changes/add-auth-rbac-class-knowledge/`。
- 迭代 2（v0.2.0-vector-retrieval）：正文切片与向量索引、班级内检索（关键词 / 向量 / 混合）、带引用的问答。规约见 `openspec/changes/add-traceable-vector-retrieval/`。
- 迭代 3（v0.3.0-knowledge-qa-ui）：新增「知识检索」与「知识问答」两个前端页面（三种检索模式、带出处的回答卡片、每题独立且不保存历史）。规约见 `openspec/changes/add-knowledge-qa-ui/`。
- 迭代 4（v0.4.0-tutor-agents-skills-streaming）：教师可配置的智能体与技能、按「适用条件」命中的技能路由、带最近历史与出处的流式助手会话（SSE），以及 `/swagger/` 接口文档；JWT 载荷精简为 `sub`/`jti`/`iat`/`exp`/`iss`，改库角色与班级下一次请求即生效，发布后所有旧 token 失效需重新登录。规约见 `openspec/changes/add-tutor-agents-skills-streaming/`。
- 迭代 5（v0.5.0-mcp-tools-and-api-tests）：教师登记并连接外部 MCP 工具服务器，审阅工具后绑定到技能；助手命中技能时在宿主校验与分班授权下调用工具，并留下不含参数与结果的审计；新增按 URL 组织、可一键运行并回查数据库的接口测试 `apitest/` 与 `/openapi.json`。规约见 `openspec/changes/add-mcp-tools-and-api-tests/`。

- 技术栈：React + TypeScript + Vite（前端）/ Go net/http（后端）/ MySQL 8.0 / Qdrant 向量库 / Nginx / Docker Compose
- 部署形态：单实例 Docker Compose，只暴露 web 端口（默认 8080）
- 跨班访问：材料接口访问其他班级的材料与访问不存在的材料，统一返回 **404**；检索与问答则返回 **200 空结果**（不泄露"该内容是否存在"）

登录凭据已从「服务端会话 + HttpOnly Cookie」改为 **JWT Bearer token**（见 `openspec/changes/switch-to-jwt-bearer-auth/`）：登录接口在响应 JSON 中返回 `token`，之后每个请求用 `Authorization: Bearer <token>` 携带，登出即吊销该 token。

## 快速开始

```bash
cp .env.example .env
# 编辑 .env：JWT_SECRET（至少 32 个字符）/ DB_PASSWORD / MYSQL_ROOT_PASSWORD / SEED_*_PASSWORD，
# MCP_AUTH_TOKEN（api 与 mcp-demo 共用，留空则 mcp-demo 不启动、campus-tools 不预置），
# 以及迭代 2 的外部服务：QDRANT_URL、EMBEDDING_BASE_URL/API_KEY/MODEL/DIM、
# CHAT_BASE_URL/API_KEY/MODEL（填你的真实网关地址与密钥；Qdrant 在 Compose 内固定为
# http://qdrant:6333，无需修改）
docker compose up --build -d
```

- 登录地址：http://localhost:8080 （`WEB_PORT` 未设置时默认 8080；被占用可在 `.env` 中修改 `WEB_PORT`）
- 健康检查：http://localhost:8080/health → `{"status":"ok"}`，无需登录
- 接口文档：http://localhost:8080/swagger/ （开发编排下为 http://localhost:8081/swagger/；无需登录，可点「Authorize」填入 `Bearer <token>` 试调接口）；Swagger UI 读取的是同一份 OpenAPI 3 文档 `GET /openapi.json`（无需认证，不含密钥）
- 共五个服务：web、api、db、qdrant、mcp-demo。只有 web 映射宿主端口；db（3306）与 qdrant（6333/6334）只在 Compose 网络内可达，mcp-demo（8090）只绑 `127.0.0.1` 供本机自查

### 本地开发：不接真实模型（确定性网关桩）

没有真实嵌入 / 对话网关时，用开发编排也能跑通上传、检索、问答与助手的流式会话：

```bash
docker compose -f docker-compose.dev.yml up -d
```

- 端口只绑 `127.0.0.1`：api → 8081、db → 3307、qdrant → 6333、网关桩 → 8091、`mcp-demo` → 8090
- 网关桩（源码在 `backend/cmd/stubgateway`）是确定性实现：`/embeddings` 把字符二元组哈希成 `EMBEDDING_DIM` 维向量；`/chat/completions` 回显服务端给出的资料编号（流式请求把回答按每 3 字一段返回）；`GET /stats` 查看两个接口的调用计数、最近一次请求体与当前开关；`POST /control` 可逐个切换开关（未提到的保持不变）：`{"embed_fail":true}` 嵌入失败、`{"chat_fail":true}` 对话失败（流式与非流式都失败）、`{"out_of_range_citation":true}` 回答多带一个越界引用 `[9]`、`{"fail_before_first_delta":true}` 流式回答在首段之前失败、`{"abort_after_deltas":2}` 推 2 段后断开（`-1` 关闭）、`{"chat_text":"..."}` 替换回答文字（`""` 恢复回显）
- 用桩时把 `.env` 里的 `EMBEDDING_BASE_URL` 与 `CHAT_BASE_URL` 改为 `http://stubgateway:8090`，密钥随意；桩只存在于开发编排与源码，不进正式镜像

### 页面用法：知识检索与知识问答

登录后页面顶部共有七个标签：「材料」「知识检索」「知识问答」「助手」对教师与学生相同，「智能体管理」「技能管理」「工具服务器」仅教师可见。本节先讲「知识检索」与「知识问答」，其余见下几节。

- **知识检索**（`/search`）：输入问句（最长 500 字，去掉空白为空时不提交），选择模式后提交；结果按接口顺序逐条给出材料标题、切片序号、字符区间与摘录。三种模式：
  - 混合（`hybrid`，默认）：关键词与向量两路各取候选、融合排序；
  - 关键词（`keyword`）：只查 MySQL 全文索引，向量库或嵌入网关不可用时仍可用；
  - 向量（`vector`）：语义相似检索，依赖嵌入网关与向量库。
- **知识问答**（`/ask`）：就本班材料提问（最长 1000 字）。每个问题独立提交：请求只带当前问题，不带此前对话，问答**不保存历史**（刷新或切换标签后清空）；有依据时显示「依据回答」卡片与按顺序编号 `[1]`、`[2]`… 的出处列表，没有命中时不调用模型，只显示「资料中未找到相关内容」。
- 两个页面都只把接口返回的内容按纯文本显示（不解析 HTML / Markdown），并原样保留接口给出的顺序与编号。
- 接口返回 **503**（向量库或网关不可用）时页面显示「检索服务暂不可用」：在检索页换用「关键词」模式仍可正常使用。

### 页面用法：助手与教师管理页

- **助手**（`/assistant`）：左侧是本人会话列表（按最近更新倒序）与「新建会话」（下拉只列本班已启用的智能体，会话创建后锁定所选智能体）；提问后回答**逐段出现**，完成后显示本轮命中的技能名与按 `[1]`、`[2]` 排列的出处（材料标题、切片序号、字符区间、摘录），点开出处可查看对应材料详情。本轮调用了 MCP 工具时，回答上方还会出现 `工具名@服务器名` 小标签（`ok` / `denied` / `error`），被拒与失败使用醒目样式。与知识问答不同，助手会话**保存历史**（重开会话可见此前各轮问答与出处），且**检索落空也会调用模型作答**（此时无出处，回答中的 `[n]` 标注会被服务端剥掉）。同一会话同一时间只能生成一条回答，生成中重复提交会收到 409 文案；回答中断（生成失败或连接断开）时已显示的文字保留并提示。
- **智能体管理**（`/admin/agents`，教师）：新建 / 编辑智能体的名称、提示词、启用状态，并勾选绑定的技能与材料；保存同名时显示服务端 400 文案。
- **技能管理**（`/admin/skills`，教师）：粘贴或选择技能文件创建技能、编辑启用状态、删除（有确认）；文件不合规时页面原样显示服务端的错误原文（含行号或字段名）。
- **工具服务器**（`/admin/mcp`，教师）：登记、连接、查看工具、断开与删除本班 MCP 服务器，并查看本班调用审计；连接失败时显示服务端返回的可读提示，页面任何位置都不显示令牌原文。

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

### 助手接口一览

新增的智能体 / 技能 / 会话接口都要求 `Authorization`，班级与身份只取自 token（请求里带 `class_id` / `role` / `user_id` 一律被忽略）：

| 接口 | 说明 |
| --- | --- |
| `GET /api/agents`、`POST /api/agents`、`PUT /api/agents/{id}` | 教师维护智能体（名称、提示词、启用状态、绑定的技能与材料）；学生的 `GET` 只返回已启用项且每项只有 `id`、`name`，写操作 403 |
| `GET /api/skills`、`POST /api/skills`、`PUT /api/skills/{id}`、`DELETE /api/skills/{id}` | 教师维护技能（提交 `{"source": 技能原文}`，格式见下一节）；学生的 `GET` 只返回已启用项且不含 `source_text`，写操作 403 |
| `GET /api/conversations`、`POST /api/conversations` | 本人会话列表 / 新建会话（请求体 `{"agent_id": N}`，标题为「新的提问」） |
| `GET /api/conversations/{id}` | 会话详情：会话字段加 `messages`（按时间顺序，助手消息带 `citations` 与 `skill_name`） |
| `POST /api/conversations/{id}/stream` | 流式提问（请求体 `{"question": "..."}`，2–500 字）：SSE 依次返回 `meta`、多个 `delta`、`done`，失败发 `error`；生成中再次提交同一会话返回 409 |

他人 / 他班 / 未启用 / 不存在的对象统一 404；技能文件不合法 400（错误含行号或字段名）。试调：

```bash
# 教师创建技能（技能文件格式见下一节）
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "$(python3 -c 'import json;print(json.dumps({"source":open("physics-skill.txt").read()}))')" \
  $API/api/skills
# → 201 {"id":3}

# 新建会话（agent_id 用已启用智能体的编号，可从 GET /api/agents 获知）
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"agent_id":1}' $API/api/conversations
# → 201 {"id":5,"agent_id":1,"title":"新的提问","created_at":"...","updated_at":"..."}

# 流式提问（-N 关闭 curl 缓冲；meta 含本轮出处，delta 为逐段文字）
curl -N -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"question":"这道方程怎么解？"}' $API/api/conversations/5/stream
```

### 技能文件格式

技能是单个纯文本文件（总长 ≤ 8000 字符）：先写「字段：值」行（全角、半角冒号均可；`#` 开头的整行忽略），空行之后是说明正文（≤ 2000 字符，`#` 与空行原样保留）。

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| 名称 | 是 | ≤ 32 字，班内唯一 |
| 适用条件 | 是 | ≤ 120 字，按中英文逗号、顿号、分号、竖线分词；提问命中词的个数就是得分，得分最高（同分取编号较小）的技能被选中，至多一个 |
| 资料 | 否 | ≤ 32 字；填写后本轮引用只保留标题包含该词的材料 |
| 工具 | 否 | 1–3 个引用，用全角或半角逗号分隔：`knowledge_search`（内置检索），或 `工具名@服务器名`（MCP 工具，服务器须已连接且该工具在其工具清单中）；保存时逐项校验，出错会指出具体取值 |
| 启用 | 是 | 「是」或「否」；停用后不再参与路由 |

示例（保存为 `physics-skill.txt`，可直接用上面的 curl 创建）：

```text
# 物理技能
名称：物理实验引导
适用条件：物理，实验、受力；physics
资料：物理
工具：knowledge_search
启用：是

先让学生画出受力分析图并说出判断依据，再逐步追问；不直接给出最终答案。
```

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

## MCP 工具服务器

仓库自带示例 MCP 服务器 `mcp-server/`（Compose 服务名 `mcp-demo`，容器内监听 8090，宿主为 `127.0.0.1:8090`）：只用 Go 标准库实现 JSON-RPC 2.0 与 Streamable HTTP，不引入第三方 MCP SDK。它提供两个工具：`formula_hint`（按主题给公式与解题思路提示）与 `unit_convert`（长度、质量、时间单位换算）。

`api` 与 `mcp-demo` 共用 `.env` 中的 `MCP_AUTH_TOKEN`：api 用它为 A 班幂等预置 `campus-tools`，并在调用工具时放进 `Authorization: Bearer`；`mcp-demo` 以常量时间比较校验。令牌为空时 `mcp-demo` 拒绝启动，api 不预置 `campus-tools`（api 本身照常启动）。令牌只在运行时注入，不进入镜像层、接口响应或日志。

```bash
# 健康检查（无需令牌）
curl -s http://127.0.0.1:8090/health
# → {"status":"ok"}

# 用同一个令牌列出工具（先把 .env 导入当前 shell，令牌值不要写进命令或文档）
curl -s -X POST http://127.0.0.1:8090/mcp \
  -H "Authorization: Bearer $MCP_AUTH_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
# → {"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"formula_hint",...},{"name":"unit_convert",...}]}}
```

教师使用流程：登录后进入「工具服务器」→ 登记（名称如 `campus-tools`（同班唯一），地址 `http://mcp-demo:8090`，不要以 `/mcp` 结尾）→「连接」读取工具清单 → 审阅工具说明与参数结构 → 在技能文件的「工具」字段写 `formula_hint@campus-tools` 并把技能绑到智能体。学生看不到该页面，接口仍返回 403；他班服务器与不存在的编号返回相同的 404。

- 技能「工具」支持 1–3 个引用：`knowledge_search` 或 `工具名@服务器名`，引用在保存时校验（服务器属于本班、已连接、工具在缓存中、函数名不超过 64 字符）。
- 只有本轮命中的技能含 MCP 引用时才进入工具阶段：宿主只把仍可用的工具交给模型，逐次校验工具名与参数，最多 2 轮 / 3 次调用 / 20 秒；工具失败会回传给模型而不是中断回答。
- 每次调用（`ok` / `denied` / `error`）写入审计 `mcp_tool_calls`，只记工具、服务器、状态、原因、耗时与归属，不记参数、结果或令牌；教师可在「工具服务器」页查看，接口为 `GET /api/mcp/calls?limit=50`。
- 服务器断开后，其工具在技能保存与运行时都立即不可用：运行中的调用记为 `denied` / `not_connected`，助手回答照常完成。
- 接口文档：人类可读的 Swagger UI 在 `/swagger/`，机器可读的 OpenAPI 3 文档在 `GET /openapi.json`（无需认证，不含密钥）。

### 升级说明（迭代 5）

登记地址新增了目标限制：主机部分不能是本机、云元数据地址或编排内的其它服务。

- 被禁止的主机名（不区分大小写）：`localhost`、`db`、`api`、`qdrant`、`web`。
- 被禁止的地址段：回环（`127.0.0.0/8`、`::1`）、链路本地（`169.254.0.0/16`、`fe80::/10`，含云元数据地址 `169.254.169.254`）、未指定地址（`0.0.0.0`、`::`）。
- `mcp-demo` 与其它私有网段地址照常允许（本编排的 MCP 服务就在私有网段里）。

如果之前用 `http://localhost:...` 或 `http://127.0.0.1:...` 登记过服务器，升级后登记与连接都会返回 400 / 502。处理办法是删除该记录，改用服务名重新登记（本编排内即 `http://mcp-demo:8090`）。同一个限制也在连接与调用时按解析后的实际 IP 再判一次，所以把域名解析到上面这些地址同样连不上。

其它行为变化：令牌长度按 UTF-8 字节计（1–512 字节）；一次工具阶段最多 10 个调用请求，只有候选集内的调用计入 3 次上限，第 11 个请求起合并为一条 `denied` / `limit` 记录；工具结果中出现的服务器令牌会被替换为 `***`。

## 接口测试（apitest）

`apitest/` 用 Python 3 + `requests` + `pytest` + `pytest-html` + `pymysql` 按 URL 组织接口用例，创建类用例会回查数据库核对字段，运行前清理上次残留、运行后比对预置数据行数。

```bash
cd apitest
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
cp .env.example .env
# 编辑 .env（只写变量值，不要提交）：开发编排下 API_BASE_URL=http://localhost:8081，
# APITEST_DB_HOST=127.0.0.1、APITEST_DB_PORT=3307、APITEST_DB_USER/NAME，
# 以及 APITEST_DB_PASSWORD、MCP_AUTH_TOKEN 与三个预置账号口令
.venv/bin/python run_all.py; echo "exit=$?"
```

- 用例目录与接口路径一致（`cases/api/login/POST.py` 对应 `POST /api/login`，路径参数写成 `_id_`），跨接口流程在 `cases/e2e/`。
- 报告生成在 `apitest/reports/report-<时间戳>.html`（单文件，可直接用浏览器打开）；退出码 0 表示全部通过，非 0 表示有失败或配置错误。
- 缺少必需环境变量时不会开始跑用例，而是列出缺少的变量名并非零退出。
- 依赖对话网关产出回答的用例标记为 `gateway`：网关不可用（流式接口在事件流开始前返回 503）时显示 skipped 并注明原因，不计为通过。
- 测试数据统一使用 `apitest_<时间戳>`（MCP 服务器名受命名约束，用 `apitest-<时间戳>`），结束后自动清理；`campus-tools` 若被临时连接或断开，会在用例结束时恢复原状态。

## 预置账号

| 用户名 | 角色 | 班级 | 密码 |
| --- | --- | --- | --- |
| teacher_a | 教师 | A 班 | 来自 `.env` 的 `SEED_TEACHER_A_PASSWORD` |
| student_a1 | 学生 | A 班 | 来自 `.env` 的 `SEED_STUDENT_A1_PASSWORD` |
| student_b1 | 学生 | B 班 | 来自 `.env` 的 `SEED_STUDENT_B1_PASSWORD` |

首次启动会自动建表，写入以上账号与两班的示例材料（班 B 有两条，其中一条只含 B 班独有内容，用于验证跨班隔离），并为材料生成切片与向量索引；种子与索引过程幂等，重启不会重复插入或覆盖已上传内容。索引依赖不可用时种子照常完成，预置切片标记为 `failed`，重建即可恢复。除账号与材料外，种子还会为 A 班写入两份预置技能（「数学解题引导」「语文解题引导」）与每班一个已启用的默认智能体；A 班默认智能体绑定这两份技能与首次种子时已有的 A 班材料。技能与智能体的种子同样按 `seed_key` 幂等，改名或改提示词后重启不会被覆盖。配置了 `MCP_AUTH_TOKEN` 时，种子还会为 A 班幂等登记一台 `campus-tools` 服务器（初始 `disconnected`），按 `seed_key` 判断，重复启动不会产生第二条，也不会覆盖教师修改过的地址或连接状态。

## 已知行为与限制

- **token 存放在 `sessionStorage`**：前端能读到 token 才能把它放进 `Authorization` 头，这是 Bearer 方案的固有代价——页面一旦出现 XSS，token 就可能被读走。因此只把 token 存进 `sessionStorage`（关闭标签页即清除，且不跨标签页共享），不存 `localStorage`，不存用户名/角色；服务端再用 `Cache-Control: no-store` 与严格 CSP（`script-src 'self'`，无 `unsafe-inline`/`unsafe-eval`）收敛风险。发现泄露时更换 `JWT_SECRET` 即可让全部 token 失效。
- **班级与角色每次请求回库读取**：JWT 只含 `sub` 等身份无关信息，角色与班级在每次请求时按 `sub` 从数据库读取——改库里的 `class_id` / `role` 下一次请求即生效，删除用户后其 token 立即 401。
- **从迭代 1 升级需重新登录**：旧的会话 Cookie 不再被接受（`sessions` 表保留但已不再读写），`SESSION_SECRET`/`SESSION_TTL` 由 `JWT_SECRET`/`JWT_TTL` 取代。
- **升级到本版本后需重新登录**：JWT 载荷精简为 `sub`/`jti`/`iat`/`exp`/`iss`，旧格式 token（含 `role`/`class_id` 等）一律 401，前端会清除后回到登录页，不做兼容。
- **跨班访问**：材料相关接口（列表 / 详情 / 下载 / 重建）对不存在的 ID 与他班 ID 返回相同的 404；智能体 / 技能 / 会话同样如此（他班、他人、未启用或不存在的对象返回相同的 404）；检索与问答对他班内容返回 200 空结果（`message` 为「资料中未找到相关内容」，`hits`/`citations` 为空），不区分"不存在"与"不属于你"。
- **检索依赖的降级**：Qdrant 或嵌入网关不可用时 `keyword` 检索仍可用（只查 MySQL 全文索引），`vector`、`hybrid` 与 `/api/ask` 返回 **503**；对话网关不可用时 `/api/ask` 返回 503。错误响应不暴露依赖地址与密钥。
- **助手的作答与降级**：会话提问经 `POST /api/conversations/{id}/stream`（SSE）逐段返回；Qdrant / 嵌入网关 / 对话网关不可用都在流开始前返回 503 且不落助手消息；检索落空时助手仍会调用模型（与 `/api/ask` 的「无依据不调用」口径不同），超出本轮出处范围的 `[n]` 标注会在推送前被剥掉。
- **技能路由至多命中一个**：把「适用条件」按标点分词后与提问逐词匹配，命中最多者胜出（同分取编号较小），全部零分则不命中；停用或从智能体解绑后立即不再命中。
- **MCP 工具调用**：只有命中技能的「工具」含 `工具名@服务器名` 时才进入工具阶段；工具定义只来自该技能的引用且以当前连接缓存为准，模型提出候选集之外的工具、非法参数或超过 2 轮 / 3 次调用 / 20 秒上限时记为 `denied` / `error`，回答仍会完成。审计不含参数、结果与令牌；服务器断开后保存引用会被拒绝，运行中的调用记为 `denied` / `not_connected`。
- **更换嵌入模型需删集合并重建**：向量与模型绑定，`EMBEDDING_DIM` 必须与集合一致。更换模型或维度后旧向量与新向量不可比——需删除 Qdrant 集合（或数据卷）并对材料逐个重建索引；维度不符时 api 按依赖不可用处理（`vector`/`hybrid` 503，日志写明维度不符），`keyword` 不受影响。集合在首次索引时自动创建。
- **单实例部署**：当前只支持单个 api/db 实例（登录限流计数在内存中，重启会清零），不做多副本、公网证书与 CI/CD，详见 `openspec/changes/add-auth-rbac-class-knowledge/proposal.md` 的 Non-goals。
- **MySQL 首次启动较慢**：db 容器首次初始化建表可能需要几十秒，此时访问 web 可能短暂看到 502，等待 `docker compose ps` 中 db 变为 `healthy` 后即可正常访问。
- **修改端口**：如需更换对外端口，在 `.env` 中设置 `WEB_PORT` 后重新 `docker compose up -d` 即可，无需改动 compose 文件。
- **迭代 2 不做**：重排序（结果只按 RRF 融合）、跨班检索（含教师）、`/api/ask` 的流式输出（保持一次性返回 JSON；逐段输出由助手的 `/api/conversations/{id}/stream` 提供）、多轮记忆与 Agent（已由 `add-tutor-agents-skills-streaming` 的智能体与会话历史提供）、PDF/Word 等新文件类型（仍只支持 `.txt` / `.md`）、检索与问答的前端页面（接口可用，页面已由迭代 3 的 `add-knowledge-qa-ui` 提供）、异步索引队列（上传请求内同步完成索引）。
