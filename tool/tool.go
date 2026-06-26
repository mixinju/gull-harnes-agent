package tool

import (
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
)

// Tool 是所有工具的抽象接口。
// 每个工具实现此接口后即可注册到 Registry 中，
// 由 Agent Loop 统一进行 Function Calling 和工具分发。
type Tool interface {
	// Name 返回工具名称，模型通过此名称在 tool_calls 中发起调用。
	Name() string

	// Description 返回工具描述，帮助模型理解决策"何时该调用这个工具"。
	// 写的越清晰，模型越不容易误用。
	Description() string

	// Schema 返回工具的 Function Calling 参数定义（JSON Schema 格式）。
	// 直接生成 openai.FunctionDefinitionParam，用于构造 ChatCompletion 请求中的 tools 数组。
	Schema() openai.FunctionDefinitionParam

	// Execute 执行工具，接收模型生成的 JSON 参数，返回执行结果。
	// 第一个返回值为工具执行的结果字符串（会作为 ToolMessage 回传给模型），
	// 第二个返回值为 error，表示工具执行失败（如参数非法、执行超时等）。
	Execute(args map[string]any) (string, error)
}

// Registry 是工具注册表，管理所有已注册的工具。
// 提供注册、查找、转换为 OpenAI SDK 格式、按名称分发执行等能力。
// 线程安全由调用方保证（通常在启动阶段一次性注册，运行时只读）。
type Registry struct {
	tools map[string]Tool
}

// NewRegistry 创建一个空的工具注册表。
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register 向注册表中注册一个工具。
// 如果工具名已存在则 panic（重复注册属于编程错误，应在启动阶段暴露）。
func (r *Registry) Register(t Tool) {
	name := t.Name()
	if name == "" {
		panic("tool name must not be empty")
	}
	if _, exists := r.tools[name]; exists {
		panic(fmt.Sprintf("tool %q already registered", name))
	}
	r.tools[name] = t
}

// Get 根据名称获取工具。第二个返回值表示是否找到。
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Names 返回所有已注册的工具名称。
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Size 返回已注册的工具数量。
func (r *Registry) Size() int {
	return len(r.tools)
}

// ToChatCompletionTools 将所有已注册的工具转换为 OpenAI SDK 的 ChatCompletionToolParam 格式，
// 可直接填入 ChatCompletionNewParams.Tools 字段。
func (r *Registry) ToChatCompletionTools() []openai.ChatCompletionToolParam {
	tools := make([]openai.ChatCompletionToolParam, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, openai.ChatCompletionToolParam{
			Function: t.Schema(),
		})
	}
	return tools
}

// Dispatch 根据工具名称和 JSON 参数字符串分发执行。
// name 对应 tool_calls[].function.name，argsJSON 对应 tool_calls[].function.arguments（JSON 字符串）。
// 工具不存在或参数解析失败时返回 error。
func (r *Registry) Dispatch(name string, argsJSON string) (string, error) {
	t, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("未知工具: %s，可用工具: %v", name, r.Names())
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("解析工具 %q 的参数失败: %v", name, err)
	}

	return t.Execute(args)
}
