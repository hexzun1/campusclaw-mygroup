## MODIFIED Requirements

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
