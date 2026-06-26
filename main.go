package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

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

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("你是一个简洁友好的助手，必要时可以调用工具来获取信息。"),
		openai.UserMessage("北京今天天气怎么样？"),
	}

	tools := []openai.ChatCompletionToolParam{
		{
			Function: openai.FunctionDefinitionParam{
				Name:        "getWeather",
				Description: openai.String("查询指定城市的当前天气"),
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
			},
		},
	}

	runAgentLoop(client, messages, tools)
}

// runAgentLoop 是 agent 主循环：反复“调用模型 -> 执行工具 -> 回填结果”，
// 直到满足以下任一终止条件：
//  1. 达到最大迭代次数 maxIterations；
//  2. 模型本轮没有发起工具调用（已给出最终回复）；
//  3. API 异常（err != nil 或 finish_reason == "length"）；
//  4. 累计 token 用量达到 tokenThreshold（从 resp.Usage.TotalTokens 读取）。
func runAgentLoop(
	client openai.Client,
	messages []openai.ChatCompletionMessageParamUnion,
	tools []openai.ChatCompletionToolParam,
) {
	for i := 1; i <= maxIterations; i++ {
		fmt.Printf("=== iteration %d ===\n", i)

		params := openai.ChatCompletionNewParams{
			Model:    "deepseek-v4-flash",
			Messages: messages,
			Tools:    tools,
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
			result := dispatchTool(call)
			fmt.Printf("[tool] %s -> %s\n", call.Function.Name, result)
			messages = append(messages, openai.ToolMessage(result, call.ID))
		}
	}

	log.Printf("达到最大迭代次数 %d，终止 agent loop", maxIterations)
}

// dispatchTool 根据工具名分发到对应的本地实现。
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

// getWeather 是一个 mock 的天气查询方法，随机返回天气信息。
func getWeather(city string) string {
	conditions := []string{"晴", "多云", "阴", "小雨", "中雨", "雷阵雨", "小雪"}
	condition := conditions[rand.Intn(len(conditions))]
	tempC := rand.Intn(30) + 5     // 5 ~ 34 ℃
	humidity := rand.Intn(60) + 30 // 30 ~ 89 %
	return fmt.Sprintf("%s 当前天气：%s，气温 %d℃，湿度 %d%%", city, condition, tempC, humidity)
}

func handleError(err error) {
	log.Printf("调用大模型失败" + err.Error())
}
