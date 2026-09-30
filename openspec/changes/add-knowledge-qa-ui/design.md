## Context

前端现状（`frontend/`，React + TypeScript + Vite，无路由库以外的状态库）：

- `App.tsx` 里的 `Gate` 按 `useAuth().user` 决定渲染登录页还是材料页，只有 `/login`、`/materials` 两条路由；`AuthProvider` 启动时按 `sessionStorage` 里的 token 调 `/api/me`。
- `api/client.ts` 已有统一的 `request<T>()`：给除登录外的请求加 `Authorization: Bearer`，401 时清除 token 并触发 `setUnauthorizedHandler` 的回调（`AuthProvider` 借此把 `user` 置空，`Gate` 随之回到登录页），非 2xx 抛出带 `status` 与服务端 `error` 文案的 `ApiError`。
- `MaterialsPage` 自带页头（`<h1>{班级名} 教学材料</h1>`、用户名与角色、主题切换、登出）和 `.materials-page` 容器；没有导航。
- 生产部署由 nginx 提供，带严格 CSP（`script-src 'self'`、`style-src 'self'`，无内联脚本与样式）；SPA 路由靠 `try_files $uri /index.html` 回退，`/api/` 与 `/health` 反代给 api。
- 后端接口已存在且不变：`POST /api/search` 请求 `{query, mode?, top_k?}`，响应 `{mode, hits[], message?}`；`POST /api/ask` 请求 `{question, messages?}`，响应 `{answer, citations[]}`。`hits` 与 `citations` 元素同形：`material_id, material_title, chunk_id, chunk_index, char_start, char_end, excerpt, score`。问句上限 500 字、问题上限 1000 字；400 的 `error` 是可直接显示的中文原因；依赖不可用时 503。

动机与范围见 `proposal.md`，可验收行为见 `specs/knowledge-qa-ui/spec.md`。

## Goals / Non-Goals

**Goals:**

- 两个新页面与现有材料页共用同一套导航与页头，登录态、Bearer 头、401 处理全部复用现有 `request()`，不另起一套。
- 页面只"忠实呈现"接口结果：不改写回答、不增删出处、编号只由 `citations` 的数组顺序决定。
- 所有来自材料或模型的文字（摘录、标题、回答）都按纯文本显示，不引入新的注入面。
- 不新增依赖、不动后端。

**Non-Goals:**

- 见 `proposal.md` 的 Non-goals。
- 不追求聊天产品级体验（不做流式、消息重发、复制按钮、Markdown 渲染、出处跳转）。
- 不为这两页引入前端测试框架（见 Decision 8）。

## Decisions

### Decision 1：路由与共享布局 —— 嵌套路由 + `AppLayout`

把登录后页面的公共部分抽成 `AppLayout`：顶部一栏放导航（三个 `NavLink`）与右侧的"用户名（角色）、主题切换、登出"，下方是 `<Outlet/>`。`Gate` 改为嵌套路由：

```
/login                       → 已登录则跳 /materials，否则 LoginPage
（受保护，未登录跳 /login）  → AppLayout
   /materials                → MaterialsPage
   /search                   → SearchPage
   /ask                      → AskPage
*                            → 按登录态跳 /materials 或 /login
```

- 使用 react-router 的 `NavLink`：当前标签自动带 `aria-current="page"` 与 `active` 类，选中标识和无障碍语义都不用手写；导航用 `<nav aria-label="主导航">`，键盘可达。
- `MaterialsPage` 的页头由 `AppLayout` 接管，页面内保留自己的 `<h1>`（"{班级名} 教学材料"）；原 `.materials-page` 容器的宽度与内边距移到布局层，避免重复留白。这是对现有页面的一次小重构，回归面只在页头与容器样式，须在浏览器里回归材料页（见 tasks）。
- 访问控制仍由 `Gate` 的登录态判断，只决定"显示什么"；真正的鉴权在服务端。两个新页面不判断角色，教师与学生完全一致。

**备选：** 三个标签做成一个页面内的 state 切换——没有可分享 / 可刷新的地址，刷新会回到"材料"，且与现有路由结构不一致，故不采用。

### Decision 2：API 封装 —— 在 `client.ts` 增加两个函数与类型，错误文案集中映射

- 类型 `Hit`（与 `hits`/`citations` 同形，`score` 保留但页面不显示）、`SearchResponse`、`AskResponse`；函数 `searchKnowledge(query, mode, signal?)`、`askKnowledge(question, signal?)`，内部走 `request()`（自动带 Bearer、处理 401）。
- 请求体只含接口约定的字段：检索 `{query, mode}`，问答 `{question}`。**不**带 `top_k`（沿用后端默认 5）、**不**带 `messages`、**不**带班级或角色——班级只由 token 决定，页面根本没有可以传它的字段。
- 一个集中函数把失败映射为要显示的文字：`ApiError` 503 → 「检索服务暂不可用」；400 → 使用 `error`（后端为中文可读原因）；401 → 不显示（全局处理已回到登录页）；其它（含网络错误）→ 「请求失败，请稍后重试」。503 用固定文案，不透传服务端消息，保证页面上不会出现依赖服务的任何细节。
- 每个请求接受 `AbortSignal`；页面卸载（切换标签、登出）时中止在途请求，避免对已卸载页面写状态，也避免离开后仍占着后端的模型调用。

### Decision 3：知识检索页 —— 单表单 + 结果列表，状态机 idle / loading / done / error

- 表单：文本输入（`maxLength=500`）、模式 `<select>`（选项文案：混合、关键词、向量，取值 `hybrid` / `keyword` / `vector`，默认 `hybrid`）、提交按钮。提交按钮在"去空白后为空"或"请求进行中"时禁用；提交处理函数也再判断一次，不依赖按钮禁用。
- 结果：按返回顺序渲染有序列表，每条显示 **材料标题**、**切片序号**（原样显示 `chunk_index`，从 0 起，标签写明「切片序号」）、**字符区间**（`char_start`–`char_end`，`title` 提示"左闭右开"）、**摘录**（`white-space: pre-wrap`，纯文本）。`score` 不显示（非需求，且三种模式量纲不同易误导）。
- 无命中：`hits` 为空时显示前端常量「资料中未找到相关内容」，不显示任何条目。选用前端常量而不是响应里的 `message`，让页面文案不随后端提示语变动，且与需求给定的固定文案一致。
- 新一次提交先清空旧结果再进入 loading；失败时进入 error 并显示错误文字（不保留旧结果，避免把过期结果当成当前结果）。请求进行中禁止再次提交，因此不会出现乱序返回。

### Decision 4：知识问答页 —— 对话列表 + 单输入框，每题独立

- 顶部说明段落使用需求指定的文字，作为固定文案写在页面里。
- 对话状态是数组 `turns`：`{id, question, status: 'pending' | 'done' | 'error', answer?, citations?, error?}`，仅保存在组件 state（内存）中，**不写任何 Web Storage、不发给后端**；组件卸载（刷新、切换标签、登出）即消失，满足"不保存历史会话"。
- 提交：追加一个 `pending` 的 turn，调用 `askKnowledge(question)`，成功后把该 turn 置为 `done`，失败置为 `error` 并带上文字；同一时间只允许一个在途请求，输入框与按钮在 `pending` 时禁用。失败不清除已有 turns。
- 布局：用户问题右对齐（气泡靠右），回答靠左；`pending` 显示「正在生成回答…」；新增 turn 后把对话区滚动到底部（DOM 的 `scrollIntoView`，不用内联脚本）。
- **每题独立**：请求体只含 `question`，不带此前的问答、不带 `messages`。理由：后端本来就"每个问题先检索再回答"，页面不存历史也不该把历史当上下文外泄给模型；代价是追问里的指代（"它""上一个"）无法被理解，需要用户补全，已写入 Non-goals。

**备选：** 把已有 turns 作为 `messages` 发给后端以支持追问——多轮上下文与"不保存历史"的取向冲突，并且要处理提示词长度与历史校验（后端有 10 条 / 2000 字的上限），不在本次范围。

### Decision 5：「依据回答」卡片与出处列表 —— 编号只由数组顺序决定

- `citations` 非空：渲染标题为「依据回答」的卡片，正文是 `answer`（纯文本，`white-space: pre-wrap`）；卡片下方渲染一个 `<ol>` 风格的出处列表，第 `i` 项（从 0 计）的前缀显示 `[i+1]`，随后是材料标题、切片序号、字符区间、摘录。编号来自 `map` 的下标，**不解析 `answer` 里的 `[n]`，也不据此增减出处**：后端已保证回答里的标注都在 `1..len(citations)` 内且顺序一致，页面只要忠实呈现即可保持"标注与出处一致"。
- `citations` 为空：只显示前端常量「资料中未找到相关内容」；**不渲染「依据回答」标题，也不渲染出处区**。即便后端此时的 `answer` 是同一句话，页面也不套用有出处的卡片样式——避免把"没有依据"包装成"依据回答"。
- 出处区 / 卡片使用语义化标签（`<section aria-label>`、`<ol>`），编号文字与列表语义各自独立，读屏时不会重复。

### Decision 6：渲染安全 —— 一律作为 React 文本节点

摘录来自用户上传的材料，回答来自模型（受材料内容影响，属于间接提示注入的输出），二者都不可信。做法：

- 只用 JSX 文本插值渲染；新页面**不**使用 `react-markdown`、不使用 `dangerouslySetInnerHTML`、不写 `innerHTML`。`<script>`、`<b>`、`# 标题`、`**粗体**` 都原样显示为字符。
- 长文本用 CSS 保持可读：`white-space: pre-wrap; overflow-wrap: anywhere;`，避免长 URL / 无空格文本撑破布局。
- 不新增任何内联脚本或内联样式；样式全部进 `index.css`，与现有 CSP 兼容（`style-src 'self'`）。滚动等交互只用 DOM API。

**备选：** 把回答按 Markdown 渲染以获得列表 / 加粗——`react-markdown` 虽默认不渲染原始 HTML，但模型输出可含链接与图片，且会让 `[1]` 之类的标注被 Markdown 语法误处理（`[1]` 后面跟括号会变链接）；纯文本最稳，故不采用。

### Decision 7：样式 —— 沿用主题变量，不新增依赖

所有新样式使用 `index.css` 中已有的 CSS 变量（`--text`、`--card-bg`、`--border`、`--accent`、`--error` 等），因此深浅色自动生效。要点：导航为一行标签，选中态用底部强调线 + 粗体，窄屏可换行；用户气泡靠右（`align-self: flex-end`，对话容器为纵向 flex）、回答卡片靠左；出处项用浅底与边框区分；错误文字用 `--error`。不引入 UI 库或图标库。

### Decision 8：验收方式 —— 浏览器实测为主，不新增测试框架

仓库的前端目前没有单元测试框架，这两页的逻辑主要是"状态 + 渲染 + 错误映射"。为它们引入 Vitest / Testing Library 会新增一批 dev 依赖，收益有限。因此：

- 每个任务用 `npm run build`（含 `tsc -b`）与 `npm run lint` 兜底类型和静态问题，再在真实浏览器里逐条核对 spec 场景（内置浏览器窗格），网络面板核对请求体与请求头。
- 依赖服务的状态用开发编排里已有的手段制造：停掉 `qdrant` 得到 503；向 `revoked_tokens` 插入当前 token 的 `jti` 得到 401；关闭网关桩的对话 / 嵌入开关；用一份含 `<script>`、HTML 标签与 Markdown 标记的材料验证纯文本渲染。
- CSP 相关场景必须在生产 nginx（`http://localhost:8080`）上验证，Vite 开发服务器没有该策略。

**备选：** 引入 Vitest + Testing Library 写组件测试——后续页面增多时值得做，本次先不为两页承担依赖与配置成本；风险见下。

## Risks / Trade-offs

- **[没有自动化前端测试]** 回归靠手工场景 → 把场景与结果记入文档，导航 / 布局重构后重点回归材料页。
- **[重构 `MaterialsPage` 页头可能破坏现有页面]** → 只搬页头与容器样式、不动其业务逻辑；用迭代 1 的材料页场景（搜索、详情、上传、下载、主题、登出）逐条回归。
- **[切片序号（从 0 起）与引用编号 `[n]`（从 1 起）并存易混淆]** → 两者标签不同：引用编号用方括号，切片序号带「切片序号」文字前缀；不为迁就而改动后端的序号约定。
- **[503 文案固定，隐藏了"关键词模式仍可用"这一信息]** → 需求给定了文案；页面仍然保留模式选择，用户自行换成关键词即可，本次不加额外引导。
- **[模型可能不遵守引用格式]** → 后端已移除越界标注；页面不改写回答，因此可能出现回答中没有 `[n]` 但 `citations` 非空的情况，此时出处仍照常列出，属于模型质量问题而非页面缺陷。
- **[无追问上下文]** 每题独立 → 已在 Non-goals 与页面说明中体现；如需多轮，另开变更并明确历史的存放与上限。
- **[摘录可能较长（后端截到约 300 字）]** → `pre-wrap` + `overflow-wrap:anywhere`，不折叠；可读性够用。
- **[在途请求与页面卸载]** → 卸载时中止请求，忽略中止错误，不显示误导性的失败提示。

## Migration Plan

纯前端变更，无数据与接口迁移。

1. 前置：`add-traceable-vector-retrieval` 已部署（含 Qdrant 与网关 / 网关桩），否则页面会一直得到 503。
2. 部署：`docker compose up --build -d`（只会重建 `web` 镜像）。
3. 验证：登录后能看到三个标签；用预置账号在两个页面各做一次成功、无命中、503 的检查。
4. 回滚：回退前端提交并重建 `web`；后端与数据未受影响。
