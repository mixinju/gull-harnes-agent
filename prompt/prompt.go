package prompt

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"gull-herness-agent/skill"
)

// Builder 用于结构化地组装 System Prompt。
//
// 设计原则：只负责"身份+Skill+行为准则+环境"，不包含工具清单。
// 工具信息由 Function Calling 的 tools 参数携带，不在 prompt 里重复。
type Builder struct {
	identity string
	skills   []*skill.Skill
	rules    []string
	context  map[string]string
}

// NewBuilder 创建一个空的 Prompt 构建器。
func NewBuilder() *Builder {
	return &Builder{
		context: make(map[string]string),
	}
}

// WithIdentity 设置 Agent 的身份描述。
func (b *Builder) WithIdentity(identity string) *Builder {
	b.identity = identity
	return b
}

// WithSkills 注入从外部加载的 Skill 列表。
// 每个 Skill 的 Name 和 Description 输出为标题。
func (b *Builder) WithSkills(skills []*skill.Skill) *Builder {
	b.skills = append(b.skills, skills...)
	return b
}

// WithRule 追加一条行为准则。
func (b *Builder) WithRule(rule string) *Builder {
	b.rules = append(b.rules, rule)
	return b
}

// WithWorkingContext 自动注入运行时上下文信息。
func (b *Builder) WithWorkingContext() *Builder {
	workDir, _ := os.Getwd()
	b.context["workdir"] = workDir
	b.context["os"] = runtime.GOOS + "/" + runtime.GOARCH
	b.context["time"] = time.Now().Format("2006-01-02 15:04:05")
	return b
}

// WithContext 手动添加一条上下文信息。
func (b *Builder) WithContext(key, value string) *Builder {
	b.context[key] = value
	return b
}

// Build 组装最终的 System Prompt 字符串。
//
// 输出格式：
//
//	# 身份
//	<identity>
//
//	# 已加载的 Skill
//	- <skill-name>: <skill-description>
//
//	# 行为准则
//	- <rule1>
//	- <rule2>
//
//	# 环境信息
//	- ...
func (b *Builder) Build() string {
	var sb strings.Builder

	// 身份
	if b.identity != "" {
		sb.WriteString("# 身份\n")
		sb.WriteString(b.identity)
		sb.WriteString("\n\n")
	}

	// 已加载的 Skill
	if len(b.skills) > 0 {
		sb.WriteString("# 已加载的 Skill\n")
		for _, s := range b.skills {
			sb.WriteString(fmt.Sprintf("<skill name=\"%s\" description=\"%s\"/>\n", s.Name, s.Description))
		}
		sb.WriteString("\n")
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

