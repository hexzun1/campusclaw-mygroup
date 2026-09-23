# 项目规则

1. **无规约不写代码**：任何行为变更，先在 `openspec/changes/<change>/` 中写好并评审 proposal / design / spec / tasks，再实现。
2. 要改行为，先改规约 Markdown，再让模型按规约改代码；不得只在对话里口头追加需求。
3. 实现必须以 `specs/**/spec.md` 中的 Scenario 为验收标准；"模型说完成了"不等于完成。
4. 安全底线：密码只存哈希；密钥只放服务端环境变量，不得写进源码或提交到仓库；权限与班级隔离必须在服务端实现，前端隐藏按钮不算。
5. 未经明确指示，不执行 `/opsx-apply`、`/opsx-archive`，不修改 tasks.md 的勾选状态。
