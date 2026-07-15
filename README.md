# Gull Agent Harness

[七天教程地址](https://mixinju.github.io/gull-harnes-agent/)
一个使用 Go 从零实现的轻量级 Agent Harness，同时也是一套循序渐进的七日实战教程。

项目不依赖大型 Agent 框架，直接围绕大模型 API 实现 Agent Loop、工具调用、上下文管理、Skill 扩展和 MCP 集成，帮助你理解 Agent 运行时的核心机制。

## 特性

- **Agent Loop**：实现思考、行动、观察的多轮执行闭环
- **工具系统**：内置 Bash、文件读取、文件写入和天气查询工具
- **上下文管理**：支持 Token 估算、阈值检测与 LLM 摘要压缩
- **Skill 扩展**：通过目录动态加载可插拔技能
- **MCP 集成**：连接外部 MCP Server，扩展 Agent 工具集
- **会话记录**：保存执行轮次、Token 用量和历史消息
- **运行日志**：记录模型调用、工具执行及异常信息
- **七日教程**：从基础概念到完整 Agent Harness，逐步讲解实现过程

## 项目结构

```text
.
├── agent/       # Agent Loop、上下文管理与摘要压缩
├── config/      # Agent 与 MCP 配置
├── docs/        # VitePress 七日教程
├── logger/      # 运行日志
├── mcp/         # MCP 客户端与工具适配
├── prompt/      # System Prompt 构建
├── session/     # 会话模型与持久化
├── skill/       # Skill 定义与加载器
├── skills/      # 可加载的技能目录
├── tool/        # 内置工具与工具注册表
└── main.go      # 程序入口
```

## 快速开始

### 环境要求

- Go 1.26+
- 一个兼容 OpenAI API 的模型服务

### 1. 克隆仓库

```bash
git clone git@github.com:mixinju/gull-harnes-agent.git
cd gull-harnes-agent
```

### 2. 配置模型服务

推荐通过环境变量设置密钥和 API 地址，避免将敏感信息写入配置文件：

```bash
export GULL_OPENAI_API_KEY="your-api-key"
export GULL_OPENAI_BASE_URL="https://your-openai-compatible-api.example.com"
```

模型名称及运行参数可在 `config/config.json` 中调整：

```json
{
  "api_key": "",
  "base_url": "",
  "model": "deepseek-v4-flash",
  "max_iterations": 10,
  "context_threshold": 200000,
  "log_dir": "logs",
  "session_dir": "sessions",
  "skills_dir": "./skills",
  "mcp_config": "config/mcp.json"
}
```

### 3. 运行 Agent

```bash
go run . -prompt "帮我分析一下当前项目结构"
```

也可以直接使用位置参数：

```bash
go run . 帮我检查 main.go 是否存在问题
```

指定其他配置文件：

```bash
go run . -config path/to/config.json -prompt "你的问题"
```

## MCP 配置

在 `config/mcp.json` 中配置需要连接的 MCP Server。MCP 服务加载失败时，Agent 会自动降级并继续使用内置工具，不影响基本功能。

## Skill 扩展

Skill 默认从 `skills/` 目录加载。每个 Skill 使用独立目录管理，并通过 `SKILL.md` 描述能力、使用场景和执行规则。

仓库内的 `skills/code-review/` 提供了一个代码审查 Skill 示例。

## 阅读教程

教程位于 `docs/`，内容按 Day 0 至 Day 7 组织：

- Day 0：理解 Agent Harness 与环境准备
- Day 1–6：逐步实现配置、工具、循环、上下文、Skill 与 MCP
- Day 7：组装完整 Agent 并回顾整体运行流程

本地启动教程站点：

```bash
cd docs
npm install
npm run dev
```

构建静态站点：

```bash
npm run build
```

## 核心理念

```text
Agent = Model + Harness
```

模型负责推理和生成，Harness 则负责模型之外的运行时能力，包括 Prompt、工具、消息、上下文、技能、安全边界与执行控制。本项目希望用尽可能直接的实现，让这些机制不再是黑盒。

## License

本项目采用 [PolyForm Noncommercial License 1.0.0](LICENSE)，允许个人、教育、研究及其他非商业用途免费使用、修改和分发。

商业使用不在授权范围内，如有商业使用需求，请联系仓库作者另行获取授权。
