## MODIFIED Requirements

> 差异说明：主规约「连续失败触发限流」已规定同一用户名、同一 IP 的锁定与统一文案；历史 add-auth-rbac-class-knowledge 还明确默认 5 次/300 秒。本 delta 完整保留 JWT 与会话条款，仅细化限流客户端地址的信任来源、隔离和失败恢复，不创建第二套登录或限流能力。

### Requirement: 登录与会话

系统 MUST 提供教师（teacher）、学生（student）两类角色的账号密码登录。登录成功后，系统 MUST 在响应 JSON 中返回一个 JWT（字段 `token`，响应体仍为 `{token, username, role}`），MUST NOT 设置会话 Cookie。token MUST 使用 HS256 签名，载荷 MUST 恰好由 `sub`（用户 ID 的十进制字符串）、`jti`（每个 token 唯一）、`iat`、`exp`（签发后 24 小时）与 `iss`（值为 `campusclaw-api`）五项组成，MUST NOT 包含用户名、角色、班级、口令或口令哈希。除 `POST /api/login` 与 `GET /health` 外，所有 `/api` 请求 MUST 携带 `Authorization: Bearer <token>`，服务端 MUST 按以下顺序认证：① 取 Bearer 凭据；② 验签——只接受 HS256、`exp` 必须存在且未过期、`iss` 必须匹配、载荷必须恰为上述五项且 `sub` 为正整数，这一步不查数据库，任一不满足返回 401；③ 查吊销表，已吊销返回 401，查询出错返回 503；④ 按 `sub` 读取用户并连同其班级，用户不存在返回 401，查询出错返回 503；⑤ 把用户 ID、用户名、角色、班级写入请求上下文。角色与班级 MUST 只取自第 ④ 步的数据库结果，MUST NOT 采信 token 中的声明或请求参数。所有认证失败的 401 响应体 MUST 相同且不含业务数据。放在 query、Cookie 或请求体中的 token MUST 被忽略。登出 MUST 使该 token 的 `jti` 立即失效，其它 token 不受影响。登录请求若在 `Authorization` 头中携带通过 ①–③ 的旧 token，登录成功时 MUST 先吊销该旧 token 的 `jti` 再签发新 token；登录失败时旧 token MUST 不受影响；携带的 token 无效时 MUST 不影响登录本身。系统 MUST 提供返回当前身份的接口 `GET /api/me`，返回字段保持为用户名、角色、班级编号与班级名。本规约中的"会话"指一次有效的登录状态，即通过上述 ①–④ 的 token；"会话班级""会话角色"指第 ④ 步从数据库读到的该用户当前的 `class_id` 与 `role`。

登录限流 MUST 以“用户名 + 规范化后的客户端 IP”为独立桶，连续失败达到 `LOGIN_MAX_FAILURES`（默认 5）后锁定 `LOGIN_LOCK_SECONDS`（默认 300 秒）；锁定期内正确口令也 MUST 返回与普通凭据错误相同的 401。成功登录 MUST 只清除当前桶的失败计数，锁定期结束后当前桶 MUST 可重新尝试；任一桶的失败、锁定或重置 MUST NOT 修改其他用户名或其他客户端 IP 的桶。

客户端 IP MUST 由服务端按以下信任边界决定：直接 TCP 对端属于服务端环境变量 `TRUSTED_PROXY_CIDRS` 指定的受控代理 IP/CIDR 时，仅使用该代理覆盖的单值 `X-Real-IP`；列表缺省为空，空列表或不匹配的对端 MUST 使用 `RemoteAddr` 的 IP 并忽略全部客户端 IP 转发头。信任列表 MUST NOT 从请求头推导，MUST NOT 默认信任所有私有地址或所有来源；非法列表及 `0.0.0.0/0`、`::/0` MUST 使服务拒绝启动。生产登录入口代理 MUST 用自己的真实对端地址覆盖客户端自带的 `X-Real-IP`。`X-Forwarded-For` 与 `Forwarded` MUST NOT 参与此版本限流 key 的选择，不论其出现在哪个位置或包含多少地址。

可信代理缺失 `X-Real-IP`，或该头为空、重复、多地址、带端口、含 zone、不是合法 IPv4/IPv6 字面地址时，服务 MUST 在访问限流桶与核对口令前返回 400，MUST NOT 回退到共享代理 IP 的桶。无法解析直接对端 IP 时也 MUST 返回 400，MUST NOT 生成任意字符串桶。合法地址 MUST 去掉首尾空白并规范化：不同端口不区分桶，IPv6 等价写法及 IPv4 映射 IPv6 与对应 IPv4 MUST 归入同一桶。不同真实客户端 IP 经同一可信代理使用同名账号时 MUST 互不影响；共享同一出口 IP 的客户端仍按同一 IP 处理。

#### Scenario: 登录成功

- **GIVEN** 预置账号存在，数据库可用，当前用户名与客户端 IP 的限流桶未被锁定
- **WHEN** teacher_a、student_a1 或 student_b1 使用正确口令调用 `POST /api/login`
- **THEN** 响应 MUST 为 200，JSON MUST 恰含 `token`、`username`、`role`，且响应 MUST NOT 设置任何会话 Cookie
- **AND** token 头部的算法 MUST 为 HS256，载荷的键集合 MUST 恰为 `sub`、`jti`、`iat`、`exp`、`iss`，`iss` 为 `campusclaw-api`，`exp - iat` MUST 等于 24 小时
- **AND** 随后以该 token 作 Bearer 调用 `GET /api/me` MUST 返回该用户的用户名、角色、班级编号与班级名

#### Scenario: 登录失败提示统一

- **GIVEN** 数据库可用，所用限流桶均未锁定，测试分别准备一个存在与一个不存在的用户名
- **WHEN** 分别使用"存在的用户名 + 错误口令"与"不存在的用户名"调用 `POST /api/login`
- **THEN** 两种请求 MUST 都返回 401，且响应体完全相同（统一文案"用户名或密码错误"）
- **AND** 响应 MUST NOT 含 token，MUST NOT 设置任何 Cookie

#### Scenario: 连续失败触发限流

- **GIVEN** 数据库可用，限流阈值与锁定时长已配置，当前用户名与客户端 IP 的失败计数为零
- **WHEN** 同一用户名在同一 IP 连续登录失败达到配置阈值后，再用正确口令登录
- **THEN** 在锁定期内该请求 MUST 被拒绝
- **AND** 响应 MUST 与口令错误时完全相同，不提示已被锁定，且 MUST NOT 含 token

#### Scenario: 携带旧 token 再次登录

- **GIVEN** 用户存在，数据库可用，甲尚未吊销，当前登录桶未锁定
- **WHEN** 已持有有效 token 甲的用户在 `Authorization` 头中携带甲并再次登录成功，得到 token 乙
- **THEN** 甲与乙的 `jti` MUST 不同
- **AND** 之后用甲调用任何 `/api` 接口 MUST 返回 401，用乙 MUST 正常

#### Scenario: 重新登录签发新 token

- **GIVEN** 用户存在，数据库可用，甲尚未吊销，另一处的登录桶未锁定
- **WHEN** 已持有有效 token 甲的用户在另一处不带 `Authorization` 头登录成功，得到 token 乙
- **THEN** 甲在其自身过期或被登出吊销之前 MUST 仍然有效

#### Scenario: 登录失败不影响旧 token

- **GIVEN** 用户存在且持有未吊销的有效 token 甲，当前登录桶未锁定
- **WHEN** 用户携带有效 token 甲调用登录，但口令错误
- **THEN** 响应 MUST 为 401，甲 MUST 仍然有效

#### Scenario: 登出后旧 token 失效

- **GIVEN** 用户有两个不同 jti、均未过期且未吊销的 token，数据库可用
- **WHEN** 已登录用户以 Bearer 调用 `POST /api/logout`，之后仍用原 token 调用 `GET /api/me`
- **THEN** 登出 MUST 成功（200）
- **AND** 使用原 token 的任何 `/api` 请求 MUST 返回 401，包括再次调用 `POST /api/logout`
- **AND** 同一用户在登出前另行签发的其它 token MUST 仍然有效

#### Scenario: 未登录访问受保护接口

- **GIVEN** 服务及数据库可用，测试为各路径准备本来可访问的有效资源编号与合法请求体
- **WHEN** 不带 `Authorization` 头调用 `GET /api/me`、`POST /api/logout`、`GET /api/materials`、`GET /api/materials/{id}`、`GET /api/materials/{id}/file`、`POST /api/materials`、`GET /api/agents`、`GET /api/skills`、`GET /api/conversations` 或 `POST /api/conversations/{id}/stream`
- **THEN** 每个请求 MUST 返回 401
- **AND** 响应体 MUST NOT 包含任何业务数据

#### Scenario: 签名错误的 token 被拒绝

- **GIVEN** 服务使用既定 JWT_SECRET，测试准备另一密钥签发的 token 与篡改载荷后未重签的 token
- **WHEN** 使用另一把密钥签发、或载荷被篡改（如加入 `role=teacher`、改动 `sub`）而签名未重算的 token 调用 `GET /api/me`
- **THEN** 响应 MUST 为 401

#### Scenario: 过期的 token 被拒绝

- **GIVEN** 测试 token 的签名、发行方与载荷结构均有效，但 exp 早于服务器当前时间
- **WHEN** 使用 `exp` 已过的 token 调用 `GET /api/me`
- **THEN** 响应 MUST 为 401

#### Scenario: 发行方或算法不符的 token 被拒绝

- **GIVEN** 测试分别准备错误发行方、none 算法和 HS512 算法的 token
- **WHEN** 使用 `iss` 不是 `campusclaw-api` 的 token，或头部算法为 `none`、`HS512` 的 token 调用 `GET /api/me`
- **THEN** 每次响应 MUST 为 401

#### Scenario: 载荷缺项的 token 被拒绝

- **GIVEN** 测试以服务端测试密钥签发各个缺项或非法 sub 的 token，数据库可用
- **WHEN** 使用签名正确但缺少 `sub`、`jti` 或 `exp` 之一，或 `sub` 不是正整数的 token 调用 `GET /api/me`
- **THEN** 每次响应 MUST 为 401

#### Scenario: 旧格式 token 被拒绝

- **GIVEN** 测试持有包含旧身份字段、签名正确且尚未过期的 token
- **WHEN** 使用签名正确、未过期，但载荷含 `user_id`、`username`、`role` 或 `class_id` 的旧格式 token 调用 `GET /api/me`
- **THEN** 响应 MUST 为 401

#### Scenario: 用户被删除后 token 立即失效

- **GIVEN** 某用户持有未过期且未吊销的合法 token，测试能删除其数据库记录
- **WHEN** 在数据库中删除某用户后，用该用户此前签发且未过期的 token 调用 `GET /api/me`
- **THEN** 响应 MUST 为 401

#### Scenario: token 只认 Authorization 头

- **GIVEN** 存在有效 token，服务与数据库可用
- **WHEN** 把一个有效 token 放在 query 参数、Cookie 或请求体中，而不带 `Authorization` 头，调用 `GET /api/me`
- **THEN** 响应 MUST 为 401

#### Scenario: 登录与健康检查无需 token

- **GIVEN** 服务及数据库可用，登录请求使用有效账号和正确口令，当前登录桶未锁定
- **WHEN** 不带 `Authorization` 头调用 `POST /api/login` 与 `GET /health`
- **THEN** 它们 MUST 按各自的规则响应，MUST NOT 因缺少 token 而返回 401

#### Scenario: 校验依赖的数据库不可用时返回 503

- **GIVEN** 用户已取得有效且未吊销的 token，测试可以中断并恢复数据库连接
- **WHEN** db 服务停止后，持有有效 token 的用户调用 `GET /api/materials`
- **THEN** 响应 MUST 为 503，MUST NOT 为 401
- **AND** db 恢复后同一 token MUST 仍然有效

#### Scenario: 可信代理后的两个客户端计数互不影响

- **GIVEN** 代理 P 在信任列表，阈值为 5、锁定 300 秒，用户名 U 存在，C1=198.51.100.10 与 C2=198.51.100.20 的桶均为空
- **WHEN** P 分别携带覆盖后的 X-Real-IP 为 C1、C2 转发同名 U 的 3 次和 2 次错误口令登录，再由 C2 用正确口令登录
- **THEN** C2 的正确登录 MUST 返回 200，MUST NOT 将两客户端合并成 5 次失败或使用 P 的地址作为共同桶
- **AND** C2 成功后 C1 再失败 2 次 MUST 进入锁定，证明 C2 的成功没有清除 C1 的计数

#### Scenario: 第三方不能通过同名账号封禁另一客户端

- **GIVEN** C1 与 C2 使用同一可信代理 P、同名账号 U、不同真实 IP，失败阈值为 5、锁定时长为 300 秒，初始桶为空
- **WHEN** 攻击者 C1 对 U 连续输错口令 5 次，随后 C1 与受害者 C2 都在 300 秒内用 U 的正确口令登录
- **THEN** C1 MUST 返回统一文案的 401 且无 token，C2 MUST 返回 200 并获得有效 token
- **AND** MUST NOT 产生按用户名全局封禁或按共享代理 IP 封禁的效果

#### Scenario: 同一客户端的不同用户名独立

- **GIVEN** 一个客户端 IP 对应存在的用户名 U、V，U 已达到锁定阈值，V 的桶为空
- **WHEN** 该客户端分别以 U、V 的正确口令登录
- **THEN** U MUST 返回 401，V MUST 返回 200；V 的成功 MUST NOT 解锁 U

#### Scenario: 直连伪造转发头不能换桶或封禁其他客户端

- **GIVEN** C1 直接连接 API 且不在信任列表，C2 是另一个 IP，账号 U 的两个桶为空，阈值为 5
- **WHEN** C1 连续 5 次输错 U 的口令，每次伪造不同 X-Real-IP 并在 X-Forwarded-For、Forwarded 中声称自己是 C2，再由 C1、C2 分别以正确口令登录
- **THEN** C1 MUST 被同一真实对端 IP 桶锁定并返回 401，C2 MUST 返回 200
- **AND** 修改或删除伪造头 MUST NOT 改变 C1 的桶，MUST NOT 污染 C2 的桶

#### Scenario: 经入口代理伪造受害者地址无效

- **GIVEN** 入口 Nginx 是受控可信代理，攻击者 C1 与受害者 C2 使用不同真实源 IP，U 的桶为空
- **WHEN** C1 经入口连续 5 次以 U 输错口令，并自带 X-Real-IP 为 C2、X-Forwarded-For 为 C2 与任意代理链，随后 C2 正确登录
- **THEN** 转发给 API 的 X-Real-IP MUST 为 C1，API MUST 忽略 X-Forwarded-For；C1 的桶锁定而 C2 MUST 返回 200
- **AND** 本场景 MUST 通过真实入口覆盖头行为验收，MUST NOT 仅靠直接向 API 注入一个声称来自 C1 的头来代替

#### Scenario: 可信代理缺失或非法地址不会污染共享桶

- **GIVEN** 直接对端 P 是可信代理，测试分别准备缺失、空白、重复头、逗号分隔两个 IP、IP 加端口、IPv6 zone、任意非法文本的 X-Real-IP
- **WHEN** 对各输入分别提交包含合法用户名与口令字段的登录请求
- **THEN** 每次 MUST 返回 400 且无 token，MUST NOT 核对口令或读取、增加、重置任何限流桶
- **AND** 后续携带合法 X-Real-IP 的正确登录 MUST 正常返回 200

#### Scenario: 等价 IP 与不同源端口不能绕过锁定

- **GIVEN** U 的失败阈值为 5，测试分别使用 IPv6 压缩/展开两种写法，以及 IPv4 与对应 IPv4 映射 IPv6 两组等价地址
- **WHEN** 每组在可信代理头中交替使用等价地址累计失败 5 次后正确登录，另以未信任直连地址更换源端口重复同样流程
- **THEN** 每组 MUST 只使用一个规范化 IP 桶并在正确登录时返回 401，MUST NOT 因写法或端口变化获得新的失败配额

#### Scenario: 成功重置与锁定过期只作用于当前桶

- **GIVEN** C1、C2 对 U 各有 4 次失败，阈值为 5、锁定 300 秒，测试可控制时钟
- **WHEN** C1 正确登录后再次失败 1 次，C2 再失败 1 次，并在锁定开始后第 299 秒和第 300 秒分别使用 C2 正确登录
- **THEN** C1 MUST 未锁定；C2 第 299 秒 MUST 返回统一 401，第 300 秒 MUST 可登录成功，MUST NOT 需要等待真实 300 秒来验收

#### Scenario: 代理信任配置必须显式且可校验

- **GIVEN** 测试分别设置空信任列表、单个受控代理 IP/CIDR、非法 CIDR、0.0.0.0/0 与 ::/0
- **WHEN** 服务加载配置，并在可启动的配置下接收带 X-Real-IP 的登录请求
- **THEN** 空列表 MUST 不信任任何转发头，明确列表 MUST 仅信任匹配对端，后三种非法或全局信任配置 MUST 拒绝启动并指出配置项名称

#### Scenario: 直接对端无法解析时拒绝

- **GIVEN** 未通过可信代理的测试请求带有无法解析为 IP 对端的 RemoteAddr，并带伪造 X-Real-IP
- **WHEN** 请求提交登录
- **THEN** 响应 MUST 为 400，MUST NOT 使用伪造头或任意 RemoteAddr 文本作为桶，MUST NOT 访问限流计数或核对口令
