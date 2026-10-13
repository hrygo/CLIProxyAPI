# Responses 客户端工具协议（核心）

本文描述核心对 Responses 客户端工具协议的处理：客户端执行的 `tool_search`
发现流程与 `custom` 工具。处理完全位于核心，无需任何插件或动态库。

代码基准：`743a5e0a`；核验日期：2026-10-14。长期资料入口为
[设计与实施依据](tool-loading/design-and-implementation_CN.md)、
[验证与兼容报告](tool-loading/verification-and-compatibility_CN.md)及
[逐请求 token 报告](tool-loading/token-usage-report_CN.md)。

## 背景

延迟加载取决于客户端的有效模式及模型/provider metadata，包括 Codex 搜索
profile 的 `supports_search_tool`。客户端回退到 eager 时，完整工具面可能进入
任务首包。Native ToolSearch 和 Code Mode 是不同模式，证据分别判定。

官方客户端固定 scope 的逐请求用量见 token 报告。首包声明减少不单独证明
全任务输入、稳定延迟或账单节省。

## 问题

要压缩首包就必须有发现协议，而两种客户端执行的工具形态都无法干净地映射到
纯 `function` 工具列表：

- `tool_search`——模型发出搜索调用，客户端返回匹配到的工具定义，之后才允许
  调用这些被发现的工具。
- `custom` 工具——自由字符串输入，可选地受语法约束。

只转发已声明工具列表的代理会破坏这个闭环：延迟工具在客户端搜索之前就被展开，
被发现的工具名返回时被改名，模型的搜索调用与客户端的搜索结果不再匹配，
`custom` 调用则被只接受严格 function schema 的上游拒绝。能力判定也发生在错误
的位置——由模板和 provider 类型推断，而不是由实际选中的路由决定——于是模型
被宣称支持搜索，实际却无法闭合该闭环。

## 价值

当客户端执行的 `tool_search` 闭合整条往返链路后，延迟工具不再进入首包，只有
模型真正搜索时才被物化。这是本功能最高价值的结果，也正是下面这套协议契约
值得其复杂度的原因：搜索闭环必须能挺过改名、历史重放、流式传输与 provider
翻译，而不静默丢失任何工具。

## 目标与非目标

目标：

- 保持首包精简：延迟工具绝不物化进首个请求，激活它们的唯一路径是发现流程。
- 仅用核心功能完成客户端 `tool_search` 与 `custom` 桥接，无需安装动态库。
- 约定大于配置：未声明任何配置的路由同样按运行时已知信息得到正确策略。
- 按实际上游路由（选定 provider、认证类别、真实上游模型、实际协议、
  endpoint 限定）解析策略，不从客户端 alias 推断能力。
- 仅当公开模型 alias 背后全部可选路由允许原生或桥接搜索策略时，
  才声明 `supports_search_tool`；该保守策略投影不是实测认证。

非目标：Muse 专用 provider、工具执行服务器、第二套通用 JSON-RPC ABI、
自动模型回退器、跨会话工具内容数据库。

## 配置

`requests.responses-tools` 是唯一配置源。`models.json`、模型 alias、插件
YAML 中不存在第二套可覆盖策略。所有字段都是可选的：整块缺省即按约定生效。

```yaml
requests:
  responses-tools:
    enabled: true          # omitted means enabled; explicit false disables all
    limits:
      max-active-tool-bytes: 262144
      max-active-attempts: 512
      max-state-bytes: 33554432
      max-attempt-bytes: 1048576
      max-schema-expansion-bytes: 65536
      max-schema-expansion-nodes: 10000
      max-depth: 64
    routes:                 # optional convention overrides
      - match:
          provider: "xai"
          auth-kind: "api-key"
          upstream-model: "grok-4.5"
          upstream-format: "codex"
          base-url: "https://api.x.ai/v1"
        client-search: "bridge"
        custom-grammar: "reject"
        schema:
          complete-search-required: false
          local-refs: "preserve"
```

### 路由标识

`match` 各字段必须描述运行时**实际选中**的路由，而不是客户端输入的模型名。
provider 到格式的映射是固定的：

| `provider` | `upstream-format` |
|---|---|
| `codex`、`xai`、`meta` | `codex` |
| `claude` | `claude` |
| `gemini`、`vertex`、`aistudio` | `gemini` |
| `kimi` | `openai` |
| `antigravity` | `antigravity` |
| `openai-compatible-<name>` | `openai` |

这里的 `codex` 是 OpenAI Responses 方言的内部名称，不是厂商标识：`xai` 与
`meta` 说的是 Responses 线协议，因此复用同一个 translator。合法匹配值与实际
路由不符时不覆盖约定；未知策略枚举和冲突配置则在加载时另行拒绝。

## 默认行为

常规场景无需任何配置。有效策略由上游格式**与** provider 共同推导：

- 只有当上游格式为 `openai-response` 或 `codex`，**并且** provider 原样
  透传 `tool_search` 内建工具时，才是 `client-search: native`。目前只有
  `codex` 与 `meta` 满足。搜索 built-in 和关联历史保持 native；Lite、portable
  声明或响应侧身份修复仍可能进入管理路径，不能仅凭 native 策略判断 duplex。
  继承原生 Lite 保留请求字节，精确 `tool_search/tool_search_tool` 函数 alias
  可在响应侧恢复为对象参数的 `tool_search_call`；声明的同名业务工具不误判。
- 其余上游一律 `client-search: bridge`，**包括**那些说着原生 Responses
  格式但会改写内建工具的 provider：`xai` executor 会把它剥掉，走原生透传
  会静默丢失 discovery。bridge 在客户端真正声明 `tool_search` 之前完全不
  生效，不带该声明的请求原样通过。
- 对能接受原生工具面的 provider，一律 `custom-tools: inherit`、
  `custom-grammar: reject`。核心从不自行推断 custom 处理方式。
- 所有路由一律 `schema.local-refs: preserve`、
  `schema.complete-search-required: false`。约定从不把其中任何一个改写为
  重写类取值。目前只有 `meta` 需要可移植工具面：它拒绝 `type: custom`，
  且要求每个属性都出现在 `required` 中，还拒绝递归 schema，因此约定会
  对它采用 `custom-tools: function`、`custom-grammar: describe`、
  `schema.complete-search-required: true` 与 `schema.local-refs: flatten`。
  在 `custom-tools: function` 的 portable native 路由，搜索结果中的有效声明
  激活到顶层 `tools`，与 bridge 共用累计身份及预算检查。Lite
  `input[].additional_tools` 在 eager 和搜索请求中提升到顶层后再适配；
  匹配 namespace 合并、相同声明去重、冲突本地 422。空 description
  使用中性说明，非空客户端说明及剩余历史身份保留。普通 eager namespace
  的 qualified response name 可恢复原 leaf/namespace，不改参数或 call ID。

  提升改变声明可见范围。内部 `Prepared.Transformation` 报告原索引、交错及
  同身份更新；提升报告 `position_preserved: false` 和
  `additional_tools_position_lift`。该 sidecar 不是 HTTP 响应字段，不认证语义
  等价，也不改路由策略。纯原生及仅响应修复报告声明位置保留；portable
  路由的精确位置语义未认证，当前无严格位置配置开关。

  `schema.local-refs: inline` 对所有 provider 都是手动开启，`meta` 也不
  例外：约定从不展开客户端自己写的 `$ref`；flatten 只把成环的引用替换
  为无约束 schema，非环 `$defs` 保持原样。
  显式 inline/flatten 同样处理没有发现或 custom metadata 的普通 function
  声明；inline 的递归 schema 本地拒绝，不因普通函数路径绕过检查。

有损策略保持手动开启，因为它们要么丢失能力，要么改写用户 schema。唯一的
例外是上文所述的可移植工具面折叠——对 `meta` 而言这是约定而非可选项，
因为该上游不接受其他方案：

- `custom-tools: strip` 与 `custom-grammar: describe`
- `schema.complete-search-required: true` 与 `schema.local-refs: flatten`
- `client-search: disabled`，用于单条路由关闭搜索

`routes[]` 规则只覆盖它自己写出的策略；每一条未写出的策略会各自解析为
该调用实际所用路由的约定，因此规则改一条策略时仍保留该 provider 约定的
其余部分。写了一条 schema 策略并不会让该路由放弃另一条：
`schema.local-refs` 与 `schema.complete-search-required` 各自独立解析。

`enabled: false` 是紧急总闸：不删除任何其他配置即可整体关闭。

策略枚举：

- `client-search`：`inherit`（按该路由的约定）、`native`（真实原生
  Responses 路由透传）、`bridge`（function 翻译）、`disabled`（明确的
  客户端搜索请求返回 422，不静默丢弃）。
- `custom-tools`：`inherit`、`native`、`function`（包装与恢复受支持调用）、
  `strip`（有损：删除未来声明但保留可转换历史；强制调用被删除工具返回
  422）、`reject`（遇到 custom 契约直接 422）。
- `custom-output-replay`：`single`（适配历史默认；同 call 多输出返回
  422 `unsupported_protocol`）、`multiple`（逐项保留结果）。multiple
  只允许显式匹配 `codex` 或 `openai-response` 上游格式，不自动继承；
  具体 Provider 接受性另验。完整 payload、call ID、可逆 item ID 及全局顺序
  保留，重复调用和孤立结果仍拒绝；最终发送契约检查结果数量、完整 item
  摘要及工具事件顺序。context 保存摘要和身份，不保存原始结果正文。
- `custom-grammar`：`reject`（默认）、`describe`（仅 function 模式：把
  grammar 语法折叠进说明；上游不保证 grammar 采样约束）。
- `schema.complete-search-required`：三态。缺省按该路由的约定解析，
  `true` 补齐 required 并把可选字段放宽为 nullable，`false` 保持声明
  原样。它只适配 client-search 参数 schema，不全局 strictify 所有工具。
- `schema.local-refs`：`preserve`（默认）或 `inline`（展开本地引用；
  递归、外部、缺失或不支持的引用直接失败，不删除约束）或 `flatten`
  （所有引用保持原样，只有成环的引用变为无约束 schema；外部或缺失
  引用仍然直接失败，不删除约束）。

匹配规则：

- `provider`、`auth-kind`、`upstream-model`、`upstream-format` 均为必填精确
  条件。不支持通配符、正则，也不从模型名推断能力。
- API-key 路由必须填 `base-url`，避免同一 provider/model 名在不同 endpoint
  共用一条规则。OAuth 路由留空。
- 重叠且策略冲突的规则在加载时拒绝；选路时动态冲突返回明确配置错误，
  不做静默 first-match。
- 限额必须为正且有合理上界；负数不表示无限。

## 回滚

设置 `requests.responses-tools.enabled: false`。其余配置不受影响，约定策略
在下一次请求起停止生效。修改路由规则时，等待活跃请求排空后再切换，避免
单个请求中途更换协议语义。

## item 身份与历史修补

Responses 的 item id 带有生成它的 item 类型的命名空间。桥接会改变 item 类型，
id 必须随之改变：客户端原样保存代理发出的 output item，并在下一轮原样重放，
严格的上游会拒绝命名空间不符的 id。

| item 类型 | id 前缀 |
|---|---|
| `function_call` | `fc_` |
| `function_call_output` | `fco_` |
| `custom_tool_call` | `ctc_` |
| `custom_tool_call_output` | `ctco_` |
| `tool_search_call` | `tsc_` |
| `tool_search_output` | `tso_` |

只有这六类参与。`message`、`reasoning` 及其他 item 保留上游给出的身份。显式
类型转换只允许以下方向，且每一对都可逆：

- `function_call` ↔ `custom_tool_call`
- `function_call` ↔ `tool_search_call`
- `function_call_output` ↔ `custom_tool_call_output`
- `function_call_output` ↔ `tool_search_output`

请求适配之前，若重放的 `input[]` item 的类型与 id 前缀构成上述配对，就地迁移
该 id。修补只改 id 的值：不重新编码整个请求，其他字节不变。修补在路由策略判断
之前执行，因此完整的污染历史在原生路由上无需声明工具、也无需建立 attempt 即可
恢复。

修补明确不做的事：

- 不猜测。服务端执行的 search、不属于任何已知命名空间的 id、缺失或非字符串
  id，以及所有非工具 item 一律不动。
- 不补造请求 id。input item 的可选 id 可以缺省。
- 不解析 opaque 历史。需要修补且请求带 `previous_response_id`、
  `previous_item_id` 或 `item_reference` 时返回 422 `opaque_history`；绝不为
  让修补通过而删除父引用。无需修补的 opaque 历史不受影响。
- 不复用 id。当迁移会让两个 item 拥有同一客户端可见 id 时返回 422
  `ambiguous_identity`，不随机改名，也不按出现顺序决定身份。

响应侧执行方向相反的同一套转换。桥接 item 缺少可用 id 属于上游协议错误，
返回 502 `upstream_contract`：代理凭空生成的 id 会被客户端保存并在下一轮重放。
恢复工具名称不改变 item 类型——名称解析到 custom 工具并不构成上游产生了
custom 调用的证据。

流式中，客户端可见的类型与 id 在 item 首次被跟踪时计算一次，之后所有引用复用
同一值：`added` item、参数 delta 与 done、`output_item.done` 以及终态
`response.completed` 的 output 都指向同一个 id。上游 id 仍是跟踪键，因此既有的
`call_id` 与 `output_index` 一致性校验仍然比较上游真实发送的内容。

协议类型和字段按解码后的 JSON 含义识别，包括合法 Unicode 转义。普通
schema/参数中的同名字符串不是历史协议；Claude 延迟身份输入中的非法
UTF-8 和孤立 surrogate 被拒绝，不替换为其他名称。

Chat→Responses 流要求上游 `[DONE]` 与匹配的合法 completed/incomplete
终态。`[DONE]` 不完成未闭合的工具调用；缺 finish、截断或非法参数流不补造
arguments 或 completed。明确 reasoning-only finish 仍可合法映射。
调用 item 完整与 response 完整是不同边界；暴露工具调用后的断流不会跨凭据
重放该调用。参数是否可执行继续由客户端/执行服务验证。

原生 WebSocket duplex 连接会修补每一个后续的 `response.create`、
`response.append` 和 `response.steer` 帧，而不只是首个请求：这些帧不经过
Manager。无法修补的帧以携带相同状态码与 reason 的本地 error frame 应答，不
向上游发送，也不会进入 pending 或 steering 状态。连接建立后，该状态码存在于
error frame 内部，不改写 HTTP 状态。

## 预算

| 配置 | 含义 |
|---|---|
| `limits.max-active-tool-bytes` | 单次尝试保留的工具声明字节 |
| `limits.max-active-attempts` | Manager 范围内并发保留的尝试数 |
| `limits.max-state-bytes` | Manager 共享状态预算 |
| `limits.max-attempt-bytes` | 单次尝试状态上限 |
| `limits.max-schema-expansion-*` | schema 改写上限 |

这些上限约束的是桥接自身保留的状态：工具声明、被跟踪的调用、暂存的
arguments 以及 SSE 缓冲。转发的请求正文属于调用方内容，代理在桥接之前
已完整持有，因此不再计入桥接 attempt 状态预算；这不是模型 context window
或完整进程内存限额。活跃声明预算在裁剪未加载声明后检查累计保留集合。

诊断只携带路由/策略 generation、工具种类计数、声明字节、已用预算和
固定原因码；不记录 arguments、patch、grammar 正文、prompt、结果、令牌或
凭据。

## 边界

- `interactions`、直接插件 executor 路由、自定义未知 executor、
  images/video、`responses/compact` 不启用桥接；保持各自路径，也不新增
  搜索能力声明。
- 已配置规则命中不支持目标时返回明确 422，不忽略规则后透传。
- `CountTokens` 共用请求 Prepare 与声明预算，但不建立响应流状态；
  无法计数的 executor 各自返回自身错误。
- 原生有状态透传/steering：只允许无需转换的原生透传；需要桥接的普通
  WebSocket 走 replay；需要桥接的 live steering 组合明确拒绝。live duplex
  不做逐帧桥接。
  每 turn gate 使用与 HTTP 相同的 Prepare，覆盖普通 schema、Lite 提升、
  响应侧修复、ID 修复及本地拒绝；私有探针返回前释放，无上游或工具 dispatch。
  实际 alias/固定凭据保持真实协议和 endpoint，部分 route hint 不覆盖它们。
  replay 中 `response.steer` 返回本地 422；replay 成功不认证 duplex。
- 服务端历史：只转换本地可展开历史；依赖远端 opaque
  `previous_response_id` 的请求在需要转换的路由上返回 422。原生路径不受
  该限制。

## 多个 custom 输出

同一次 custom 执行可以用同一 `call_id` 回填通知和最终结果。在 function/strip
适配路由的默认 single 策略下，合法多结果返回 request-scoped、availability-neutral
的 422 `unsupported_protocol`，不打开上游，不静默合并、丢弃或降为 user text。
重复 call、缺失 call ID、孤立/顺序错误结果仍为 `invalid_custom_input`。

显式 multiple 的原生 Responses 路由逐项重放，并检查调用参数、结果内容、
metadata、身份、数量和全局工具事件顺序；规范化摘要保留精确数字 token。
指定历史 GPT route 有合成历史接受性和官方 Codex 编码 pair 的有限证据。
Muse 明确要求一调用一输出；其他跨协议路径不能外推为已兼容。
详见[验证报告](tool-loading/verification-and-compatibility_CN.md)。

## 累计发现

Bridge 和 portable native function 路由累计显式历史中的发现身份。先 A 后 B
仍可调用 A；后轮更新同一身份采用最新声明，先前调用和结果保留历史。
同轮冲突返回 422 `ambiguous_identity`。直接 function choice 与 allowed_tools
使用活动 alias；portable 原生搜索 choice 保持 native。

累计集合整体超预算返回 413 `declaration_budget`，不隐式淘汰旧轮次，不发送
部分集合或切换凭据。拒绝释放 attempt lease。纯原生透传沿用上游自身激活语义。
发现不是执行授权；客户端或 MCP 服务按当前权限和原始 schema 在 dispatch 前
校验，移除后轮搜索引用本身不证明较早权限已撤销。

## Claude 延迟工具适配

非原生 Messages 路由按 deferral 字段、内容 reference block 与关联历史识别协议，
检查有效声明和 ToolSearch 绑定，裁剪未加载目录及占位项，再检查活跃预算。
普通文字、名称、schema/参数中的同名字段保持普通路径；重复成员与 Unicode
转义不能隐藏真正协议。原生 Anthropic 和总闸关闭路径保留原行为。

Execute、CountTokens、ExecuteStream 传递活跃集合的最终发送契约，删除、
重命名、重复、新增未加载函数及非法最终选择在发送前拒绝。Gemini 使用已有
消歧名称映射，回应优先精确还原，不以大小写或下划线 fallback 串名。
此保护有明确集合范围，不是全部历史内容的通用签名。

## 诊断与能力范围

准备和响应失败 observer 可选启用。响应阶段限定 `response_restore`、
`stream_feed`、`stream_finish`，仅 typed error 输出固定 reason/status；
任意错误文本不作为可信归因，取消中不产生迟到诊断。

Capability profile 描述有效 route policy；HTTP/SSE declared 不是 live 证书，
WS replay/duplex 保持独立证据。公开 alias 的全部可选路线参与汇总，暂时
cooldown 或 region error 不隐藏候选；显式禁用目标排除。

Scoped 记录绑定代码、policy generation、客户端版本/build/mode、source
protocol、provider/model/format、endpoint、transport、catalog/fixture/permission
hash 与 region。未知维度保持 unknown，范围变动为 revalidation-required。
结构和摘要合法不认证证据真实性，也不自动改变路由或切回 eager。
