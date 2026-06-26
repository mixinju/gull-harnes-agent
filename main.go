package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"gull-herness-agent/prompt"
	"gull-herness-agent/skill"
	"gull-herness-agent/tool"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// maxIterations 是 agent loop 的最大迭代次数上限。
const maxIterations = 10

// tokenThreshold 是上下文 token 数阈值，超过则终止 agent loop。
const tokenThreshold = 200_000

// apiLogger 用于将 API 请求/响应写入日志文件。
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

	// 初始化日志文件
	logFile, err := os.OpenFile("api_call.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("无法创建日志文件: %v", err)
	}
	defer func(logFile *os.File) {
		err := logFile.Close()
		if err != nil {

		}
	}(logFile)
	apiLogger = log.New(logFile, "", log.LstdFlags)

	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	)

	// 初始化工具注册表，注册所有可用工具
	registry := tool.NewRegistry()
	registry.Register(tool.NewWeatherTool())
	registry.Register(tool.NewBashTool())
	registry.Register(tool.NewFileReadTool())
	registry.Register(tool.NewFileWriteTool())

	// 从 skills/ 目录加载 Skill
	loader := skill.NewLoader("./skills")
	skills, err := loader.Load()
	if err != nil {
		log.Fatalf("加载 Skill 失败: %v", err)
	}
	fmt.Printf("已加载 %d 个 Skill\n", len(skills))

	// 注册 use_skill 工具，让模型可以按需加载 Skill 的完整文档
	if len(skills) > 0 {
		registry.Register(tool.NewUseSkillTool(skills))
		fmt.Printf("已注册 %d 个工具: %v\n", registry.Size(), registry.Names())
	}

	// 用结构化 Builder 组装 System Prompt
	pb := prompt.NewBuilder().
		WithIdentity(prompt.DefaultIdentity).
		WithSkills(skills).
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

// runAgentLoop 是 agent 主循环：反复"调用模型 -> 执行工具 -> 回填结果"，
// 直到满足以下任一终止条件：
//  1. 达到最大迭代次数 maxIterations；
//  2. 模型本轮没有发起工具调用（已给出最终回复）；
//  3. API 异常（err != nil 或 finish_reason == "length"）；
//  4. 累计 token 用量达到 tokenThreshold（从 resp.Usage.TotalTokens 读取）。
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
			Tools:    registry.ToChatCompletionTools(),
		}

		// 记录请求体
		logRequest(i, params)

		resp, err := client.Chat.Completions.New(context.Background(), params)
		if err != nil {
			// 处理API的异常
			handleError(err)
			return
		}

		// 记录响应体
		logResponse(i, resp)

		// 从返回的 usage 中计算 token 用量，超过阈值则提前终止
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

		// finish_reason == "length"：达到 token 上限
		if choice.FinishReason == "length" {
			log.Printf("达到 token 上限，终止迭代（iteration %d）", i)
			if msg.Content != "" {
				fmt.Println(msg.Content)
			}
			return
		}

		// 没有工具调用：模型已给出最终回复
		if len(msg.ToolCalls) == 0 {
			fmt.Println(msg.Content)
			log.Printf("模型未发起工具调用，结束 agent loop（iteration %d）", i)
			return
		}

		// 执行模型请求的所有工具调用，并把结果作为 tool 消息回填
		for _, call := range msg.ToolCalls {
			result := dispatchTool(registry, call)
			fmt.Printf("[tool] %s -> %s\n", call.Function.Name, result)
			messages = append(messages, openai.ToolMessage(result, call.ID))
		}
	}

	log.Printf("达到最大迭代次数 %d，终止 agent loop", maxIterations)
}

// dispatchTool 通过注册表分发工具调用。
func dispatchTool(registry *tool.Registry, call openai.ChatCompletionMessageToolCall) string {
	result, err := registry.Dispatch(call.Function.Name, call.Function.Arguments)
	if err != nil {
		return fmt.Sprintf("工具执行失败: %v", err)
	}
	return result
}

// logRequest 将请求体以 JSON 格式写入日志文件。
func logRequest(iter int, params openai.ChatCompletionNewParams) {
	reqJSON, err := json.MarshalIndent(params, "", "  ")
	if err != nil {
		apiLogger.Printf("[REQ iter=%d] JSON marshal error: %v\n", iter, err)
		return
	}
	apiLogger.Printf("\n========== REQUEST iter=%d (%s) ==========\n%s\n",
		iter, time.Now().Format(time.RFC3339), string(reqJSON))
}

// logResponse 将响应体以 JSON 格式写入日志文件。
func logResponse(iter int, resp *openai.ChatCompletion) {
	respJSON, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		apiLogger.Printf("[RESP iter=%d] JSON marshal error: %v\n", iter, err)
		return
	}
	apiLogger.Printf("\n========== RESPONSE iter=%d (%s) ==========\n%s\n",
		iter, time.Now().Format(time.RFC3339), string(respJSON))
}

func handleError(err error) {
	log.Printf("调用大模型失败" + err.Error())
}
