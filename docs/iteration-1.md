# 迭代 1 说明（v0.1.0-auth-upload）

> **本文的登录会话方案已被 `switch-to-jwt-bearer-auth` 取代**：Cookie 相关命令（`curl -c/-b`）、`SESSION_SECRET`/`SESSION_TTL` 以及 design 里「否决 JWT、禁止 Web Storage」的决策 2 仅作历史记录，现状见 `docs/jwt-bearer-auth.md`。本文其余内容（班级隔离、403/404 约定、上传入库等）仍然有效。

规约：`openspec/changes/add-auth-rbac-class-knowledge/`

## 关键 Scenario 验收

以下用生产 `docker compose up --build -d`（`web` 映射到 `localhost:8080`）跑出的真实命令与输出，逐条对照 `specs/auth-upload/spec.md` 验收。

### 1. 未登录访问受保护接口 → 401

```
$ curl -s -i http://localhost:8080/api/materials
HTTP/1.1 401 Unauthorized
Content-Type: application/json; charset=utf-8

{"error":"未登录或会话已失效"}
```

**结论：通过**。响应体不含任何材料标题或正文。

### 2. 学生上传被拒绝 → 403

```
$ curl -s -c jar -d '{"username":"student_a1","password":"***"}' http://localhost:8080/api/login
{"role":"student","username":"student_a1"}

$ curl -s -i -b jar -F "file=@it1.md" http://localhost:8080/api/materials
HTTP/1.1 403 Forbidden
Content-Type: application/json; charset=utf-8

{"error":"没有权限执行该操作"}
```

**结论：通过**。角色判断只信任服务端会话，学生上传直接 403，未落盘、未写库。

### 3. 跨班按 ID 访问返回 404（与不存在 ID 完全一致）

```
$ curl -s -i -b teacher_jar http://localhost:8080/api/materials/2   # B 班材料，teacher_a 属于 A 班
HTTP/1.1 404 Not Found
Content-Type: application/json; charset=utf-8

{"error":"记录不存在"}

$ diff <(curl -s -i -b teacher_jar http://localhost:8080/api/materials/2) \
       <(curl -s -i -b teacher_jar http://localhost:8080/api/materials/999999)
（无输出，两者完全一致）
```

**结论：通过**。跨班访问与访问不存在的记录返回码与响应体逐字节相同，不泄露"该 ID 是否存在"。

### 4. 登出后旧 Cookie 失效 → 401

```
$ curl -s -X POST -b teacher_jar http://localhost:8080/api/logout
{"ok":true}

$ curl -s -i --cookie "session_id=<登出前的会话ID>" http://localhost:8080/api/me
HTTP/1.1 401 Unauthorized
Content-Type: application/json; charset=utf-8

{"error":"未登录或会话已失效"}
```

**结论：通过**。登出立即删除服务端会话行，旧会话 ID 之后一律 401。

### 5. 重建后数据仍在

```
$ curl -s -b teacher_jar -F "file=@persist.md" -F "title=持久化测试材料" http://localhost:8080/api/materials
{"id":7,"title":"持久化测试材料"}

$ docker compose down          # 不加 -v，保留数据卷
$ docker compose up --build -d
$ curl -s -b teacher_jar http://localhost:8080/api/materials | grep 持久化测试材料
"持久化测试材料"
$ curl -s -b teacher_jar http://localhost:8080/api/materials/7/file
持久化测试内容
```

**结论：通过**。`down` 不删除卷时，MySQL 数据与上传文件在重建后原样可见、可下载。

## 三项设计决策（决策 / 备选 / 理由）

### 决策 1：技术栈选 Go + MySQL + React + Nginx，放弃 Flask + SQLite

- **决策**：后端用 Go 标准库 `net/http`（不引入 Web 框架），数据库用 MySQL 8.0，前端 React + TypeScript + Vite，入口用 Nginx 做静态托管 + 反向代理，整体用 Docker Compose 编排。
- **备选**：第 2 课初稿的 Flask + SQLite 单体应用——更轻量、不需要独立数据库容器。
- **理由**：本迭代虽然还没做检索，但下一步就是知识库检索，SQLite 的文件锁在多进程/未来多实例场景下不够用；MySQL 与生产环境更等价。前后端分离之后，"谁能看到什么数据"这条信任边界干净地落在 Go 这一层，浏览器和静态页面全部不可信，这也是为什么权限判断都写在服务端中间件里，而不依赖前端隐藏按钮。

### 决策 2：登录态用服务端会话 + HttpOnly Cookie，不用 JWT

- **决策**：`sessions` 表存随机会话 ID（32 字节随机数，`crypto/rand`）、`user_id`、过期时间；Cookie 设 `HttpOnly` + `SameSite=Lax`；登录成功时签发新会话并删除该用户之前所有会话（防会话固定）；登出即删除会话行并清 Cookie。
- **备选**：JWT——无状态、不用查库。
- **理由**：验收里有一条硬性要求"登出后旧 Cookie 立即失效"。JWT 一旦签发，在过期前服务端没法主动作废，除非再维护一个黑名单（等于又实现了一遍会话表，不如直接用会话表）。另外 JWT 常见做法是存 localStorage，一旦有 XSS 就直接把身份令牌交出去；HttpOnly Cookie 从设计上就杜绝了 JS 读取，这也是为什么前端代码里完全没有读写 `document.cookie`。

### 决策 3：跨班访问统一返回 404，不用 403

- **决策**：按 ID 访问材料详情/文件时，SQL 先只按 `id` 取行（不在 WHERE 里带 `class_id`），取到之后在 Go 代码里比较 `row.class_id` 是否等于会话班级；不相等和查无此行两种情况，返回完全相同的 404 响应体。
- **备选**：跨班返回 403（"存在但你无权访问"），不存在返回 404。
- **理由**：如果跨班返回 403、不存在返回 404，攻击者只要遍历 ID 就能靠状态码猜出"哪些 ID 存在"，间接暴露了别的班级材料总数和 ID 分布这类本不该跨班可见的信息。统一成 404 之后，两种情况从客户端角度完全不可区分，符合"班级是数据边界"的要求；代价是教师看到"提示是 403 权限不足"和"提示是资源不存在"这类更友好的报错区分做不到了，但这属于故意放弃的一点可用性，换班级隔离的严格性。

## 已知限制

- 登录限流计数在内存中，单实例；重启后计数清零（见 `design.md` Risks），不在本迭代解决范围内。
- 检索、问答、账号体系扩展、平台超级管理员等按 `proposal.md` Non-goals 明确不做。
