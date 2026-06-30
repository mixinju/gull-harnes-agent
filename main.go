package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"gull-herness-agent/agent"
	"gull-herness-agent/config"
	"gull-herness-agent/logger"
	"gull-herness-agent/mcp"
	"gull-herness-agent/prompt"
	"gull-herness-agent/session"
	"gull-herness-agent/skill"
	"gull-herness-agent/tool"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func main() {
	configPath := flag.String("config", "config/config.json", "配置文件路径")
	promptFlag := flag.String("prompt", "", "用户输入（与位置参数二选一）")
	flag.Parse()

	// 用户输入：-prompt 优先，否则取位置参数拼接
	userInput := *promptFlag
	if userInput == "" {
		userInput = strings.Join(flag.Args(), " ")
	}
	if userInput == "" {
		_, _ = fmt.Fprintln(os.Stderr, "用法: gull-herness-agent -prompt \"你的问题\"")
		_, _ = fmt.Fprintln(os.Stderr, "      gull-herness-agent 你的问题")
		os.Exit(1)
	}

	// 1. 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 初始化日志器
	lg, err := logger.New(cfg.LogDir)
	if err != nil {
		log.Fatalf("初始化日志器失败: %v", err)
	}
	defer lg.Close()

	lg.Info("[启动] 配置加载完成: model=%s threshold=%d max_iter=%d",
		cfg.Model, cfg.ContextThreshold, cfg.MaxIterations)

	// 3. 创建 LLM 客户端
	client := openai.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(cfg.BaseURL),
	)

	// 4. 注册工具
	registry := tool.NewRegistry()
	registry.Register(tool.NewWeatherTool())
	registry.Register(tool.NewBashTool())
	registry.Register(tool.NewFileReadTool())
	registry.Register(tool.NewFileWriteTool())

	// 5. 从 config/mcp.json 加载 MCP 工具（降级：失败不影响内置工具）
	defer mcp.LoadAll(registry, cfg.MCPConfig)()

	// 6. 加载 Skill → 注册 use_skill
	skills := skill.NewLoader(cfg.SkillsDir).Load()
	registry.Register(tool.NewUseSkillTool(skills))
	lg.Info("[启动] 已加载 %d 个 Skill，已注册 %d 个工具", len(skills), registry.Size())

	// 7. 组装 System Prompt
	pb := prompt.NewBuilder().
		WithIdentity(prompt.DefaultIdentity).
		WithSkills(skills).
		WithRule(prompt.RuleSelfDebug).
		WithRule(prompt.RuleReadBeforeWrite).
		WithRule(prompt.RuleFailGracefully).
		WithWorkingContext()

	// 8. 创建上下文管理器：带 LLM 摘要能力，token 超阈值时自动压缩历史
	ctx := agent.NewContext(
		agent.WithSummarizer(agent.NewLLMSummarizer(client, cfg.Model)),
		agent.WithThreshold(cfg.ContextThreshold),
	)

	// 9. 创建会话
	sess := session.New(cfg.Model, userInput)
	lg.Info("[启动] 会话已创建: %s", sess.ID)
	lg.Info("")

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
	if err := sess.Save(cfg.SessionDir); err != nil {
		lg.Error("保存会话失败: %v", err)
	} else {
		lg.Info("")
		lg.Info("[完成] 会话已保存: %s/%s.json (%d 轮, %d tokens)",
			cfg.SessionDir, sess.ID, sess.Iterations, sess.TotalTokens)
	}
}
