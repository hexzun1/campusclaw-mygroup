# auth-upload Specification

## Purpose

让教师与学生以各自角色登录 CampusClaw，以班级作为数据边界隔离教学材料；教师上传的内容解析后写入知识库，本班成员可查看与下载；越权上传、跨班访问与未登录访问在服务端被拒绝。

## Requirements

### Requirement: 登录与会话

系统 MUST 提供教师（teacher）、学生（student）两类角色的账号密码登录。登录成功后，系统 MUST 在响应 JSON 中返回一个 JWT（字段 `token`），MUST NOT 设置会话 Cookie。token MUST 使用 HS256 签名，载荷 MUST 包含 `user_id`、`username`、`role`、`class_id`、`iss`（值为 `campusclaw-api`）、`iat`、`exp`（签发后 24 小时）与每个 token 唯一的 `jti`，MUST NOT 包含口令或口令哈希。除 `POST /api/login` 与 `GET /health` 外，所有 `/api` 请求 MUST 携带 `Authorization: Bearer <token>`；token 缺失、格式错误、签名错误、算法不是 HS256、已过期、`iss` 不符、载荷缺少必需项，或其 `jti` 已被吊销，MUST 一律返回 401，且响应体 MUST NOT 包含业务数据。放在 query、Cookie 或请求体中的 token MUST 被忽略。`role` 与 `class_id` MUST 只取自已验签的 token。登出 MUST 使该 token 的 `jti` 立即失效，其它 token 不受影响。系统 MUST 提供返回当前身份的接口。本规约中的"会话"指一次有效的登录状态，即已验签、未过期、未吊销的 token；"会话班级""会话角色"指该 token 中的 `class_id` 与 `role`。

#### Scenario: 登录成功

- **WHEN** teacher_a、student_a1 或 student_b1 使用正确口令调用 `POST /api/login`
- **THEN** 响应 MUST 为 200，JSON 中 MUST 含 `token`，且响应 MUST NOT 设置任何会话 Cookie
- **AND** token 头部的算法 MUST 为 HS256，载荷 MUST 含 `user_id`、`username`、`role`、`class_id`、`iss=campusclaw-api`、`iat`、`exp`、`jti`，且 `exp - iat` MUST 等于 24 小时
- **AND** 随后以该 token 作 Bearer 调用 `GET /api/me` MUST 返回该用户的用户名、角色与所属班级

#### Scenario: 登录失败提示统一

- **WHEN** 分别使用"存在的用户名 + 错误口令"与"不存在的用户名"调用 `POST /api/login`
- **THEN** 两种请求 MUST 都返回 401，且响应体完全相同（统一文案"用户名或密码错误"）
- **AND** 响应 MUST NOT 含 token，MUST NOT 设置任何 Cookie

#### Scenario: 连续失败触发限流

- **WHEN** 同一用户名在同一 IP 连续登录失败达到配置阈值后，再用正确口令登录
- **THEN** 在锁定期内该请求 MUST 被拒绝
- **AND** 响应 MUST 与口令错误时完全相同，不提示已被锁定，且 MUST NOT 含 token

#### Scenario: 重新登录签发新 token

- **WHEN** 已持有有效 token 的用户再次登录成功
- **THEN** 系统 MUST 签发 `jti` 与原 token 不同的新 token
- **AND** 原 token 在其自身过期或被登出吊销之前 MUST 仍然有效

#### Scenario: 登出后旧 token 失效

- **WHEN** 已登录用户以 Bearer 调用 `POST /api/logout`，之后仍用原 token 调用 `GET /api/me`
- **THEN** 登出 MUST 成功（200）
- **AND** 使用原 token 的任何 `/api` 请求 MUST 返回 401，包括再次调用 `POST /api/logout`
- **AND** 同一用户在登出前另行签发的其它 token MUST 仍然有效

#### Scenario: 未登录访问受保护接口

- **WHEN** 不带 `Authorization` 头调用 `GET /api/me`、`POST /api/logout`、`GET /api/materials`、`GET /api/materials/{id}`、`GET /api/materials/{id}/file` 或 `POST /api/materials`
- **THEN** 每个请求 MUST 返回 401
- **AND** 响应体 MUST NOT 包含任何材料标题、正文或文件内容

#### Scenario: 签名错误的 token 被拒绝

- **WHEN** 使用另一把密钥签发、或载荷被篡改（如把 `role` 改成 `teacher`、把 `class_id` 改成班级 B）而签名未重算的 token 调用 `GET /api/me`
- **THEN** 响应 MUST 为 401

#### Scenario: 过期的 token 被拒绝

- **WHEN** 使用 `exp` 已过的 token 调用 `GET /api/me`
- **THEN** 响应 MUST 为 401

#### Scenario: 发行方或算法不符的 token 被拒绝

- **WHEN** 使用 `iss` 不是 `campusclaw-api` 的 token，或头部算法为 `none`、`HS512` 的 token 调用 `GET /api/me`
- **THEN** 每次响应 MUST 为 401

#### Scenario: 载荷缺项的 token 被拒绝

- **WHEN** 使用签名正确但缺少 `jti`、`exp`、`role` 或 `class_id` 之一的 token 调用 `GET /api/me`
- **THEN** 每次响应 MUST 为 401

#### Scenario: token 只认 Authorization 头

- **WHEN** 把一个有效 token 放在 query 参数、Cookie 或请求体中，而不带 `Authorization` 头，调用 `GET /api/me`
- **THEN** 响应 MUST 为 401

#### Scenario: 登录与健康检查无需 token

- **WHEN** 不带 `Authorization` 头调用 `POST /api/login` 与 `GET /health`
- **THEN** 它们 MUST 按各自的规则响应，MUST NOT 因缺少 token 而返回 401

#### Scenario: 校验依赖的数据库不可用时返回 503

- **WHEN** db 服务停止后，持有有效 token 的用户调用 `GET /api/materials`
- **THEN** 响应 MUST 为 503，MUST NOT 为 401
- **AND** db 恢复后同一 token MUST 仍然有效

### Requirement: 角色权限

系统 MUST 仅依据已验签 token 中的角色授权：教师可上传、查看、下载本班材料；学生只能查看和下载本班材料。客户端在表单、Header、query 中声明的角色 MUST 被忽略。

#### Scenario: 学生上传被拒绝

- **WHEN** student_a1 携带有效会话向 `POST /api/materials` 提交一份合法的 `.md` 文件
- **THEN** 响应 MUST 为 403
- **AND** 请求前后 materials 与 knowledge_entries 两表的行数 MUST 相同
- **AND** 上传目录 MUST NOT 新增文件

#### Scenario: 客户端伪造角色无效

- **WHEN** student_a1 在上传请求的表单或 Header 中附带 `role=teacher`
- **THEN** 响应 MUST 仍为 403，数据库与上传目录均无变化

#### Scenario: 篡改 token 提升角色无效

- **WHEN** 把 student_a1 的 token 载荷中的 `role` 改成 `teacher`（签名未重算）后提交上传请求
- **THEN** 响应 MUST 为 401，数据库与上传目录均无变化

#### Scenario: 教师上传被允许

- **WHEN** teacher_a 携带有效会话提交一份合法的 `.md` 文件
- **THEN** 响应 MUST 为 201 并返回新材料的 ID 与标题

### Requirement: 班级隔离

班级是数据边界，只取自已验签的 token。所有材料与知识库的读写 MUST 在服务端按会话班级校验；请求中携带的班级参数 MUST 被忽略；跨班访问与访问不存在的记录 MUST 返回相同的 404 响应。

#### Scenario: 列表只含本班

- **WHEN** student_a1 调用 `GET /api/materials`
- **THEN** 返回的每条记录 MUST 属于班级 A
- **AND** MUST NOT 出现标题含「B 班」的预置材料

#### Scenario: 篡改班级参数无效

- **WHEN** student_a1 调用 `GET /api/materials?class_id=<班级 B 的 ID>`，或在 Header、请求体中附带班级 B 的 ID
- **THEN** 返回结果 MUST 与不带该参数时相同，只含班级 A 的记录

#### Scenario: 篡改 token 中的班级无效

- **WHEN** 把 student_a1 的 token 载荷中的 `class_id` 改成班级 B 的 ID（签名未重算）后调用 `GET /api/materials`
- **THEN** 响应 MUST 为 401，响应体 MUST NOT 含任何班级 B 的材料

#### Scenario: 跨班按 ID 访问返回 404

- **WHEN** teacher_a 或 student_a1 调用 `GET /api/materials/{B 班材料 ID}` 或 `GET /api/materials/{B 班材料 ID}/file`
- **THEN** 响应 MUST 为 404
- **AND** 响应体 MUST 与请求一个不存在的 ID 时完全相同，不含 B 班材料的标题、正文或文件内容

#### Scenario: 上传归属会话班级

- **WHEN** teacher_a 上传时在表单中附带班级 B 的 ID
- **THEN** 新建的材料与知识库条目 MUST 归属班级 A
- **AND** student_b1 的列表 MUST NOT 出现该材料

### Requirement: 材料上传与知识库入库

教师上传的材料 MUST 经服务端校验后，在同一 MySQL 事务中写入材料表、知识库表与知识库切片表（`knowledge_chunks`），并关联会话班级；事务内任何失败 MUST 不留下数据库记录或磁盘文件。事务提交后，服务端 MUST 为切片生成向量并写入向量库；该步骤失败 MUST NOT 回滚材料与切片，只将受影响切片的索引状态标记为 `failed`。上传 MAY 携带切分策略参数（见 `knowledge-retrieval` 的「切分策略」），缺省为 `auto`；参数不合法 MUST 返回 400 且不留下任何记录或文件。首版只支持 `.txt` 与 `.md`，大小上限由配置决定。

#### Scenario: 上传成功后多表入库且本班可见

- **WHEN** teacher_a 上传一份内容为合法 UTF-8 的 `.md` 文件
- **THEN** materials 表 MUST 新增 1 行，knowledge_entries 表 MUST 新增 1 行，knowledge_chunks 表 MUST 新增至少 1 行，它们都属于班级 A 且互相关联
- **AND** knowledge_entries 中的正文 MUST 与文件内容一致
- **AND** teacher_a 与 student_a1 再调用 `GET /api/materials` MUST 看到该条新记录

#### Scenario: 向量索引失败不回滚材料

- **WHEN** teacher_a 上传合法文件，事务已提交，但向量库或嵌入网关在索引时失败
- **THEN** 响应 MUST 为 201，materials、knowledge_entries、knowledge_chunks 中的记录与磁盘文件 MUST 保留
- **AND** 受影响切片的索引状态 MUST 为 `failed`

#### Scenario: 切分参数不合法

- **WHEN** teacher_a 上传合法文件，但切分策略未知或参数越界
- **THEN** 响应 MUST 为 400
- **AND** 三张表的行数与上传目录 MUST 保持不变

#### Scenario: 不支持的扩展名

- **WHEN** teacher_a 上传 `.exe` 或 `.pdf` 文件
- **THEN** 响应 MUST 为 400
- **AND** 各表行数与上传目录 MUST 保持不变

#### Scenario: 文件超过大小上限

- **WHEN** teacher_a 上传的文件大小超过配置的上限
- **THEN** 响应 MUST 为 413
- **AND** 各表行数与上传目录 MUST 保持不变

#### Scenario: 空文件或非 UTF-8 内容

- **WHEN** teacher_a 上传空的 `.txt` 文件，或内容不是合法 UTF-8 的 `.txt` 文件
- **THEN** 响应 MUST 为 400
- **AND** 各表行数与上传目录 MUST 保持不变

#### Scenario: 客户端文件名不参与存储路径

- **WHEN** teacher_a 上传文件名为 `../../etc/passwd.md` 的文件
- **THEN** 文件 MUST 以服务端生成的名称保存在上传目录内
- **AND** 客户端文件名只 MAY 用作展示标题

### Requirement: 材料详情与文件访问

材料详情与原文件 MUST 只能通过经过会话与班级校验的接口获取；上传目录 MUST NOT 作为静态资源对外提供；响应 MUST NOT 暴露服务器磁盘路径。

#### Scenario: 本班详情可读

- **WHEN** student_a1 调用 `GET /api/materials/{A 班材料 ID}`
- **THEN** 响应 MUST 为 200，包含标题、班级、上传时间与知识库正文
- **AND** MUST NOT 包含服务器上的文件路径或存储名

#### Scenario: 本班文件可下载

- **WHEN** student_a1 调用 `GET /api/materials/{A 班材料 ID}/file`
- **THEN** 响应 MUST 为 200，内容与上传的原文件一致

#### Scenario: 猜测上传路径取不到文件

- **WHEN** 任何客户端（无论是否登录）直接请求 `/uploads/` 下的路径
- **THEN** 系统 MUST NOT 返回任何上传文件的内容

### Requirement: 预置核心数据

系统首次启动时 MUST 自动创建表结构并写入可验收的样本数据，预置材料 MUST 与上传的材料一样生成知识库切片；种子过程 MUST 幂等。预置切片的向量索引 MUST 尽力而为：嵌入网关或向量库不可用时种子 MUST 仍然完成，相应切片标记为 `failed`，并可通过重建索引恢复。

#### Scenario: 双班与三个账号

- **WHEN** 首次执行 `docker compose up --build`
- **THEN** 数据库中 MUST 存在班级 A、班级 B
- **AND** MUST 存在 teacher_a（教师，A 班）、student_a1（学生，A 班）、student_b1（学生，B 班），口令来自环境变量并可登录

#### Scenario: 两班材料可区分

- **WHEN** 种子完成
- **THEN** MUST 各有至少一条标题含「A 班」「B 班」的材料，分别属于对应班级
- **AND** 每条都关联一条知识库正文，并至少有一个属于同一班级的切片

#### Scenario: 重复启动不重复插入

- **WHEN** 已有上传数据时再次重启 api 服务
- **THEN** 用户、班级与预置材料 MUST NOT 被重复插入
- **AND** 预置材料的切片 MUST NOT 被重复生成
- **AND** 已上传的材料 MUST NOT 被覆盖或删除

#### Scenario: 索引依赖不可用时种子仍完成

- **WHEN** 首次启动时嵌入网关或向量库不可用
- **THEN** 种子 MUST 完成，预置材料与切片 MUST 存在
- **AND** 预置切片的索引状态 MUST 为 `failed`，`keyword` 检索 MUST 仍能命中

### Requirement: 口令与密钥安全

口令 MUST 以 bcrypt 哈希存储；JWT 签名密钥（`JWT_SECRET`）、数据库凭据与预置账号口令 MUST 只来自环境变量，缺失时服务 MUST 启动失败；`JWT_SECRET` 少于 32 个字符 MUST 视为不合格并使服务启动失败；仓库 MUST NOT 包含真实密钥。

#### Scenario: 库中无明文口令

- **WHEN** 查询 users 表的口令字段
- **THEN** 每个值 MUST 以 bcrypt 前缀（如 `$2a$` 或 `$2b$`）开头
- **AND** MUST NOT 等于任何预置账号的明文口令

#### Scenario: 缺少必需配置时启动失败

- **WHEN** 未提供 `JWT_SECRET` 或数据库口令等必需环境变量就启动 api
- **THEN** api MUST 启动失败，并输出缺失的变量名
- **AND** MUST NOT 使用内置默认值继续运行

#### Scenario: JWT_SECRET 过短时启动失败

- **WHEN** 提供的 `JWT_SECRET` 少于 32 个字符就启动 api
- **THEN** api MUST 启动失败，并输出 `JWT_SECRET` 不合格的原因
- **AND** 输出 MUST NOT 含该密钥的值

#### Scenario: token 载荷不含口令信息

- **WHEN** 解码任一登录返回的 token 载荷
- **THEN** 载荷 MUST NOT 含口令、口令哈希或任何密钥

#### Scenario: 仓库不含真实密钥

- **WHEN** 检查已提交到 Git 的文件
- **THEN** MUST 存在 `.env.example` 且只含变量名与占位值
- **AND** MUST NOT 存在 `.env` 文件或任何真实口令、密钥

### Requirement: Compose 部署与健康检查

系统 MUST 以 Docker Compose 启动 web、api、db、qdrant 四个服务；只有 web MUST 映射宿主端口；数据库、向量库数据与上传文件 MUST 持久化；`GET /health` MUST 无需登录且只表示进程存活。

#### Scenario: 按 README 从零启动

- **WHEN** 第三方克隆仓库，按 README 复制 `.env.example` 为 `.env` 并填值，执行 `docker compose up --build -d`
- **THEN** 浏览器访问 `http://localhost:8080` MUST 显示登录页
- **AND** `GET /health` MUST 返回 200 与 `{"status":"ok"}`，且不要求登录

#### Scenario: 只暴露 web 端口

- **WHEN** 服务全部启动后在宿主机检查端口
- **THEN** 只有 web 的端口 MUST 可访问
- **AND** 数据库端口（3306）、向量库端口（6333、6334）与 api 端口 MUST NOT 映射到宿主机

#### Scenario: 重建后数据仍在

- **WHEN** teacher_a 上传一条材料并完成索引后执行 `docker compose down`，再执行 `docker compose up -d`（不删除数据卷）
- **THEN** 预置账号 MUST 仍可登录
- **AND** 该材料 MUST 仍出现在本班列表中且可下载
- **AND** 以 `vector` 模式检索该材料的内容 MUST 仍能命中

#### Scenario: 数据库不可用时返回 503

- **WHEN** db 服务停止后，已登录用户调用 `GET /api/materials`
- **THEN** 响应 MUST 为 503
- **AND** MUST NOT 返回 401 或清除用户的会话 Cookie

### Requirement: 前端页面行为

前端 MUST 通过同源 `/api` 调用后端。登录成功后前端 MUST 把 token 保存在 `sessionStorage` 中，并在除登录外的每个 API 请求上加 `Authorization: Bearer <token>` 头；除 token 外 MUST NOT 在任何 Web Storage 中保存会话 ID、用户名、角色等身份信息，MUST NOT 把 token 保存在 `localStorage`，MUST NOT 依赖 Cookie。身份以 `GET /api/me` 为准；界面只决定"显示什么"，不承担访问控制。原文件下载 MUST 通过带 `Authorization` 头的请求完成，MUST NOT 依赖不带该头的链接导航。登出与收到 401 时前端 MUST 清除 token。

#### Scenario: 刷新后以服务端身份为准

- **WHEN** 已登录用户刷新材料页
- **THEN** 前端 MUST 读取 `sessionStorage` 中的 token 并调用 `GET /api/me` 确认身份，有效则停留在材料页，无效则清除 token 并跳转登录页
- **AND** `localStorage` MUST 不含 token，`sessionStorage` 中除 token 外 MUST 不含会话 ID、用户名或角色

#### Scenario: 请求携带 Bearer 头且不依赖 Cookie

- **WHEN** 已登录用户在材料页执行列表、搜索、详情、上传等操作
- **THEN** 除登录外的每个 `/api` 请求 MUST 带 `Authorization: Bearer <token>` 头
- **AND** 浏览器中 MUST 不存在本站的会话 Cookie，功能 MUST 不受影响

#### Scenario: 收到 401 回到登录页

- **WHEN** 材料页中的任一接口调用返回 401
- **THEN** 前端 MUST 清除 token、清空当前用户状态并跳转登录页

#### Scenario: 登出清除 token

- **WHEN** 用户点击登出
- **THEN** 前端 MUST 调用 `POST /api/logout`、清除 token 并回到登录页
- **AND** 之后浏览器后退，材料页 MUST 无法取到数据并回到登录页

#### Scenario: 下载经过鉴权

- **WHEN** 已登录用户点击某份本班材料的下载
- **THEN** 下载请求 MUST 带 `Authorization` 头，保存下来的文件内容 MUST 与上传的原文件一致
- **AND** 不带 `Authorization` 头直接请求同一下载地址 MUST 返回 401

#### Scenario: 上传入口仅教师可见

- **WHEN** student_a1 登录后打开材料页
- **THEN** 页面 MUST NOT 显示上传入口
- **AND** teacher_a 登录后打开材料页 MUST 显示上传入口

#### Scenario: 登录失败统一提示

- **WHEN** 用户在登录页输入错误口令并提交
- **THEN** 页面 MUST 显示"用户名或密码错误"，不区分用户是否存在

### Requirement: 前端页面体验

材料页 MUST 提供基本的浏览体验：本班筛选、Markdown 渲染、深浅色主题与操作反馈。这些体验不替代任何服务端校验。

#### Scenario: 本班标题筛选

- **WHEN** 用户在材料页搜索框输入关键词
- **THEN** 列表 MUST 只显示本班中标题包含该关键词的材料
- **AND** MUST NOT 出现其他班级的材料

#### Scenario: Markdown 详情渲染且不执行脚本

- **WHEN** 用户打开一份 `.md` 材料的详情，其内容包含标题、列表、表格与 `<script>` 标签
- **THEN** 标题、列表与表格 MUST 按 GFM 格式化显示
- **AND** `<script>` 内容 MUST NOT 被执行

#### Scenario: 深浅色主题切换

- **WHEN** 用户点击主题切换按钮
- **THEN** 页面 MUST 在浅色与深色主题之间切换

#### Scenario: 上传结果反馈

- **WHEN** 教师上传成功或失败
- **THEN** 页面 MUST 显示对应的成功或失败提示，失败时说明原因（格式不支持、文件过大或内容无效）
- **AND** 成功后列表 MUST 自动刷新并出现新材料

### Requirement: 浏览器端防护响应头

token 由前端脚本持有，因此系统 MUST 降低脚本注入与响应被缓存重放的风险：所有 `/api` 响应（包括 401、404、503 等错误响应）MUST 带 `Cache-Control: no-store`；前端页面响应 MUST 带内容安全策略头，脚本与连接来源 MUST 限制为同源，且 MUST NOT 允许内联脚本与 `eval`，MUST 禁止 `object` 与被其它站点嵌入框架。这些措施 MUST NOT 破坏登录、列表、搜索、详情、上传、下载与主题切换功能。

#### Scenario: API 响应不可缓存

- **WHEN** 任意 `/api` 请求得到响应（成功或 401、404、503）
- **THEN** 响应头 MUST 含 `Cache-Control: no-store`

#### Scenario: 页面带内容安全策略

- **WHEN** 请求前端页面
- **THEN** 响应头 MUST 含内容安全策略，其 `script-src` MUST 只允许同源，且 MUST NOT 含 `unsafe-inline` 与 `unsafe-eval`
- **AND** MUST 禁止 `object` 并限制被嵌入框架

#### Scenario: 策略下功能正常

- **WHEN** 在带该策略的页面上依次执行登录、列表、搜索、详情、上传、下载、主题切换、登出
- **THEN** 每项功能 MUST 正常完成
- **AND** 浏览器控制台 MUST 无内容安全策略违规报告
