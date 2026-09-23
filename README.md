# CampusClaw

- **价值**：面向中小学的教研智能体，把分散在群文件与网盘里的教学材料集中进按班级隔离的知识库，为后续检索与智能问答打底。
- **场景**：教师登录后上传并管理本班教学材料；学生登录后只读查看、下载本班材料；不同班级之间数据互不可见。
- **本学期不做**：知识库问答与检索、对话助手、作业流程、注册与找回密码、JWT/OAuth/SSO、平台超级管理员、PDF/Word 解析、公网部署与多副本高可用。

## 当前迭代

迭代 1（v0.1.0-auth-upload）：登录与角色权限、班级数据隔离、材料上传与知识库入库。规约见 `openspec/changes/add-auth-rbac-class-knowledge/`。

- 技术栈：React + TypeScript + Vite（前端）/ Go net/http（后端）/ MySQL 8.0 / Nginx / Docker Compose
- 部署形态：单实例 Docker Compose，只暴露 web 端口（默认 8080）
- 跨班访问：访问其他班级的材料与访问不存在的材料，统一返回 **404**

## 快速开始

```bash
cp .env.example .env
# 编辑 .env，为 SESSION_SECRET / DB_PASSWORD / MYSQL_ROOT_PASSWORD / SEED_*_PASSWORD 等填入真实值
docker compose up --build -d
```

- 登录地址：http://localhost:8080 （`WEB_PORT` 未设置时默认 8080；被占用可在 `.env` 中修改 `WEB_PORT`）
- 健康检查：http://localhost:8080/health → `{"status":"ok"}`，无需登录

## 预置账号

| 用户名 | 角色 | 班级 | 密码 |
| --- | --- | --- | --- |
| teacher_a | 教师 | A 班 | 来自 `.env` 的 `SEED_TEACHER_A_PASSWORD` |
| student_a1 | 学生 | A 班 | 来自 `.env` 的 `SEED_STUDENT_A1_PASSWORD` |
| student_b1 | 学生 | B 班 | 来自 `.env` 的 `SEED_STUDENT_B1_PASSWORD` |

首次启动会自动建表并写入以上账号及两班各一条示例材料，种子过程幂等，重启不会重复插入或覆盖已上传内容。

## 已知行为与限制

- **跨班访问统一 404**：无论是访问不存在的材料 ID，还是访问其他班级的材料 ID，接口都返回相同的 404 响应，不会泄露"该 ID 是否存在"。
- **单实例部署**：当前只支持单个 api/db 实例（登录限流计数在内存中，重启会清零），不做多副本、公网证书与 CI/CD，详见 `openspec/changes/add-auth-rbac-class-knowledge/proposal.md` 的 Non-goals。
- **MySQL 首次启动较慢**：db 容器首次初始化建表可能需要几十秒，此时访问 web 可能短暂看到 502，等待 `docker compose ps` 中 db 变为 `healthy` 后即可正常访问。
- **修改端口**：如需更换对外端口，在 `.env` 中设置 `WEB_PORT` 后重新 `docker compose up -d` 即可，无需改动 compose 文件。
