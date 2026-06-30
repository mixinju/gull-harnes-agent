# Day 7：整合与 CLI 交互

## 导语

前六天我们造好了所有零件——Tool 注册表、Skill 加载器、MCP 客户端、System Prompt 构建器、Context 上下文管理器。但这些零件散落在 `main.go` 里，硬编码的 API Key、写死的模型名、固定的用户输入，跑一次就得改代码。

问题很具体：

- API Key 直接写在 `main.go` 里，提交到 Git 就泄露了
- 模型名 `deepseek-v4-flash` 硬编码在 `agent.go:101`，换个模型得改代码
- 用户输入是硬编码的 `openai.UserMessage("北京今天天气怎么样？")`，每次跑都问同一个问题
- 输出混用 `fmt.Printf` 和 `log.Printf`，迭代到第几轮、调了什么工具、token 消耗多少，全靠肉眼在控制台刷屏里找
- 跑完就没了，历史对话丢失，没法回溯"上次那个任务模型是怎么一步步决策的"

今天要把这些零件**组装成一台能开的车**：配置文件管理所有可变参数，命令行接收用户输入，日志系统分层输出（终端看关键决策、文件存全量原始数据），会话模块持久化每次运行。

### 什么是 REPL

REPL 全称 **Read-Eval-Print Loop**（读取-求值-打印-循环），是一种交互式的命令行运行模式。它的工作循环是：

1. **Read**：读取用户输入的一行（或多行）文本
2. **Eval**：对输入求值/执行
3. **Print**：把执行结果打印到终端
4. **Loop**：回到第 1 步，等待下一次输入

你在终端里敲 `python3`、`node`、`irb` 进入的就是 REPL——输入一行代码，立刻看到结果，再输入下一行。它和"一次性脚本"的区别在于**持续会话**：前一次输入的变量、上下文在后续输入中仍然可用。

放到 Agent 场景里，REPL 意味着用户可以**连续对话**：

```
> 帮我看看 main.go
[Agent 读取文件并分析...]
> 那里面的 Agent 结构体有哪些字段？
[Agent 基于上一轮的上下文回答，不需要重新读文件...]
> 把 WithModel 改成支持从环境变量读取
[Agent 修改代码...]
```

注意第二轮"那里面的 Agent 结构体"——"里面"指的是上一轮读过的 `main.go`，REPL 的持续会话让模型天然拥有这个上下文。

### Claude Code 也是 REPL

Claude Code 就是一个典型的 Agent REPL。你在终端里启动 `claude`，进入一个持续会话：

```bash
$ claude
> 帮我看看这个项目的目录结构
[Claude Code 调用 ls/tree 工具，输出结构]
> 有哪些测试文件？
[基于上一轮看到的结构，直接回答，不需要重新执行 ls]
> 给 utils.js 加上单元测试
[读取 utils.js → 生成测试 → 写入文件]
```

它还支持斜杠命令这类特殊指令：

- `/help` —— 查看可用命令
- `/clear` —— 清空当前会话上下文
- `/cost` —— 查看本次会话的 token 消耗
- `/exit` —— 退出

这些指令不是发给模型的，而是 REPL 自身拦截处理。这就是"交互式"的代价——你得写一套命令解析层。

::: warning 本篇不做完整 REPL
实现一个生产级的 REPL 至少要处理：多行输入（`"""` 或 `\` 续行）、特殊指令解析（`/exit` `/clear` `/history`）、信号处理（Ctrl+C 中断当前任务而非退出）、会话恢复（从上次中断处继续）、流式输出（逐 token 打印而非等完）、终端UI构建等等。

这些每一项都不难，但堆在一起会让 `main.go` 从 100 行膨胀到 400 行，喧宾夺主——Day 7 的核心是"把模块组装起来"，不是"造一个终端框架"。

所以我们今天采用**一次性任务模式**：命令行传入 prompt，Agent 跑完输出结果就退出。这覆盖了 80% 的使用场景（脚本自动化、CI 流水线、一次性代码任务），且代码简洁。需要多轮对话时，基于现有的 `session` 模块扩展 REPL 也不迟——会话持久化已经做好了，恢复上下文只是 `session.Load()` 的事。
:::

## 本日目标

将 Day 1-6 的所有模块组装为完整框架，实现配置化、可观测、可回放的 CLI 工具。

## 你将学到

- 配置文件设计：JSON 结构 + 环境变量覆盖敏感字段
- CLI 参数解析：`flag` 包的用法，`-prompt` 和位置参数二选一
- 日志双通道：终端友好输出 + 文件结构化全量日志（基于 `log/slog`）
- 会话持久化：把每次运行的消息历史和指标保存为 JSON，便于回放
- Agent 重构：用函数式选项注入用户输入，消除硬编码

## 整体流程

把 Day 1-6 的模块组装后，一次完整的 Agent 运行流程如下：

<svg viewBox="0 0 660 780" xmlns="http://www.w3.org/2000/svg" style="width:100%;max-width:660px;margin:16px auto;display:block;font-family:-apple-system,'PingFang SC',sans-serif;font-size:12px">
  <defs>
    <marker id="arr" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse">
      <path d="M 0 0 L 10 5 L 0 10 z" fill="#475569"/>
    </marker>
    <marker id="arrR" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse">
      <path d="M 0 0 L 10 5 L 0 10 z" fill="#94a3b8"/>
    </marker>
  </defs>

  <!-- ===== 参与者头部 ===== -->
  <rect x="17" y="15" width="96" height="32" rx="4" fill="#3b82f6" stroke="#2563eb" stroke-width="1.5"/>
  <text x="65" y="31" text-anchor="middle" dominant-baseline="central" fill="#fff" font-weight="600">User</text>
  <rect x="147" y="15" width="96" height="32" rx="4" fill="#6366f1" stroke="#4f46e5" stroke-width="1.5"/>
  <text x="195" y="31" text-anchor="middle" dominant-baseline="central" fill="#fff" font-weight="600">Agent</text>
  <rect x="277" y="15" width="96" height="32" rx="4" fill="#3b82f6" stroke="#2563eb" stroke-width="1.5"/>
  <text x="325" y="31" text-anchor="middle" dominant-baseline="central" fill="#fff" font-weight="600">LLM</text>
  <rect x="407" y="15" width="96" height="32" rx="4" fill="#8b5cf6" stroke="#7c3aed" stroke-width="1.5"/>
  <text x="455" y="31" text-anchor="middle" dominant-baseline="central" fill="#fff" font-weight="600">Tool</text>
  <rect x="537" y="15" width="96" height="32" rx="4" fill="#10b981" stroke="#059669" stroke-width="1.5"/>
  <text x="585" y="31" text-anchor="middle" dominant-baseline="central" fill="#fff" font-weight="600">Session</text>

  <!-- ===== 生命线 ===== -->
  <line x1="65" y1="47" x2="65" y2="765" stroke="#cbd5e1" stroke-width="1.5" stroke-dasharray="4 4"/>
  <line x1="195" y1="47" x2="195" y2="765" stroke="#cbd5e1" stroke-width="1.5" stroke-dasharray="4 4"/>
  <line x1="325" y1="47" x2="325" y2="765" stroke="#cbd5e1" stroke-width="1.5" stroke-dasharray="4 4"/>
  <line x1="455" y1="47" x2="455" y2="765" stroke="#cbd5e1" stroke-width="1.5" stroke-dasharray="4 4"/>
  <line x1="585" y1="47" x2="585" y2="765" stroke="#cbd5e1" stroke-width="1.5" stroke-dasharray="4 4"/>

  <!-- ===== Agent 激活条 ===== -->
  <rect x="190" y="60" width="10" height="700" fill="#6366f1" fill-opacity="0.22" stroke="#6366f1" stroke-width="0.8"/>

  <!-- ===== 消息 1: User → Agent ===== -->
  <text x="127" y="74" text-anchor="middle" fill="#1e293b">Run(prompt)</text>
  <line x1="65" y1="82" x2="188" y2="82" stroke="#475569" stroke-width="1.5" marker-end="url(#arr)"/>

  <!-- ===== 消息 2: Agent → Session ===== -->
  <text x="392" y="108" text-anchor="middle" fill="#1e293b">记录 system + user 消息</text>
  <line x1="200" y1="116" x2="583" y2="116" stroke="#475569" stroke-width="1.5" marker-end="url(#arr)"/>

  <!-- ===== loop 片段框 ===== -->
  <rect x="35" y="138" width="600" height="545" fill="none" stroke="#94a3b8" stroke-width="1.2" stroke-dasharray="5 3" rx="2"/>
  <polygon points="35,138 98,138 106,150 106,158 35,158" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.2"/>
  <text x="43" y="151" fill="#1e293b" font-weight="600">loop</text>
  <text x="73" y="151" fill="#64748b">× N 轮迭代</text>

  <!-- 消息 3: Agent → LLM -->
  <text x="262" y="178" text-anchor="middle" fill="#1e293b">ChatCompletion(messages, tools)</text>
  <line x1="200" y1="186" x2="323" y2="186" stroke="#475569" stroke-width="1.5" marker-end="url(#arr)"/>

  <!-- 消息 4: LLM → Agent (返回) -->
  <text x="262" y="214" text-anchor="middle" fill="#1e293b">response(finish_reason, usage)</text>
  <line x1="325" y1="222" x2="202" y2="222" stroke="#94a3b8" stroke-width="1.5" stroke-dasharray="6 4" marker-end="url(#arrR)"/>

  <!-- ===== opt 1: token 超阈值 ===== -->
  <rect x="50" y="245" width="570" height="105" fill="none" stroke="#f59e0b" stroke-width="1.2" stroke-dasharray="5 3" rx="2"/>
  <polygon points="50,245 120,245 128,257 128,265 50,265" fill="#fef3c7" stroke="#f59e0b" stroke-width="1.2"/>
  <text x="58" y="258" fill="#92400e" font-weight="600">opt</text>
  <text x="82" y="258" fill="#64748b">token 超阈值</text>

  <text x="392" y="282" text-anchor="middle" fill="#1e293b">Compact() 压缩上下文</text>
  <line x1="200" y1="290" x2="583" y2="290" stroke="#475569" stroke-width="1.5" marker-end="url(#arr)"/>

  <text x="392" y="318" text-anchor="middle" fill="#ef4444" font-weight="600">若仍超 → finalize(error) 终止</text>
  <circle cx="195" cy="338" r="5" fill="#ef4444"/>

  <!-- ===== opt 2: finish_reason = length ===== -->
  <rect x="50" y="365" width="570" height="65" fill="none" stroke="#f59e0b" stroke-width="1.2" stroke-dasharray="5 3" rx="2"/>
  <polygon points="50,365 155,365 163,377 163,385 50,385" fill="#fef3c7" stroke="#f59e0b" stroke-width="1.2"/>
  <text x="58" y="378" fill="#92400e" font-weight="600">opt</text>
  <text x="82" y="378" fill="#64748b">finish_reason = length</text>

  <text x="392" y="405" text-anchor="middle" fill="#ef4444" font-weight="600">finalize(部分回复) 终止</text>
  <circle cx="195" cy="420" r="5" fill="#ef4444"/>

  <!-- ===== opt 3: 无工具调用 ===== -->
  <rect x="50" y="445" width="570" height="90" fill="none" stroke="#f59e0b" stroke-width="1.2" stroke-dasharray="5 3" rx="2"/>
  <polygon points="50,445 130,445 138,457 138,465 50,465" fill="#fef3c7" stroke="#f59e0b" stroke-width="1.2"/>
  <text x="58" y="458" fill="#92400e" font-weight="600">opt</text>
  <text x="82" y="458" fill="#64748b">无工具调用</text>

  <text x="262" y="483" text-anchor="middle" fill="#1e293b">输出最终回复</text>
  <text x="392" y="508" text-anchor="middle" fill="#10b981" font-weight="600">finalize(completed) 终止</text>
  <circle cx="195" cy="525" r="5" fill="#10b981"/>

  <!-- ===== 否则：执行工具 ===== -->
  <text x="125" y="557" fill="#64748b" font-style="italic">否则：模型发起了工具调用</text>

  <!-- 消息: Agent → Tool: Dispatch -->
  <text x="327" y="582" text-anchor="middle" fill="#1e293b">Dispatch(name, args)</text>
  <line x1="200" y1="590" x2="453" y2="590" stroke="#475569" stroke-width="1.5" marker-end="url(#arr)"/>

  <!-- 消息: Tool → Agent (返回): result -->
  <text x="327" y="616" text-anchor="middle" fill="#1e293b">result</text>
  <line x1="455" y1="624" x2="202" y2="624" stroke="#94a3b8" stroke-width="1.5" stroke-dasharray="6 4" marker-end="url(#arrR)"/>

  <!-- 消息: Agent → Session: 记录 tool -->
  <text x="392" y="648" text-anchor="middle" fill="#1e293b">记录 tool 消息</text>
  <line x1="200" y1="656" x2="583" y2="656" stroke="#475569" stroke-width="1.5" marker-end="url(#arr)"/>

  <!-- loop 回流提示 -->
  <text x="335" y="676" text-anchor="middle" fill="#64748b" font-style="italic">i++，回到循环顶部</text>

  <!-- ===== 最终: Agent → User (返回) ===== -->
  <text x="127" y="705" text-anchor="middle" fill="#1e293b">完成（消息历史 + 指标）</text>
  <line x1="190" y1="713" x2="67" y2="713" stroke="#94a3b8" stroke-width="1.5" stroke-dasharray="6 4" marker-end="url(#arrR)"/>

  <!-- 终止标记 -->
  <circle cx="65" cy="745" r="7" fill="none" stroke="#475569" stroke-width="1.5"/>
  <line x1="60" y1="740" x2="70" y2="750" stroke="#475569" stroke-width="1.5"/>
  <line x1="70" y1="740" x2="60" y2="750" stroke="#475569" stroke-width="1.5"/>
</svg>

上图以**序列图**的形式展示了一次完整 Agent 运行的时序交互。顶部 5 个参与者（User / Agent / LLM / Tool / Session）各自有一条垂直生命线，Agent 的紫色激活条贯穿运行全程，消息按时间从上往下依次发生。

灰色 `loop` 框是主循环（× N 轮迭代），每轮先调用 LLM 拿到响应，再顺序经过三个橙色 `opt` 条件分支判断是否终止：

- **token 超阈值** → 压缩上下文，若仍超则 `finalize(error)` 终止（红点）
- **finish_reason = length** → `finalize(部分回复)` 终止（红点）
- **无工具调用** → 输出最终回复，`finalize(completed)` 终止（绿点）

三个 `opt` 都不命中时，走"否则"分支：执行工具 → 回填结果 → 记录到 Session → `i++` 回到循环顶部。最终 Agent 把消息历史和指标返回给 User。实线箭头是调用请求，虚线箭头是返回响应。

---

## 第一步：配置文件

### 1.1 问题：硬编码散落各处

回顾 Day 6 的 `main.go`，配置项散落在三个地方：

```go
// 环境变量（API Key）
apiKey := os.Getenv("GULL_OPENAI_API_KEY")
baseURL := os.Getenv("GULL_OPENAI_BASE_URL")

// 硬编码在 agent.go
params := openai.ChatCompletionNewParams{
    Model: "deepseek-v4-flash",  // ← 换模型得改代码
    ...
}

// 硬编码的初始消息
ctx := agent.NewContext(
    agent.WithInitialMessages(
        openai.UserMessage("北京今天天气怎么样？"),  // ← 换问题得改代码
    ),
)
```

这种做法的问题：
- **API Key 不能进版本控制**，但其他配置（模型名、阈值、目录路径）又想纳入版本管理
- **不同环境用不同配置**（开发用 deepseek，测试用其他模型），硬编码无法切换
- **新增配置项要改代码**，比如想调 `max_iterations`，得改 `main.go`

### 1.2 设计：JSON 配置 + 环境变量覆盖

核心思路：**非敏感配置写 JSON 文件，敏感配置走环境变量覆盖**。

`config/config.json`：

```json
{
  "api_key": "",
  "base_url": "https://api.deepseek.com",
  "model": "deepseek-v4-flash",
  "max_iterations": 10,
  "context_threshold": 200000,
  "log_dir": "logs",
  "session_dir": "sessions",
  "skills_dir": "./skills",
  "mcp_config": "config/mcp.json"
}
```

`api_key` 留空，从环境变量读取；其他配置都有默认值，可以按需调整。

`config/config.go`：

```go
type Config struct {
    APIKey           string `json:"api_key"`
    BaseURL          string `json:"base_url"`
    Model            string `json:"model"`
    MaxIterations    int    `json:"max_iterations"`
    ContextThreshold int64  `json:"context_threshold"`
    LogDir           string `json:"log_dir"`
    SessionDir       string `json:"session_dir"`
    SkillsDir        string `json:"skills_dir"`
    MCPConfig        string `json:"mcp_config"`
}
```

加载逻辑的优先级：**环境变量 > 配置文件 > 默认值**。

```go
func Load(path string) (*Config, error) {
    // 1. 读取 JSON 文件
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
    }

    var cfg Config
    if err := json.Unmarshal(data, &cfg); err != nil {
        return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
    }

    // 2. 环境变量覆盖（优先级最高，避免密钥写进配置文件）
    if v := os.Getenv("GULL_OPENAI_API_KEY"); v != "" {
        cfg.APIKey = v
    }
    if v := os.Getenv("GULL_OPENAI_BASE_URL"); v != "" {
        cfg.BaseURL = v
    }

    // 3. 默认值填充
    if cfg.Model == "" {
        cfg.Model = "deepseek-v4-flash"
    }
    if cfg.MaxIterations == 0 {
        cfg.MaxIterations = 10
    }
    // ... 其他默认值

    // 4. 必填校验
    if cfg.APIKey == "" {
        return nil, fmt.Errorf("api_key 未配置（请在 config/config.json 中设置或设置环境变量 GULL_OPENAI_API_KEY）")
    }

    return &cfg, nil
}
```

**为什么环境变量优先级最高？**

1. 首先是安全性问题，一般不建议将密钥等以纯文本方式保存在配置文件中，最好是通过环境变量方法控制
2. 配置文件可以提交到 Git（`api_key` 留空），团队成员共享同一份默认配置。但每个人有自己的 API Key，不想改配置文件（会污染 Git 状态），也不想在配置文件里写明文密钥。环境变量正好解决这个矛盾——开发时 `export GULL_OPENAI_API_KEY=sk-xxx`，配置文件保持干净。

---

## 第二步：日志系统

### 2.1 问题：输出混乱

Day 6 的 `agent.go` 里，输出散落在四种地方：

```go
fmt.Printf("=== iteration %d ===\n", i)           // 终端：迭代标记
fmt.Printf("[usage] prompt=%d completion=%d\n")    // 终端：token 用量
fmt.Printf("[tool] %s -> %s\n", name, result)      // 终端：工具调用
fmt.Println(msg.Content)                            // 终端：最终回复
log.Printf("模型未发起工具调用，结束 agent loop")     // 标准日志：决策
a.logRequest(i, params)                             // 文件：原始请求 JSON
a.logResponse(i, resp)                              // 文件：原始响应 JSON
```

问题：
- **终端和文件混用 `fmt` 和 `log`**，格式不统一
- **原始 JSON 请求/响应** 和 **人类可读的决策日志** 混在一起，终端被刷屏
- **没有日志级别**，Debug 信息和关键决策同样醒目
- `logRequest`/`logResponse` 是 Agent 的方法，职责耦合——Agent 不该关心日志怎么写

### 2.2 设计：双通道 + slog

核心思路：**终端给人看关键决策，文件给机器存全量数据**。

| 通道 | 格式 | 内容 | 目标 |
|------|------|------|------|
| 终端 | 纯文本 | Step/Info/Error 级别 | 人看关键流程 |
| 文件 | JSON | 全量（含 Debug、原始请求/响应） | 机器检索、调试回溯 |

用 Go 1.21+ 的标准库 `log/slog` 实现文件端，终端端直接 `fmt.Fprintln` 保持干净：

```go
type Logger struct {
    slog *slog.Logger // 文件端：结构化 JSON
    file *os.File
}
```

#### 自定义日志级别

slog 的四个级别（Debug/Info/Warn/Error）不够用——Agent Loop 里的"迭代开始""工具调用""终止决策"这些信息，比 Info 更醒目，但不到 Warn 的告急程度。我们定义一个 `StepLevel`：

```go
const StepLevel = slog.Level(2) // Info=0, Warn=4
```

通过 `ReplaceAttr` 让文件里的 JSON 日志把 `StepLevel` 渲染为 `"STEP"` 而非数字：

```go
fileHandler := slog.NewJSONHandler(f, &slog.HandlerOptions{
    Level: LevelDebug, // 文件记录 Debug 及以上所有日志
    ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
        if a.Key == slog.LevelKey && a.Value.Any() != nil {
            if level, ok := a.Value.Any().(slog.Level); ok && level == StepLevel {
                a.Value = slog.StringValue("STEP")
            }
        }
        return a
    },
})
```

#### 五个对外方法

```go
// Step —— 关键步骤/决策，终端 + 文件
func (l *Logger) Step(format string, args ...any) {
    msg := fmt.Sprintf(format, args...)
    _, _ = fmt.Fprintln(os.Stdout, msg)       // 终端
    l.slog.Log(context.Background(), StepLevel, msg) // 文件
}

// Info —— 启动信息，终端 + 文件
// Error —— 错误，终端 stderr + 文件
// Debug —— 调试细节，仅文件（终端不显示，避免噪音）
// JSON —— 原始 JSON 载荷，仅文件
func (l *Logger) JSON(tag string, v any) {
    l.slog.With(tag, v).Debug("raw payload")
}
```

**为什么终端不用 slog.TextHandler？**

`TextHandler` 会输出 `time=2026-06-30T15:39:13 level=INFO msg=配置加载完成`，前缀噪音太多。终端给人看，`[启动] 配置加载完成: model=deepseek-v4-flash` 这种纯文本更友好。文件端用 JSON 是因为结构化日志可以用 `jq` 检索：`cat logs/agent.log | jq 'select(.level=="STEP")'`。

#### 替代 logRequest / logResponse

Agent 里原来的两个方法直接删掉，改为：

```go
a.logger.JSON("REQUEST", params)  // 原始请求体 → 仅文件
a.logger.JSON("RESPONSE", resp)   // 原始响应体 → 仅文件
a.logger.Step("[LLM] 响应: finish_reason=%s, total=%d", ...)  // 摘要 → 终端+文件
```

职责清晰：`JSON` 存原始数据供调试，`Step` 给人看关键信息。

---

## 第三步：会话管理

### 3.1 为什么需要会话

跑完一次 Agent，终端输出刷过就没了。如果想知道"上次那个任务，模型调了几次工具、每次返回什么、总共花了多少 token"，只能翻终端历史。

会话模块的作用：**把每次运行的完整消息历史和指标持久化为 JSON 文件**，便于：
- 回溯模型的决策过程（调了哪些工具、中间返回什么）
- 统计 token 消耗趋势（哪个任务花钱多）
- 后续扩展多轮对话时恢复上下文

### 3.2 数据结构

```go
type Session struct {
    ID          string    `json:"id"`            // 时间戳: 20260630-143052
    CreatedAt   time.Time `json:"created_at"`
    Model       string    `json:"model"`
    UserPrompt  string    `json:"user_prompt"`
    Status      string    `json:"status"`        // running / completed / error
    Iterations  int       `json:"iterations"`
    TotalTokens int64     `json:"total_tokens"`
    Duration    string    `json:"duration"`
    Messages    []Message `json:"messages"`
}
```

`Message` 是简化的可序列化结构，**不直接序列化 openai-go 的 union 类型**：

```go
type Message struct {
    Role       string     `json:"role"`                  // system/user/assistant/tool
    Content    string     `json:"content,omitempty"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
}
```

**为什么不直接 Marshal openai-go 的消息？**

`openai.ChatCompletionMessageParamUnion` 是结构体 union（`OfSystem`/`OfUser`/`OfAssistant`/`OfTool` 四个指针字段），直接 `json.Marshal` 会产生大量空字段（所有 union 选项都序列化），且字段名受 SDK 版本影响不稳定。用简单结构体规避这些问题，只保留持久化所需的必要信息。

### 3.3 核心方法

```go
// New 创建会话，ID 用时间戳保证唯一
func New(model, userPrompt string) *Session {
    now := time.Now()
    return &Session{
        ID:         now.Format("20060102-150405"),
        CreatedAt:  now,
        Model:      model,
        UserPrompt: userPrompt,
        Status:     "running",
        Messages:   []Message{},
    }
}

// AddMessage 把 openai-go 消息转换为可序列化格式并追加
func (s *Session) AddMessage(msg openai.ChatCompletionMessageParamUnion) {
    s.Messages = append(s.Messages, toSessionMessage(msg))
}

// Save 持久化到 sessions/<id>.json
func (s *Session) Save(dir string) error { ... }

// Load 从 JSON 文件加载历史会话
func Load(path string) (*Session, error) { ... }
```

转换函数 `toSessionMessage` 逐层判断 union 的哪个字段有值，提取文本和工具调用：

```go
func toSessionMessage(msg openai.ChatCompletionMessageParamUnion) Message {
    var m Message
    if !param.IsOmitted(msg.OfSystem) {
        m.Role = "system"
        m.Content = systemContentToString(msg.OfSystem.Content)
        return m
    }
    if !param.IsOmitted(msg.OfAssistant) {
        m.Role = "assistant"
        if !param.IsOmitted(msg.OfAssistant.Content.OfString) {
            m.Content = msg.OfAssistant.Content.OfString.Value
        }
        for _, call := range msg.OfAssistant.ToolCalls {
            m.ToolCalls = append(m.ToolCalls, ToolCall{
                ID:   call.ID,
                Name: call.Function.Name,
                Args: call.Function.Arguments,
            })
        }
        return m
    }
    // ... OfUser / OfTool
}
```

---

## 第四步：Agent 重构

### 4.1 用 WithUserInput 替代硬编码

Day 6 的 `main.go` 用 `WithInitialMessages` 注入硬编码消息：

```go
ctx := agent.NewContext(
    agent.WithInitialMessages(
        openai.UserMessage("北京今天天气怎么样？"),  // ← 硬编码
    ),
)
```

新增 `WithUserInput` 选项，让用户输入和上下文管理解耦：

```go
type Agent struct {
    // ...
    userInput string  // ← 新增
}

func WithUserInput(input string) Option {
    return func(a *Agent) { a.userInput = input }
}
```

`Run()` 时自动注入：

```go
func (a *Agent) Run() {
    // 注入 System Prompt
    if a.prompt != nil {
        sysMsg := openai.SystemMessage(a.prompt.Build())
        a.ctx.Append(sysMsg)
        a.session.AddMessage(sysMsg)
    }

    // 注入用户输入（作为一条 user 消息）
    if a.userInput != "" {
        userMsg := openai.UserMessage(a.userInput)
        a.ctx.Append(userMsg)
        a.session.AddMessage(userMsg)
    }
    // ... 进入主循环
}
```

**为什么不让 main.go 直接 `ctx.Append(UserMessage(...))`？**

那样 main.go 要同时操作 `ctx` 和 `session` 两个对象（消息要同时记到两边）。`WithUserInput` 让 Agent 负责把消息同步注入到 context 和 session，调用方只需传一个字符串，职责更清晰。

### 4.2 去掉 prompt 耦合的 logRequest / logResponse

原来的 `logRequest` / `logResponse` 是 Agent 的私有方法，用 `json.MarshalIndent` 把请求/响应体写到文件。现在统一用 `logger.JSON`：

```go
// 之前
a.logRequest(i, params)   // Agent 自己的方法
a.logResponse(i, resp)

// 现在
a.logger.JSON("REQUEST", params)   // Logger 的方法
a.logger.JSON("RESPONSE", resp)
```

Agent 不再关心日志怎么写，只管把数据丢给 Logger。

### 4.3 结构化决策日志

每一步关键决策都用 `logger.Step` 输出，带 `[决策]` 前缀：

```go
for i := 1; i <= a.maxIter; i++ {
    a.logger.Step("=== 第 %d 轮迭代 ===", i)
    // ... LLM 调用
    a.logger.Step("[LLM] 响应: finish_reason=%s, prompt=%d, completion=%d, total=%d", ...)
    a.logger.Step("[LLM] 当前上下文 token: %d | 累计消耗: %d / 阈值 %d", ...)

    // 压缩判断
    if a.ctx.ShouldCompact() {
        a.logger.Step("[决策] token %d 达到阈值 %d，触发上下文压缩", ...)
        // ...
        a.logger.Step("[决策] 压缩完成，当前 token: %d", a.ctx.Tokens())
    }

    // 终止判断
    if choice.FinishReason == "length" {
        a.logger.Step("[决策] 达到模型 token 上限，终止迭代（第 %d 轮）", i)
    }
    if len(msg.ToolCalls) == 0 {
        a.logger.Step("[决策] 模型未发起工具调用，输出最终回复（第 %d 轮）", i)
    }

    // 工具调用（带耗时统计）
    for _, call := range msg.ToolCalls {
        a.logger.Step("[TOOL] %s(%s)", call.Function.Name, truncate(call.Function.Arguments, 200))
        start := time.Now()
        result := a.dispatch(call)
        a.logger.Step("[TOOL] 耗时 %v | 结果(%d 字符): %s",
            time.Since(start), len([]rune(result)), truncate(result, 300))
    }
}
```

---

## 第五步：组装 main.go

所有零件就绪，`main.go` 变成纯粹的组装线：

```go
func main() {
    configPath := flag.String("config", "config/config.json", "配置文件路径")
    promptFlag := flag.String("prompt", "", "用户输入（与位置参数二选一）")
    flag.Parse()

    // 用户输入：-prompt 优先，否则取位置参数拼接
    userInput := *promptFlag
    if userInput == "" {
        userInput = strings.Join(flag.Args(), " ")
    }

    // 1. 加载配置
    cfg, err := config.Load(*configPath)

    // 2. 初始化日志器
    lg, _ := logger.New(cfg.LogDir)
    defer lg.Close()

    // 3. 创建 LLM 客户端
    client := openai.NewClient(
        option.WithAPIKey(cfg.APIKey),
        option.WithBaseURL(cfg.BaseURL),
    )

    // 4. 注册工具
    registry := tool.NewRegistry()
    registry.Register(tool.NewBashTool())
    // ... 其他内置工具

    // 5. MCP 工具（降级）
    defer mcp.LoadAll(registry, cfg.MCPConfig)()

    // 6. Skill
    skills := skill.NewLoader(cfg.SkillsDir).Load()
    registry.Register(tool.NewUseSkillTool(skills))

    // 7. System Prompt
    pb := prompt.NewBuilder().
        WithIdentity(prompt.DefaultIdentity).
        WithSkills(skills).
        WithRule(prompt.RuleSelfDebug).
        WithWorkingContext()

    // 8. 上下文管理器
    ctx := agent.NewContext(
        agent.WithSummarizer(agent.NewLLMSummarizer(client, cfg.Model)),
        agent.WithThreshold(cfg.ContextThreshold),
    )

    // 9. 会话
    sess := session.New(cfg.Model, userInput)

    // 10. 创建 Agent 并启动
    ag := agent.New(client,
        agent.WithModel(cfg.Model),
        agent.WithRegistry(registry),
        agent.WithPrompt(pb),
        agent.WithContext(ctx),
        agent.WithUserInput(userInput),
        agent.WithMaxIterations(cfg.MaxIterations),
        agent.WithLogger(lg),
        agent.WithSession(sess),
    )
    ag.Run()

    // 11. 保存会话
    sess.Save(cfg.SessionDir)
}
```

**11 步的执行顺序很关键**：
- 日志器要先于其他模块初始化（后面的步骤都要用 `lg.Info` 输出启动信息）
- MCP 和 Skill 的加载在工具注册表创建之后（要往 registry 里注册）
- System Prompt 的构建在 Skill 加载之后（要注入 Skill 元数据）
- 会话在 Agent 启动前创建（Run 过程中要往 session 里追加消息）

---

## 运行效果

### 启动

```bash
# 设置环境变量（敏感信息不走配置文件）
export GULL_OPENAI_API_KEY="sk-xxx"
export GULL_OPENAI_BASE_URL="https://api.deepseek.com"

# 运行
./gull-herness-agent -prompt "帮我看看 main.go 有没有明显问题"
```

### 终端输出

```
[启动] 配置加载完成: model=deepseek-v4-flash threshold=200000 max_iter=10
2026/06/30 16:06:34 已注册 14 个 MCP 工具
2026/06/30 16:06:34 已注册 15 个 MCP 工具
[启动] 已加载 1 个 Skill，已注册 34 个工具
[启动] 会话已创建: 20260630-160634

模型: deepseek-v4-flash | token 阈值: 200000 | 最大迭代: 10
用户输入: 你好

=== 第 1 轮迭代 ===
[LLM] 发起请求 (messages=2, tools=34)
[LLM] 响应: finish_reason=stop, prompt=4850, completion=149, total=4999
[LLM] 当前上下文 token: 4999 | 累计消耗: 4999 / 阈值 200000
[决策] 模型未发起工具调用，输出最终回复（第 1 轮）

=== 最终回复 ===
你好！👋 我是你的全能编程助手，很高兴为你服务！

[完成] 会话已保存: sessions/20260630-160634.json (1 轮, 4999 tokens)
```

### 文件日志（logs/agent.log）

结构化 JSON，每行一条，可用 `jq` 检索：

```bash
# 查看所有 STEP 级别日志
cat logs/agent.log | jq 'select(.level=="STEP")'

# 查看原始请求体
cat logs/agent.log | jq 'select(.msg=="raw payload") | .REQUEST'

# 统计每轮的 token 消耗
cat logs/agent.log | jq 'select(.level=="STEP" and (.msg|startswith("[LLM] 响应")))'
```

```json
{"time":"2026-06-30T16:06:34.502+08:00","level":"STEP","msg":"=== 第 1 轮迭代 ==="}
{"time":"2026-06-30T16:06:34.502+08:00","level":"DEBUG","msg":"raw payload","REQUEST":{"messages":[...],"model":"deepseek-v4-flash","tools":[...]}}
{"time":"2026-06-30T16:06:34.502+08:00","level":"STEP","msg":"[LLM] 发起请求 (messages=2, tools=34)"}
{"time":"2026-06-30T16:06:37.169+08:00","level":"DEBUG","msg":"raw payload","RESPONSE":{"choices":[...],"usage":{"prompt_tokens":4850,...}}}
{"time":"2026-06-30T16:06:37.169+08:00","level":"STEP","msg":"[LLM] 响应: finish_reason=stop, prompt=4850, completion=149, total=4999"}
{"time":"2026-06-30T16:06:37.169+08:00","level":"STEP","msg":"[决策] 模型未发起工具调用，输出最终回复（第 1 轮）"}
```

### 会话文件（sessions/20260630-160634.json）

```json
{
  "id": "20260630-160634",
  "created_at": "2026-06-30T16:06:34.502758+08:00",
  "model": "deepseek-v4-flash",
  "user_prompt": "你好",
  "status": "completed",
  "iterations": 1,
  "total_tokens": 4999,
  "duration": "2.667019834s",
  "messages": [
    {"role": "system", "content": "# 身份\n你是一个全能的编程助手..."},
    {"role": "user", "content": "你好"},
    {"role": "assistant", "content": "你好！👋 我是你的全能编程助手..."}
  ]
}
```

---

## 目录结构

完成 Day 7 后的项目结构：

```
gull-herness-agent/
├── config/
│   ├── config.go          # 配置加载逻辑
│   ├── config.json        # 默认配置（非敏感）
│   └── mcp.json           # MCP server 配置
├── agent/
│   ├── agent.go           # Agent Loop（用 logger + session 替代 fmt/log）
│   ├── context.go         # 上下文管理（Day 6）
│   ├── estimator.go       # Token 估算（Day 6）
│   └── summarizer.go      # LLM 摘要（Day 6）
├── logger/
│   └── logger.go          # 通用日志器（slog 双通道）
├── session/
│   └── session.go         # 会话持久化
├── tool/                  # 内置工具（Day 2-4）
├── skill/                 # Skill 加载器（Day 5）
├── mcp/                   # MCP 客户端（Day 5）
├── prompt/                # System Prompt 构建（Day 5）
├── skills/                # Skill 目录
├── logs/                  # 全量日志（自动创建）
├── sessions/              # 会话文件（自动创建）
├── main.go                # 组装入口
└── go.mod
```

---

## 端到端演示

让 Agent 完成一个真实的代码任务——读取文件并分析：

```bash
./gull-herness-agent -prompt "读取 agent.go 文件，告诉我它有多少行代码，主要职责是什么"
```

终端输出：

```
=== 第 1 轮迭代 ===
[LLM] 发起请求 (messages=2, tools=34)
[LLM] 响应: finish_reason=tool_calls, prompt=4850, completion=89, total=4939
[决策] 模型选择调用 1 个工具
[TOOL] file_read({"path":"agent.go"})
[TOOL] 耗时 1.2ms | 结果(540 字符): package agent\n\nimport (...

=== 第 2 轮迭代 ===
[LLM] 发起请求 (messages=4, tools=34)
[LLM] 响应: finish_reason=stop, prompt=5800, completion=220, total=6020
[决策] 模型未发起工具调用，输出最终回复（第 2 轮）

=== 最终回复 ===
agent.go 共 279 行代码。它的主要职责是：
1. 封装 Agent Loop 的完整执行流程...
2. 通过函数式选项注入依赖...
3. 管理迭代、工具调用、上下文压缩...

[完成] 会话已保存: sessions/20260630-162100.json (2 轮, 10959 tokens)
```

实际运行效果如下图所示：

![终端运行效果](/images/run_in_terminal.png)

会话文件里完整记录了两轮迭代的消息历史，包括工具调用的参数和返回结果。

---

## 总结

Day 7 把散落的零件组装成了一台完整的机器：

| 模块 | 解决的问题 | 核心设计 |
|------|-----------|---------|
| `config` | 硬编码散落各处 | JSON 配置 + 环境变量覆盖 |
| `logger` | 输出混乱、无法检索 | slog 双通道：终端纯文本 + 文件 JSON |
| `session` | 跑完即丢、无法回溯 | 消息历史 + 指标持久化为 JSON |
| `agent` 重构 | 硬编码输入、职责耦合 | `WithUserInput` + logger + session 注入 |

**关键设计决策**：

1. **不做 REPL**——一次性任务模式更实用，复杂度更低
2. **环境变量优先级最高**——API Key 不进配置文件，非敏感配置纳入版本管理
3. **终端不用 slog.TextHandler**——避免 `time= level= msg=` 前缀噪音
4. **不直接序列化 openai-go 的 union 类型**——用简单结构体规避字段不稳定问题
5. **`WithUserInput` 而非 `RunWithPrompt`**——用户输入是配置项，不是独立方法

至此，一个完整的、可配置的、可观测的 Agent 框架就成型了。后续扩展方向：
- REPL 多轮对话（基于现有 session 恢复上下文）
- 流式输出（`stream: true`，逐 token 返回）
- 工具调用并发执行（多个 `tool_calls` 并行 dispatch）
- 成本统计（按模型定价计算每次运行的费用）
