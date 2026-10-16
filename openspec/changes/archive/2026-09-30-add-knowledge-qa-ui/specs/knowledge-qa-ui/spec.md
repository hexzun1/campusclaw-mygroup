## Purpose

让已登录的教师与学生在页面上检索本班材料、就本班材料提问，并在页面上核对每条结果与每个引用来自哪份材料的哪一段；页面只呈现既有接口的结果，不承担访问控制，也不保存对话。

## ADDED Requirements

### Requirement: 页面导航与访问控制

已登录页面的顶部 MUST 提供导航，「材料」「知识检索」「知识问答」三个标签并列，当前页面对应的标签 MUST 有明确的选中标识。学生与教师 MUST 都能访问这三个页面，且看到相同的检索与问答功能。未登录时访问知识检索页或知识问答页 MUST 回到登录页；页面上的访问限制只是显示层面的处理，真正的鉴权由服务端接口完成。

#### Scenario: 三个标签并列且标出当前页

- **WHEN** 任意角色登录后打开材料页
- **THEN** 页面顶部 MUST 同时显示「材料」「知识检索」「知识问答」三个标签
- **AND** 「材料」标签 MUST 处于选中状态
- **AND** 点击其它标签后 MUST 进入对应页面，且该标签变为选中状态

#### Scenario: 学生与教师都能使用检索和问答

- **WHEN** student_a1 与 teacher_a 分别登录并打开知识检索页与知识问答页
- **THEN** 两个角色 MUST 都能看到并使用输入框、提交按钮，功能与展示一致
- **AND** 不因角色不同而隐藏这两个页面

#### Scenario: 未登录不能进入新页面

- **WHEN** 未登录用户直接访问知识检索页或知识问答页的地址
- **THEN** 页面 MUST 回到登录页，且 MUST NOT 发出检索或问答请求

#### Scenario: 刷新后停留在当前页

- **WHEN** 已登录用户在知识检索页或知识问答页刷新浏览器
- **THEN** 页面 MUST 仍停留在该页面
- **AND** 输入框中的内容与页面上已显示的结果 MAY 被清空

### Requirement: 知识检索的输入与模式

知识检索页 MUST 提供一个问句输入框、检索模式选择和提交入口。模式 MUST 可选 `keyword`、`vector`、`hybrid`，默认 `hybrid`。提交时 MUST 调用 `POST /api/search`，请求体只含问句与所选模式，MUST NOT 含班级标识或角色；问句去除首尾空白后为空 MUST 不发请求，输入长度 MUST 被限制在接口允许的上限（500 个字符）之内；请求进行中 MUST 禁止重复提交。

#### Scenario: 默认使用混合模式

- **WHEN** 用户打开知识检索页并直接提交一个问句
- **THEN** 请求体中的模式 MUST 为 `hybrid`

#### Scenario: 按所选模式检索

- **WHEN** 用户把模式选为 `keyword` 或 `vector` 后提交问句
- **THEN** 请求体中的模式 MUST 与所选一致

#### Scenario: 空问句不发请求

- **WHEN** 用户在输入框为空或只有空白字符时提交
- **THEN** 页面 MUST NOT 发出检索请求

#### Scenario: 请求不携带班级信息

- **WHEN** 用户提交检索
- **THEN** 检索请求的请求体、query 与自定义 Header 中 MUST NOT 出现班级标识或角色字段
- **AND** 请求 MUST 带 `Authorization: Bearer <token>` 头

#### Scenario: 超长问句无法提交

- **WHEN** 用户试图输入超过 500 个字符的问句
- **THEN** 输入框 MUST 阻止超出上限的输入，页面 MUST NOT 发出超长请求

#### Scenario: 请求进行中不能重复提交

- **WHEN** 一次检索尚未返回时用户再次点击提交
- **THEN** 页面 MUST NOT 发出第二个请求，且 MUST 显示正在检索的状态

### Requirement: 检索结果展示

检索成功且有命中时，页面 MUST 按接口返回的顺序逐条展示：材料标题、切片序号、字符偏移区间与摘录。检索成功但无命中时，页面 MUST 显示「资料中未找到相关内容」，且 MUST NOT 显示任何结果条目。新一次检索返回后，页面 MUST 只展示这一次的结果，不混入上一次的结果。

#### Scenario: 命中结果逐条展示

- **WHEN** student_a1 以 `hybrid` 检索 A 班材料中存在的内容，接口返回若干命中
- **THEN** 页面 MUST 逐条展示每个命中的材料标题、切片序号、字符偏移区间与摘录
- **AND** 条目顺序 MUST 与接口返回的 `hits` 顺序一致
- **AND** 展示的每个字段 MUST 与接口返回的对应字段取值相同

#### Scenario: 无命中的统一提示

- **WHEN** 接口返回 200 且 `hits` 为空
- **THEN** 页面 MUST 显示「资料中未找到相关内容」
- **AND** MUST NOT 显示任何结果条目

#### Scenario: 新结果替换旧结果

- **WHEN** 用户在第一次检索的结果显示之后，再提交第二次不同的检索
- **THEN** 页面 MUST 只显示第二次检索的结果，第一次的条目 MUST 不再出现
- **AND** 第二次检索失败时，页面 MUST 显示失败提示而不是继续展示第一次的结果

### Requirement: 知识问答的对话展示

知识问答页 MUST 在顶部显示说明文字：「每个问题先在本班做混合检索，取前 4 条切片再生成简短回答，并标出 [1]、[2]；没有命中时不调用模型」。页面 MUST 以对话形式展示：用户的问题靠右显示，对应的回答显示在其后。提交时 MUST 调用 `POST /api/ask`，请求体只含当前问题，MUST NOT 含班级标识、角色、`system` 消息或此前的对话；问题去除首尾空白后为空 MUST 不发请求，输入长度 MUST 被限制在接口允许的上限（1000 个字符）之内；一个问题尚未返回时 MUST 禁止再提交下一个。

#### Scenario: 顶部说明文字

- **WHEN** 用户打开知识问答页
- **THEN** 页面顶部 MUST 显示上述说明文字，其中 MUST 包含"前 4 条切片""[1]、[2]""没有命中时不调用模型"

#### Scenario: 用户问题靠右显示

- **WHEN** 用户提交一个问题
- **THEN** 该问题 MUST 显示在对话区域的右侧
- **AND** 回答 MUST 显示在该问题之后，靠左对齐

#### Scenario: 每个问题独立提问

- **WHEN** 用户在同一页面先后提问两个问题
- **THEN** 第二次请求的请求体 MUST 只含第二个问题，MUST NOT 含第一个问题或其回答
- **AND** 请求体 MUST NOT 含 `messages` 字段，也 MUST NOT 含任何 `system` 角色内容

#### Scenario: 空问题与超长问题

- **WHEN** 用户提交空白问题，或试图输入超过 1000 个字符
- **THEN** 页面 MUST NOT 发出请求，输入 MUST 被限制在上限之内

#### Scenario: 回答返回前不能连续提问

- **WHEN** 一个问题尚未收到回答时用户再次点击提交
- **THEN** 页面 MUST NOT 发出第二个请求，并 MUST 显示"正在生成回答"之类的等待状态

### Requirement: 依据回答与出处列表

接口返回有出处的回答时，页面 MUST 用标题为「依据回答」的卡片显示回答文字，并在卡片下方按 `citations` 的顺序列出出处，第 n 项 MUST 标注为 `[n]`，每项显示材料标题、切片序号、字符偏移区间与摘录，使回答中的 `[1]`、`[2]` 与出处列表的编号一一对应。页面 MUST 原样显示接口返回的回答文字与出处，MUST NOT 自行增删、重排出处或补造引用。接口返回无出处（`citations` 为空）时，页面 MUST 只显示固定文案「资料中未找到相关内容」，MUST NOT 显示「依据回答」标题，也 MUST NOT 显示出处列表。

#### Scenario: 回答与出处编号一致

- **WHEN** student_a1 就 A 班材料里有依据的内容提问，接口返回 3 条 `citations` 和含 `[1]`、`[2]` 标注的回答
- **THEN** 页面 MUST 显示标题为「依据回答」的卡片，卡片中的回答文字与接口返回的 `answer` 一致
- **AND** 卡片下方 MUST 列出 3 条出处，依次标注 `[1]`、`[2]`、`[3]`，顺序与 `citations` 一致
- **AND** 每条出处 MUST 显示材料标题、切片序号、字符偏移区间与摘录

#### Scenario: 不补造出处

- **WHEN** 接口返回的 `citations` 有 2 条
- **THEN** 出处列表 MUST 恰好显示 2 条
- **AND** 页面 MUST NOT 因回答文字里出现其它编号而增加或推测出处

#### Scenario: 无命中只显示固定文案

- **WHEN** student_a1 询问 A 班材料里没有依据的问题，接口返回 `citations` 为空
- **THEN** 页面 MUST 只显示「资料中未找到相关内容」
- **AND** MUST NOT 显示「依据回答」标题，MUST NOT 显示任何出处

#### Scenario: 出处不越过班级

- **WHEN** student_a1 就只存在于 B 班材料中的内容提问
- **THEN** 页面 MUST 显示「资料中未找到相关内容」，且页面上 MUST NOT 出现任何 B 班材料的标题或摘录

### Requirement: 检索与问答的错误处理

两个页面 MUST 以一致的方式处理失败：接口返回 503 时 MUST 显示「检索服务暂不可用」，且 MUST NOT 显示依赖服务的地址、密钥或内部错误详情；接口返回 401 时 MUST 清除 token 并回到登录页；接口返回 400 时 MUST 显示接口给出的原因；其它失败（如网络中断）MUST 显示通用的失败提示。失败 MUST NOT 造成页面无响应，用户 MUST 能修改输入后重新提交；问答页上已显示的对话 MUST 在失败后保留。

#### Scenario: 服务不可用时的提示

- **WHEN** 向量库或网关不可用，用户以 `hybrid` 或 `vector` 检索，或在问答页提问，接口返回 503
- **THEN** 页面 MUST 显示「检索服务暂不可用」
- **AND** 页面上 MUST NOT 出现依赖服务的地址、端口或密钥

#### Scenario: 关键词模式在依赖不可用时仍可用

- **WHEN** 向量库与网关都不可用，用户以 `keyword` 模式检索本班材料中存在的内容
- **THEN** 页面 MUST 正常显示命中结果，MUST NOT 显示「检索服务暂不可用」

#### Scenario: 登录失效回到登录页

- **WHEN** 检索页或问答页发出的请求返回 401（如 token 已被登出吊销）
- **THEN** 页面 MUST 清除 token 并回到登录页

#### Scenario: 请求被拒绝时显示原因

- **WHEN** 接口对检索或提问返回 400 并给出原因
- **THEN** 页面 MUST 显示该原因，且用户 MUST 能修改输入后再次提交

#### Scenario: 失败后对话仍在

- **WHEN** 问答页已有一轮问答，用户提出的下一个问题得到 503
- **THEN** 已显示的前一轮问答 MUST 仍在页面上
- **AND** 失败的那个问题旁 MUST 显示失败提示

### Requirement: 渲染安全与内容不落地

检索摘录、材料标题、模型回答与出处内容 MUST 一律作为纯文本显示：其中出现的 HTML 标签或 Markdown 标记 MUST 原样显示为文字，MUST NOT 被解析、渲染或执行。检索与问答的输入和输出 MUST NOT 写入 `localStorage`、`sessionStorage` 或 Cookie（`sessionStorage` 中仍只允许存在 token）；问答的对话 MUST 只存在于页面内存中，刷新、离开页面或登出后 MUST 消失。这些页面 MUST 在既有的内容安全策略下正常工作，不产生策略违规。

#### Scenario: HTML 与脚本按文字显示

- **WHEN** 某份 A 班材料的内容含 `<script>alert(1)</script>` 与 `<b>加粗</b>`，用户检索到它，或问答的出处 / 回答中包含这些内容
- **THEN** 页面 MUST 把这些标签原样显示为文字，MUST NOT 弹出对话框，MUST NOT 出现加粗效果

#### Scenario: Markdown 标记不被渲染

- **WHEN** 摘录或回答中含 `# 标题`、`**粗体**`、`- 列表` 等 Markdown 标记
- **THEN** 页面 MUST 按原文显示这些字符，MUST NOT 渲染成标题、粗体或列表

#### Scenario: 对话不落地

- **WHEN** 用户在两个页面完成检索与问答后检查浏览器的 `localStorage`、`sessionStorage` 与 Cookie
- **THEN** `localStorage` 与 Cookie MUST 不含检索或问答的内容，`sessionStorage` 中 MUST 只含 token
- **AND** 用户刷新问答页或切换到其它标签再回来后，此前的对话 MUST 不再显示

#### Scenario: 内容安全策略下无违规

- **WHEN** 在生产部署（带内容安全策略）下使用检索页与问答页完成检索、提问、切换标签、切换主题与登出
- **THEN** 每项操作 MUST 正常完成，浏览器控制台 MUST 无策略违规报告
