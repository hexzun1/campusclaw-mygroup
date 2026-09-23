# CampusClaw

- **价值**：面向中小学的教研智能体，把分散在群文件与网盘里的教学材料集中进按班级隔离的知识库，为后续检索与智能问答打底。
- **场景**：教师登录后上传并管理本班教学材料；学生登录后只读查看、下载本班材料；不同班级之间数据互不可见。
- **本学期不做**：知识库问答与检索、对话助手、作业流程、注册与找回密码、JWT/OAuth/SSO、平台超级管理员、PDF/Word 解析、公网部署与多副本高可用。

## 当前迭代

迭代 1（v0.1.0-auth-upload）：登录与角色权限、班级数据隔离、材料上传与知识库入库。规约见 `openspec/changes/add-auth-rbac-class-knowledge/`。

- 技术栈：React + TypeScript + Vite（前端）/ Go net/http（后端）/ MySQL 8.0 / Nginx / Docker Compose
- 部署形态：单实例 Docker Compose，只暴露 web 端口（默认 8080）
- 跨班访问：访问其他班级的材料与访问不存在的材料，统一返回 **404**

> 启动步骤、预置账号与健康检查地址将在实现阶段（tasks.md §8）补充。
