# Day 3：System Prompt 工程

## 导语

Day 2 我们用接口+注册表让工具可插拔了，但 System Prompt 还是一条硬编码字符串——想加一条"先读文件再改"的规则，得小心翼翼地在长字符串里找插入点。今天把它升级为**结构化动态生成**：用 Builder 模式，几条链式调用就能组装出一个清晰、可维护的 System Prompt。

更重要的是，你会发现 prompt 的结构化不只是"代码好看"——一条规则的增减，会直接改变 Agent 的决策路径。

## 本日目标

将硬编码的 System Prompt 升级为基于 Builder 模式的结构化组装，让 prompt 可分区段管理、动态注入运行时信息。

## 你将学到

- System Prompt 的三个核心区段：身份、行为准则、环境信息
- Builder 链式调用模式在 prompt 工程中的应用
- 为什么工具清单不应该放在 System Prompt 里把 Builder、Registry、runAgentLoop 三者封装成一个可复用的 Agent 结构体。开始从"写脚本"升级到"写框架"。


- 用 `runtime.GOOS` / `os.Getwd` / `time.Now` 动态注入运行时上下文
- 添加/删除一条规则如何改变 Agent 的决策行为

---

## 第一步：硬编码的痛点

先看看我们 Day 2 的 System Prompt（就一行）：

```go
openai.SystemMessage("你是一个全能的编程助手，可以执行 bash 命令、读写文件，还可以查询天气。遇到问题尽量自己动手排查，可以用命令验证猜想，也可以读写文件来解决问题。")
```

这行代码有**三个痛点**：

**痛点 1：加规则很痛苦**

假如你想让 Agent "修改文件前先用 file_read 确认内容"，得在长字符串里找一个合适的插入点，然后小心翼翼地把中文接上去——打错一个字整个行为就变了。

**痛点 2：无法动态注入运行时信息**

模型不知道自己在哪个目录、是什么操作系统、当前几点。如果你想让它输出"当前项目路径是 /xxx"，硬编码做不到——必须是运行时填充。

**痛点 3：工具描述和 prompt 混在一起**

Day 2 的 prompt 里写了"可以执行 bash 命令、读写文件，还可以查询天气"。但你已经在 `registry` 里注册了这些工具的完整描述（name + description + parameters），prompt 里再说一遍纯属重复。更重要的是——哪天加了个新工具，你不仅要 `registry.Register(...)`，还要记得回来改 prompt，漏掉一步就出 bug。

这三个痛点指向同一个结论：**prompt 需要结构化**。

---

## 第二步：三个区段

用我们熟悉的 getWeather 场景来思考：

> 用户问"北京天气怎样"时，模型拿到的东西包括：
> - `tools` 参数 → 告诉它"有哪些工具可用、怎么调用"
> - `messages[0]`（System Prompt）→ 告诉它"你是谁、该怎么干活"
> - `messages[1]`（User Message）→ 用户的问题

那么 System Prompt 应该包含哪些信息？拆成三个区段：

```
┌─────────────────────────────┐
│  # 身份                      │
│  你是一个全能的编程助手       │  ← 语气和角色
├─────────────────────────────┤
│  # 行为准则                   │
│  - 遇到问题先自己排查         │  ← 决策边界
│  - 改文件前先读文件           │
│  - 失败后解释原因             │
├─────────────────────────────┤
│  # 环境信息                   │
│  - 工作目录: /path/to/project │  ← 运行时注入
│  - 操作系统: darwin/arm64     │
│  - 当前时间: 2025-06-26 15:30 │
└─────────────────────────────┘
```

那工具清单放哪？**不放进 prompt**。Function Calling 的 `tools` 参数本身就是模型的"工具说明书"——`name`、`description`、`parameters` 都在里面。prompt 里再写一遍等于：浪费 token + 可能不一致 + 新增工具要改两处。

:::tip 核心原则
**`tools` 参数管"能做什么"，System Prompt 管"怎么思考"。** 工具信息走 API 协议层，行为约束走 prompt 层，职责分离。
:::

---

## 第三步：实现 Builder 模式

有了设计，我们来用代码实现。核心是一个 `Builder` 结构体，提供链式 API。

### 3.1 Builder 结构体

```go
type Builder struct {
    identity string            // # 身份
    rules    []string          // # 行为准则（多条）
    context  map[string]string // # 环境信息（k-v）
}

func NewBuilder() *Builder {
    return &Builder{
        context: make(map[string]string),
    }
}
```

每个方法返回 `*Builder` 本身，实现链式调用。

### 3.2 WithIdentity：语气和角色

```go
func (b *Builder) WithIdentity(identity string) *Builder {
    b.identity = identity
    return b
}
```

这不仅是设定"角色名"，更是设定**语气和意图**。我们仍然用 getWeather 来感受差异：

同一个 prompt，只改身份描述：

| 身份 | 模型回答 |
|------|---------|
| **"你是一个助手"** | 北京今天晴朗，气温 22℃，湿度 55% |
| **"你是气象专家"** | 北京今日高空受高压脊控制，晴间多云，地面温度 22℃，相对湿度 55%，体感舒适，紫外线强度中等，适宜户外活动 |

同样的 `getWeather` 工具结果，不同身份描述下，模型的语气、细节程度、甚至回答的"自信度"都不一样。

### 3.3 WithRule：决策边界

```go
func (b *Builder) WithRule(rule string) *Builder {
    b.rules = append(b.rules, rule)
    return b
}
```

每条规则是一个字符串，可以多次调用。三条规则各管一个维度：

| 常量 | 值 | 维度的含义 |
|------|-----|---------|
| `RuleSelfDebug` | "遇到问题尽量自己动手排查…" | **自驱力**：不要一遇问题就求助用户 |
| `RuleReadBeforeWrite` | "修改文件前先用 file_read 确认内容" | **谨慎性**：先看再改，避免覆盖 |
| `RuleFailGracefully` | "如果多次尝试仍然失败…" | **容错**：不要无限重试，及时止损 |

:::tip
规则不在多，在于精。三到五条覆盖"自驱、谨慎、容错"就够了。规则太多反而稀释重点，模型可能一条都执行不好。
:::

### 3.4 WithWorkingContext

```go
func (b *Builder) WithWorkingContext() *Builder {
    workDir, _ := os.Getwd()
    b.context["工作目录"] = workDir
    b.context["操作系统"] = runtime.GOOS + "/" + runtime.GOARCH
    b.context["当前时间"] = time.Now().Format("2006-01-02 15:04:05")
    return b
}
```

这三个字段不是编译期写死的，而是**运行时动态获取**的。每次运行 Agent 都会根据当前环境重新生成。

为什么模型需要知道这些？举个例子：用户说"在当前目录下创建一个文件"，如果 prompt 里没有工作目录信息，模型就需要额外调用一次 `pwd` 命令来确认路径——白白多一轮迭代。把工作目录放进 prompt，模型一开始就知道"自己在哪"。

### 3.5 Build：组装生成

```go
func (b *Builder) Build() string {
    var sb strings.Builder

    // 身份
    if b.identity != "" {
        sb.WriteString("# 身份\n")
        sb.WriteString(b.identity)
        sb.WriteString("\n\n")
    }

    // 行为准则
    if len(b.rules) > 0 {
        sb.WriteString("# 行为准则\n")
        for _, r := range b.rules {
            sb.WriteString("- " + r + "\n")
        }
        sb.WriteString("\n")
    }

    // 环境信息
    if len(b.context) > 0 {
        sb.WriteString("# 环境信息\n")
        for key, value := range b.context {
            sb.WriteString(fmt.Sprintf("- %s: %s\n", key, value))
        }
    }

    return strings.TrimSpace(sb.String())
}
```

注意每个 section 都用 `if` 判断是否为空——没设身份就不输出 `# 身份` 段。这让 Builder 更灵活：你可以先用 `WithIdentity` 跑起来，后面再加 `WithRule`，不会出空标题。

用 `strings.Builder` 而不是 `+=` 拼接，是因为 Go 中大量字符串拼接时 Builder 更高效——虽然 prompt 很短，但好习惯从小处养成。

---

## 第四步：提炼 system.go

Builder 给了你随意组合的自由，但每次都手写字符串太啰嗦。我们把默认值提取到 `prompt/system.go`：

```go
package prompt

const DefaultIdentity = "你是一个全能的编程助手，可以执行 bash 命令、读写文件。"

const (
    RuleSelfDebug       = "遇到问题尽量自己动手排查，可以用命令验证猜想，也可以读写文件来解决问题"
    RuleReadBeforeWrite = "修改文件前先用 file_read 确认当前内容"
    RuleFailGracefully  = "如果多次尝试仍然失败，向用户说明原因而不是反复重试"
)
```

这带来的好处：

- **规则"名称化"**：`RuleReadBeforeWrite` 比记住 "修改文件前先用 file_read 确认当前内容" 这串中文容易得多
- **集中管理**：所有默认值在一个文件里，改一处全局生效
- **可覆盖**：`main.go` 可以用常量，也可以传自定义字符串

---

### 4.1 接入 main.go

**Day 2（硬编码）**：

```go
messages := []openai.ChatCompletionMessageParamUnion{
    openai.SystemMessage("你是一个全能的编程助手，可以执行 bash 命令、读写文件，还可以查询天气。遇到问题尽量自己动手排查，可以用命令验证猜想，也可以读写文件来解决问题。"),
    openai.UserMessage("北京今天天气怎么样？"),
}
```

**Day 3（Builder）**：

```go
// 用结构化 Builder 组装 System Prompt
pb := prompt.NewBuilder().
    WithIdentity(prompt.DefaultIdentity).
    WithRule(prompt.RuleSelfDebug).
    WithRule(prompt.RuleReadBeforeWrite).
    WithRule(prompt.RuleFailGracefully).
    WithWorkingContext()

messages := []openai.ChatCompletionMessageParamUnion{
    openai.SystemMessage(pb.Build()),
    openai.UserMessage("北京今天天气怎么样？"),
}
```

效果对比：

| | Day 2 | Day 3 |
|------|------|------|
| 身份 | 混在一句话里 | `WithIdentity`，独立设置 |
| 行为准则 | 和身份描述粘连 | 每条独立 `WithRule`，增删不改其他 |
| 环境信息 | 无 | `WithWorkingContext` 自动注入 |
| 工具清单 | 手动写在 prompt 里 | **不在 prompt 里**，走 `tools` 参数 |
| 增加一条规则 | 在长字符串里找位置 | 加一行 `WithRule(...)` |

---

## 第五步：规则如何改变行为

这是本章最核心的教学点。我们用一段代码做对比实验。

### 场景

用户让 Agent "把 /tmp/readme.txt 里的版本号从 1.0 改成 2.0"。假设文件里已经有一些内容。

### 没有 RuleReadBeforeWrite 的 Agent

prompt 里没有"先读再改"的规则时，模型的典型行为：

```
=== iteration 1 ===
模型发起工具调用：file_write(path="/tmp/readme.txt", content="v2.0")
[tool] file_write -> 已成功写入文件 /tmp/readme.txt（4 字符）
=== iteration 2 ===
模型未发起工具调用，结束 agent loop
已经把版本号改成 2.0 了！
```

❌ 问题：原有内容全部丢失，只剩 "v2.0" 四个字符。

### 加上 RuleReadBeforeWrite 的 Agent

prompt 里加了 `WithRule(prompt.RuleReadBeforeWrite)` 后：

```
=== iteration 1 ===
模型发起工具调用：file_read(path="/tmp/readme.txt")
[tool] file_read -> 文件: /tmp/readme.txt（共 5 行，显示第 1-5 行）
-----------------------------------------
 1|# My Project
 2|
 3|Version: 1.0
 4|
 5|Copyright 2025
=== iteration 2 ===
模型发起工具调用：file_write(path="/tmp/readme.txt", content="# My Project\n\nVersion: 2.0\n\nCopyright 2025")
[tool] file_write -> 已成功写入文件 /tmp/readme.txt（48 字符）
=== iteration 3 ===
模型未发起工具调用，结束 agent loop
已把版本号从 1.0 改为 2.0，其他内容保持不变！
```

✅ 正确：先读取完整内容，只改版本号，其余保留。

**这就是 prompt 工程的力量——一行 `WithRule(RuleReadBeforeWrite)` 改变了模型的决策路径。** 不是改代码，而是改"说明书"。

:::warning Prompt 的边界：大模型是概率性的
上面这个对比展示了 prompt 规则的有效性——但它不是 100% 可靠的。大语言模型本质上是概率系统，即使在 System Prompt 里写了"先读后写"，模型仍然有**一定概率**忽略它——可能在上下文太长时被稀释、在某个特定输入下"忘记"、或者被其他更强势的指令覆盖。

对于"先读后写"这类**强约束**——后果严重（数据丢失）、要求确定性保障（100% 不能违反）——正确做法不是靠 prompt，而是靠**工程实现**：

```go{2,8}
// FileWriteTool 中加一个简单的工程约束
var readFiles = map[string]bool{} // 记录哪些文件已经被 file_read 过

func (t *FileWriteTool) Execute(args map[string]any) (string, error) {
    path := args["path"].(string)

    // 工程约束：写入前必须已经读过
    if !readFiles[path] {
        return "", fmt.Errorf("拒绝写入 %s：请先用 file_read 读取该文件", path)
    }

    return os.WriteFile(path, []byte(content), 0o644)
}
```

ReadTool 每次读完把路径记入 `readFiles`，WriteTool 写入前查一下——不在就拒绝。几行代码就做到了 prompt 做不到的**100% 确定性约束**。

**原则**：
- **软约束**（语气、风格、建议性行为）→ 用 prompt 规则
- **硬约束**（数据安全、确定性操作、不可逆行为）→ 用工程实现
- **最佳实践**：两者兼用——prompt 给模型提供"最佳路径"（减少不必要的重试），工程做兜底（确保安全底线）

本教程以 prompt 工程为核心，Day 2 的工具实现留了"全开放"模式的伏笔——后续章节我们会把这些工程约束逐渐加进去。
:::

---

## 完整可运行代码

当前项目结构：

```
.
├── main.go
├── tool/
│   ├── tool.go        # Tool 接口 + Registry
│   ├── weather.go     # WeatherTool（getWeather）
│   ├── bash.go        # BashTool
│   ├── file_read.go   # FileReadTool
│   └── file_write.go  # FileWriteTool
└── prompt/
    ├── prompt.go      # Builder
    └── system.go      # 默认常量
```

::: details prompt/prompt.go（完整代码）

```go
package prompt

import (
    "fmt"
    "os"
    "runtime"
    "strings"
    "time"
)

type Builder struct {
    identity string
    rules    []string
    context  map[string]string
}

func NewBuilder() *Builder {
    return &Builder{context: make(map[string]string)}
}

func (b *Builder) WithIdentity(identity string) *Builder {
    b.identity = identity
    return b
}

func (b *Builder) WithRule(rule string) *Builder {
    b.rules = append(b.rules, rule)
    return b
}

func (b *Builder) WithWorkingContext() *Builder {
    workDir, _ := os.Getwd()
    b.context["工作目录"] = workDir
    b.context["操作系统"] = runtime.GOOS + "/" + runtime.GOARCH
    b.context["当前时间"] = time.Now().Format("2006-01-02 15:04:05")
    return b
}

func (b *Builder) WithContext(key, value string) *Builder {
    b.context[key] = value
    return b
}

func (b *Builder) Build() string {
    var sb strings.Builder

    if b.identity != "" {
        sb.WriteString("# 身份\n")
        sb.WriteString(b.identity)
        sb.WriteString("\n\n")
    }

    if len(b.rules) > 0 {
        sb.WriteString("# 行为准则\n")
        for _, r := range b.rules {
            sb.WriteString("- " + r + "\n")
        }
        sb.WriteString("\n")
    }

    if len(b.context) > 0 {
        sb.WriteString("# 环境信息\n")
        for key, value := range b.context {
            sb.WriteString(fmt.Sprintf("- %s: %s\n", key, value))
        }
    }

    return strings.TrimSpace(sb.String())
}
```

:::

::: details prompt/system.go（完整代码）

```go
package prompt

const DefaultIdentity = "你是一个全能的编程助手，可以执行 bash 命令、读写文件。"

const (
    RuleSelfDebug       = "遇到问题尽量自己动手排查，可以用命令验证猜想，也可以读写文件来解决问题"
    RuleReadBeforeWrite = "修改文件前先用 file_read 确认当前内容"
    RuleFailGracefully  = "如果多次尝试仍然失败，向用户说明原因而不是反复重试"
)
```

:::

::: details main.go 中 prompt 相关部分

```go
import (
    // ...
    "gull-herness-agent/prompt"
    "gull-herness-agent/tool"
    // ...
)

func main() {
    // ... 注册工具 ...

    // 用结构化 Builder 组装 System Prompt
    pb := prompt.NewBuilder().
        WithIdentity(prompt.DefaultIdentity).
        WithRule(prompt.RuleSelfDebug).
        WithRule(prompt.RuleReadBeforeWrite).
        WithRule(prompt.RuleFailGracefully).
        WithWorkingContext()

    messages := []openai.ChatCompletionMessageParamUnion{
        openai.SystemMessage(pb.Build()),
        openai.UserMessage("北京今天天气怎么样？"),
    }

    runAgentLoop(client, registry, messages)
}
```

:::

---

## 关键设计决策

### 为什么工具清单不放进 System Prompt？

Function Calling 的 `tools` 参数已经包含所有工具的 `name`、`description`、`parameters`——这本身就是一份完整的"工具说明书"。prompt 里再写一遍会有三个问题：

1. **浪费 token**：一份信息传两次
2. **可能不一致**：你改了 `WeatherTool.Description()`，但忘了改 prompt 里的描述 → 模型看到两份矛盾的信息
3. **维护负担**：每加一个工具就要改 prompt，违反了 Day 2 的"加工具只需一行注册"原则

### 为什么用 Builder 而不是 fmt.Sprintf？

`fmt.Sprintf` 适合"填空"，但 prompt 组装需要的是"控制 section 的有无"——没有规则时整个 `# 行为准则` 段不该出现，没有身份时 `# 身份` 段不该出现。Builder 的 `if` 判断做到了这一点，`fmt.Sprintf` 做不到。

### 为什么常量放在 system.go 而不是 main.go？

这和 Day 2 把 WeatherTool 从 main.go 移到 tool/weather.go 的理由一样——**职责分离**。prompt 的内容归 prompt 包管，main.go 只负责"组装+调用"。而且 `system.go` 里的常量可以被其他 Go 文件引用。

### prompt 变化对 token 消耗的影响？

很多新手担心结构化 prompt 会费 token。实际上：`WithWorkingContext()` 每次动态生成（时间会变），但增量只有 ~80 token。三条行为准则约 120 token。结构化并没有比 Day 2 的长字符串多耗 token——反而因为**去掉了工具清单的重复描述**，可能更省。

---

## 一句话总结

今天的核心收获：**"prompt 不要写成散文，要写成结构化文档"**。Builder 让你像搭积木一样组装 prompt——每条规则独立一行，新增、删除、调整都不会影响其他部分。更重要的是，你亲眼看到了"加一行规则改一个行为"——这是 prompt 工程中最直观也最重要的认知。

## 下一步

Day 4：引入 Skill 机制——通过目录扫描 + SKILL.md 文件，给 Agent 动态加载专业领域知识（如"代码审查"skill），让同一个 Agent 在不同场景下表现出不同的专业能力。
