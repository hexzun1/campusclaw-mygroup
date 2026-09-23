## Purpose

让教师与学生以各自角色登录 CampusClaw，以班级作为数据边界隔离教学材料；教师上传的内容解析后写入知识库，本班成员通过列表查看；越权上传、跨班访问与未登录访问在服务端被拒绝。

## ADDED Requirements

### Requirement: 用户登录

系统 MUST 提供教师（teacher）、学生（student）两类角色的账号密码登录；登录成功后 MUST 建立服务端会话；未登录用户访问受保护页面 MUST 被引导到登录页，且 MUST NOT 泄露任何业务数据。

#### Scenario: 教师与学生登录成功

- **WHEN** 用户使用有效账号与密码登录（预置账号含教师 A、学生 A1、学生 B1）
- **THEN** 登录 MUST 成功并建立服务端会话
- **AND** 会话中 MUST 记录用户标识、角色（teacher 或 student）与所属班级（班级 A 或 B）
- **AND** 用户随后访问材料列表 MUST 得到 HTTP 200

#### Scenario: 错误密码登录失败

- **WHEN** 用户提供已存在的用户名但错误的密码
- **THEN** 登录 MUST 失败，页面停留在登录页并提示"用户名或密码错误"
- **AND** 系统 MUST NOT 建立有效会话
- **AND** 响应 MUST NOT 区分"用户不存在"与"密码错误"，也 MUST NOT 返回密码哈希

#### Scenario: 未登录访问受保护页面

- **WHEN** 未携带有效会话的用户请求受保护页面（如材料列表）
- **THEN** 系统 MUST 返回 HTTP 302 重定向到登录页
- **AND** 响应 MUST NOT 包含任何材料标题、正文或文件路径

#### Scenario: 未登录调用受保护 API

- **WHEN** 未携带有效会话的请求调用受保护 API（如材料列表 API 或上传 API）
- **THEN** 系统 MUST 返回 HTTP 401
- **AND** 响应 MUST NOT 包含任何材料数据

#### Scenario: 登出后会话失效

- **WHEN** 已登录用户执行登出后再次访问材料列表
- **THEN** 系统 MUST 将其重定向到登录页

### Requirement: 角色权限

系统 MUST 按会话中的角色在服务端授权：教师可上传本班材料；学生对本班材料只读；学生调用上传接口 MUST 被拒绝。

#### Scenario: 学生上传被拒绝

- **WHEN** 学生 A1 以有效会话向材料上传接口提交一份合法文件
- **THEN** 系统 MUST 返回 HTTP 403
- **AND** 材料表与知识库表的记录数 MUST 与请求前相同
- **AND** 上传目录中 MUST NOT 出现因该请求产生的新文件

#### Scenario: 教师上传被允许

- **WHEN** 教师 A 以有效会话向材料上传接口提交一份支持格式的文件
- **THEN** 系统 MUST 返回 HTTP 201 并包含新材料的标识
- **AND** 系统 MUST 执行入库流程（见「材料上传与知识库入库」）

#### Scenario: 学生界面不提供写入口

- **WHEN** 学生 A1 登录后打开本班材料列表
- **THEN** 页面 MUST 展示本班材料
- **AND** 页面 MUST NOT 渲染上传或删除材料的表单与按钮（服务端仍须按上一 Scenario 拒绝写请求）

### Requirement: 班级隔离

班级是数据边界。所有材料与知识库查询 MUST 在服务端按会话中的班级过滤；A 班用户 MUST NOT 读取、修改或删除 B 班材料；前端隐藏按钮 MUST NOT 作为满足本要求的唯一手段；客户端提交的班级参数 MUST NOT 覆盖会话中的班级。

#### Scenario: 跨班按 ID 访问材料被拒绝

- **WHEN** 班级 A 的用户（学生 A1 或教师 A）通过 URL 或 API 路径参数请求 B 班某条材料的 ID
- **THEN** 系统 MUST 返回 HTTP 404
- **AND** 响应 MUST NOT 包含 B 班材料的标题、正文片段、文件路径或存储键

#### Scenario: 学生列表不泄露他班材料

- **WHEN** 学生 A1 登录后请求材料列表
- **THEN** 返回的每一条记录 MUST 属于班级 A
- **AND** MUST NOT 出现 B 班预置材料的标题（标题含「B 班」的样本）

#### Scenario: 教师列表仅含本班记录

- **WHEN** 教师 A 登录后打开材料列表
- **THEN** 列表中每一条记录 MUST 属于班级 A
- **AND** 列表数据 MUST 来自数据库查询，而非页面中写死的内容

#### Scenario: 篡改班级参数无效

- **WHEN** 学生 A1 在列表请求的 query 或上传请求的表单中附带 `class_id` 为班级 B 的值
- **THEN** 列表结果 MUST 仍只含班级 A 的记录
- **AND** 教师 A 以同样方式上传时，新材料 MUST 仍归属班级 A

### Requirement: 材料上传与知识库入库

教师上传教学材料后，系统 MUST 保存文件、解析文本并写入知识库；材料与知识库条目 MUST 关联上传者所属班级；本班材料列表 MUST 从数据库查询展示；上传成功后刷新列表 MUST 可见新记录。首版支持的格式为 `.txt` 与 `.md`。

#### Scenario: 上传后知识库与列表可查

- **WHEN** 教师 A 上传一份合法的 `.md` 或 `.txt` 文件且解析成功
- **THEN** 材料表 MUST 新增一条标题可辨认的记录，并关联班级 A
- **AND** 知识库表 MUST 新增一条关联该材料与班级 A 的记录，其正文包含文件文本
- **AND** 教师 A 刷新本班材料列表时 MUST 看到这条新记录

#### Scenario: 上传后本班学生可见且只读

- **WHEN** 教师 A 上传成功后，学生 A1 刷新本班材料列表
- **THEN** 学生 A1 MUST 在列表中看到该条新材料
- **AND** 学生 B1 刷新其列表时 MUST NOT 看到该条材料

#### Scenario: 上传失败时不产生脏数据

- **WHEN** 教师上传的文件为不支持的格式（如 `.exe`）、空文件或无法解析的内容
- **THEN** 系统 MUST 返回 HTTP 400 与明确的错误说明
- **AND** 材料表与知识库表 MUST NOT 新增任何记录
- **AND** 上传目录中 MUST NOT 残留该文件

### Requirement: 预置核心数据

系统 MUST 建立班级、用户、讲义、作业、助手、技能六类核心数据结构，并预置可验收的样本数据，以支持登录、班级隔离与上传验收。

#### Scenario: 种子数据包含双班与三名用户

- **WHEN** 首次执行数据库初始化（包括 Compose 首次启动时自动执行）
- **THEN** 库中 MUST 存在班级 A 与班级 B
- **AND** MUST 存在教师 A（班级 A）、学生 A1（班级 A）、学生 B1（班级 B），且三人都能用 README 中给出的演示口令登录

#### Scenario: 两班材料标题可区分

- **WHEN** 初始化完成
- **THEN** MUST 存在至少一条标题含「A 班」且属于班级 A 的材料
- **AND** MUST 存在至少一条标题含「B 班」且属于班级 B 的材料
- **AND** 讲义、作业、助手、技能四类表 MUST 已创建（可仅含占位行，本 change 不验收其业务功能）

### Requirement: 密码哈希与会话密钥

系统 MUST 使用单向哈希存储用户密码，禁止明文；会话签名密钥 MUST 仅通过服务端环境变量注入，MUST NOT 写在源码中或提交到版本库。

#### Scenario: 库中无明文密码

- **WHEN** 直接查询用户表的密码字段
- **THEN** 字段值 MUST NOT 等于任何预置账号的明文口令
- **AND** 字段值 MUST 可识别为哈希格式（如 `pbkdf2:`、`scrypt:` 或 `$2b$` 前缀）

#### Scenario: 缺少会话密钥时拒绝启动

- **WHEN** 启动应用时未提供会话签名密钥环境变量
- **THEN** 应用 MUST 启动失败并输出缺少该变量的提示
- **AND** `.env.example` MUST 列出该变量名且不含真实密钥值

#### Scenario: 仓库中无密钥

- **WHEN** 在仓库中搜索真实的会话密钥值或 `.env` 文件
- **THEN** MUST NOT 找到已提交的真实密钥或 `.env` 文件

### Requirement: Docker Compose 部署与健康检查

系统 MUST 以 Docker Compose 作为标准启动方式；应用 MUST 提供无需登录的 `GET /health`；数据库文件与上传目录 MUST 通过 volume 持久化，容器重建后数据仍在。

#### Scenario: Compose 启动后可访问

- **WHEN** 操作者按 README 复制 `.env.example` 为 `.env`、填入密钥并执行 `docker compose up --build`，直至健康检查通过
- **THEN** 浏览器 MUST 能打开登录页
- **AND** `GET /health` MUST 返回 HTTP 200，响应体为 `{"status":"ok"}`

#### Scenario: health 不依赖登录态

- **WHEN** 不带任何会话 cookie 请求 `GET /health`
- **THEN** 系统 MUST 返回 HTTP 200
- **AND** MUST NOT 重定向到登录页

#### Scenario: 重建容器后数据仍在

- **WHEN** 教师 A 上传一条材料后，执行 `docker compose down` 再 `docker compose up`（不删除 volume）
- **THEN** 预置账号 MUST 仍可登录
- **AND** 教师 A 的材料列表 MUST 仍包含该条上传材料
