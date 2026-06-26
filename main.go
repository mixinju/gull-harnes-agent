package main

import (
	"fmt"
	"log"
	"os"

	"gull-herness-agent/agent"
	"gull-herness-agent/prompt"
	"gull-herness-agent/skill"
	"gull-herness-agent/tool"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

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
			log.Printf("关闭文件失败: %v", err)
		}
	}(logFile)
	logger := log.New(logFile, "", log.LstdFlags)

	// 创建 LLM 客户端
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	)

	// 注册工具
	registry := tool.NewRegistry()
	registry.Register(tool.NewWeatherTool())
	registry.Register(tool.NewBashTool())
	registry.Register(tool.NewFileReadTool())
	registry.Register(tool.NewFileWriteTool())

	// 加载 Skill → 注册 use_skill
	loader := skill.NewLoader("./skills")
	skills, err := loader.Load()
	if err != nil {
		log.Fatalf("加载 Skill 失败: %v", err)
	}
	if len(skills) > 0 {
		registry.Register(tool.NewUseSkillTool(skills))
		fmt.Printf("已加载 %d 个 Skill，已注册 %d 个工具\n", len(skills), registry.Size())
	}

	// 组装 System Prompt
	pb := prompt.NewBuilder().
		WithIdentity(prompt.DefaultIdentity).
		WithSkills(skills).
		WithRule(prompt.RuleSelfDebug).
		WithRule(prompt.RuleReadBeforeWrite).
		WithRule(prompt.RuleFailGracefully).
		WithWorkingContext()

	// 创建 Agent 并启动
	ag := agent.New(client,
		agent.WithRegistry(registry),
		agent.WithPrompt(pb),
		agent.WithMessages([]openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("北京今天天气怎么样？"),
		}),
		agent.WithMaxIterations(10),
		agent.WithTokenThreshold(200_000),
		agent.WithLogger(logger),
	)
	ag.Run()
}
