> Apply 约定：一个 task 一轮 —— 读 task → 实现 → 对照 spec 审查 diff → 运行 verify → 通过后勾选 `[x]` → 提交。verify 未通过不得勾选，不得带入下一项。本变更只改前端与文档，**不得修改 `backend/` 与任何接口**。
> 开发期环境：`docker compose -f docker-compose.dev.yml up -d`（db、qdrant、网关桩、api 映射在 `127.0.0.1`），前端用 `npm --prefix frontend run dev`（`.claude/launch.json` 里的 `frontend-dev`，端口 5174，`/api` 代理到 `127.0.0.1:8081`）。账号沿用：teacher_a / student_a1（A 班）、student_b1（B 班），口令取自本机 `.env`。
> 记录请求的浏览器控制台片段（只用于验收，不进代码）：`window.__log=[];const f=window.fetch;window.fetch=async(u,o={})=>{window.__log.push({url:String(u),method:o.method||'GET',headers:Object.fromEntries(new Headers(o.headers||{})),body:typeof o.body==='string'?o.body:null});return f(u,o)}`；之后读 `window.__log` 核对请求体与请求头。制造依赖故障的办法：`docker compose -f docker-compose.dev.yml stop qdrant`（得 503）、向 `revoked_tokens` 插入当前 token 的 `jti`（得 401）。
> 预期的接口契约以 `openspec/changes/add-traceable-vector-retrieval/specs/knowledge-retrieval/spec.md`（或归档后的主规格）为准；对照真实数据时用 `curl -H "Authorization: Bearer $TOKEN" -d '{...}' $API/api/search` 取同一问题的原始 JSON。

## 1. API 封装

- [x] 1.1 在 `api/client.ts` 增加类型 `Hit`、`SearchResponse`、`AskResponse`，函数 `searchKnowledge(query, mode, signal?)`（请求体只含 `query`、`mode`）与 `askKnowledge(question, signal?)`（请求体只含 `question`），均走既有 `request()`；增加集中的错误文案映射（503 →「检索服务暂不可用」，400 → 服务端 `error`，401 → 不显示，其它 →「请求失败，请稍后重试」） — verify: `cd frontend && npm run build && npm run lint` 通过；`grep -nE "top_k|messages|class_id|role" frontend/src/api/client.ts` 在这两个新函数的请求体附近无输出

## 2. 共享布局与路由

- [x] 2.1 新增 `AppLayout`（顶部 `<nav aria-label="主导航">`，用 `NavLink` 并列「材料」「知识检索」「知识问答」，右侧沿用"用户名（角色）、主题切换、登出"），`App.tsx` 改为受保护的嵌套路由：`/materials`、`/search`、`/ask`，其余路径按登录态跳转；先为 `/search`、`/ask` 各建一个只有标题的占位页 — verify: `npm run build && npm run lint` 通过；浏览器里 student_a1 与 teacher_a 登录后都能看到三个标签，当前页标签的 `aria-current` 为 `page`，依次点击三个标签地址变为 `/materials`、`/search`、`/ask`
- [x] 2.2 把 `MaterialsPage` 的页头与外层容器样式移到布局层，页面内保留自己的 `<h1>`（"{班级名} 教学材料"），避免重复的内边距 — verify: 浏览器里材料页回归：搜索、打开详情、下载、主题切换、登出各做一次都正常；teacher_a 能看到上传入口而 student_a1 看不到；页头只出现一次，页面两侧留白与改动前一致（截图对比）
- [x] 2.3 验证访问控制与刷新 — verify: 登出状态下直接打开 `/search` 与 `/ask`，都回到登录页，且请求记录里没有 `/api/search`、`/api/ask`；登录后在 `/search`、`/ask` 各刷新一次仍停留在原页面（生产 nginx 的回退在 6.1 再验）

## 3. 知识检索页

- [x] 3.1 实现检索表单：问句输入框（`maxLength=500`）、模式选择（混合 / 关键词 / 向量，取值 `hybrid` / `keyword` / `vector`，默认 `hybrid`）、提交按钮；去空白后为空或请求进行中不发请求，进行中显示"正在检索"并禁用提交 — verify: 用请求记录片段确认：直接提交问句时请求体恰为 `{"query":"…","mode":"hybrid"}`；选"关键词"/"向量"后请求体的 `mode` 随之变化；请求头含 `Authorization: Bearer …`，请求体与 URL 中没有 `class_id`、`role`、`top_k`；输入为空或只有空格时无请求；向输入框粘贴 600 个字符后长度为 500；连续快速点击提交两次只产生 1 个请求
- [x] 3.2 实现结果展示：按返回顺序逐条显示材料标题、切片序号、字符区间与摘录（纯文本，`white-space: pre-wrap`）；无命中显示「资料中未找到相关内容」；新一次检索先清空旧结果 — verify: student_a1 以 `hybrid` 检索"教学材料"，页面条数、顺序与每条的标题 / 切片序号 / 区间 / 摘录都与同一问题 `curl` 得到的 `hits` 一致；检索一个不存在的乱码串，页面只显示「资料中未找到相关内容」且无条目；再做第二次不同的检索后，第一次的条目消失
- [x] 3.3 依赖不可用时的表现 — verify: `stop qdrant` 后以 `hybrid`、`vector` 检索，页面显示「检索服务暂不可用」，页面文本中不含 `qdrant`、`6333`、`api-key`；同时以 `keyword` 检索 A 班存在的词仍正常显示命中；`start qdrant` 后恢复
- [x] 3.4 其它失败路径 — verify: 用 `window.fetch` 包装临时让下一次请求返回 400 `{"error":"检索词不能超过 500 个字符"}`，页面显示该原因且可改后重新提交；让下一次请求 reject（模拟断网），页面显示「请求失败，请稍后重试」且可重试；向 `revoked_tokens` 插入当前 token 的 `jti` 后再提交，页面回到登录页且 `sessionStorage` 中 token 已清除

## 4. 知识问答页

- [x] 4.1 实现问答页骨架：顶部说明文字（原文见 spec）、单行问题输入框（`maxLength=1000`）与提交按钮；提交后在对话区右侧追加用户问题，回答前显示「正在生成回答…」；空白问题不发请求；进行中禁用输入与按钮；请求体只含 `question`；对话只存于组件内存 — verify: 页面顶部文字含"前 4 条切片""[1]、[2]""没有命中时不调用模型"；用请求记录确认第一个与第二个问题的请求体分别恰为 `{"question":"…"}`，第二次请求不含第一个问题，也没有 `messages` 字段；用户问题元素的水平位置在对话区右半侧（`getBoundingClientRect` 的右边缘贴近容器右边缘）；用 `fetch` 包装让响应延迟 3 秒，期间再点提交没有第二个请求
- [x] 4.2 实现「依据回答」卡片与出处列表：`citations` 非空时显示标题为「依据回答」的卡片（正文为 `answer`，纯文本），下方按数组顺序列出出处，第 n 项标 `[n]`，显示材料标题、切片序号、字符区间、摘录 — verify: student_a1 就 A 班有依据的内容提问（如"教学材料是做什么用的"），页面卡片正文与同一问题 `curl` 的 `answer` 逐字一致；出处条数、顺序、标题 / 切片序号 / 区间 / 摘录与 `citations` 一致，编号依次为 `[1]`…`[n]`；若回答里有 `[n]` 标注，其 n 均落在出处编号范围内
- [x] 4.3 无依据的表现 — verify: student_a1 询问 A 班材料里没有依据的问题（如乱码串），以及只存在于 B 班的内容，页面都只显示「资料中未找到相关内容」；DOM 中没有「依据回答」文字、没有出处列表元素；页面上没有出现任何 B 班材料的标题或摘录；`$STUB/stats`（网关桩统计）显示这两次提问期间对话调用次数没有增加
- [x] 4.4 失败与登录失效 — verify: 先完成一轮成功问答，再 `stop qdrant` 提第二个问题：第一轮问答仍在页面上，第二个问题旁显示「检索服务暂不可用」，输入框恢复可用，`start qdrant` 后可重新提问并成功；向 `revoked_tokens` 插入当前 token 的 `jti` 后再提问，页面回到登录页

## 5. 渲染安全与不落地

- [x] 5.1 用材料验证纯文本渲染 — verify: teacher_a 上传一份 `.md`，内容含 `<script>alert(1)</script>`、`<b>加粗</b>`、`# 标题`、`**粗体**`、`- 列表`；先把 `window.alert` 替换为记录函数，再在检索页与问答页命中它：这些字符原样显示为文字，未触发 `alert`，结果区内 `document.querySelector('b, h1, strong, ul')` 为 `null`
- [x] 5.2 确认无新的注入面 — verify: `grep -nE "dangerouslySetInnerHTML|innerHTML|react-markdown|rehype-raw|ReactMarkdown" frontend/src/pages/SearchPage.tsx frontend/src/pages/AskPage.tsx frontend/src/components/AppLayout.tsx` 无输出
- [x] 5.3 确认内容不落地 — verify: 完成检索与问答后，`Object.keys(sessionStorage)` 只含 `campusclaw_token`，`localStorage.length` 为 0，`document.cookie` 为空；在问答页刷新，以及切到「材料」再切回，此前的对话都已消失

## 6. 生产部署验证

- [x] 6.1 在带 CSP 的生产 nginx 上验证 — verify: `docker compose up --build -d` 后打开 `http://localhost:8080`：登录、三个标签切换、检索（至少 `keyword` 命中成功）、提问、主题切换、登出全部可用，浏览器控制台无 CSP 违规；在 `/search`、`/ask` 直接刷新页面仍返回应用（nginx 回退生效）；若生产编排未配置可用网关，则 `hybrid` / 问答按预期显示「检索服务暂不可用」，这仍算通过，但必须记入文档

## 7. 文档与收尾

- [x] 7.1 README 补充"知识检索""知识问答"两个页面的入口与用法（三种模式的含义、问答每题独立、不保存历史、503 的含义） — verify: 按 README 的说明在页面上做一次检索和一次提问，与描述一致
- [x] 7.2 新增 `docs/knowledge-qa-ui.md`，逐条记录本 change 每个 Requirement 的场景验收结果（通过 / 不通过与依据），并在 `docs/iteration-2.md` 顶部加一句"「不做前端检索页」这一非目标已被 add-knowledge-qa-ui 取代"，其余内容不改 — verify: 文档覆盖 spec 的 6 条 Requirement，每条有结论与依据；`docs/iteration-2.md` 首屏可见该说明
- [x] 7.3 运行 `openspec validate add-knowledge-qa-ui --strict` — verify: 退出码 0 且无 error
- [x] 7.4 确认没有误改后端、没有泄露凭据 — verify: `git diff --stat -- backend docker-compose.yml docker-compose.dev.yml` 无输出；`git ls-files | grep -x .env` 无输出；`git grep -n "Bearer ey"` 无输出
- [ ] 7.5 归档本 change（需使用者明确指示后执行 `/opsx:archive`）— verify: `openspec list` 中无本 change；`openspec/specs/knowledge-qa-ui/spec.md` 已生成且与 delta 一致
