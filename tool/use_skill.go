package tool

import (
	"fmt"

	"gull-herness-agent/skill"

	"github.com/openai/openai-go"
)

// UseSkillTool 用于按需加载 Skill 的完整说明文档。
//
// 在 System Prompt 中只注入 Skill 的名称和描述（元数据标签），
// 详细的领域指南（SKILL.md 正文）通过此工具按需获取，
// 避免每次请求都携带大量 Skill 内容。
type UseSkillTool struct {
	skills []*skill.Skill
}

// NewUseSkillTool 创建一个 UseSkillTool 实例。
func NewUseSkillTool(skills []*skill.Skill) *UseSkillTool {
	return &UseSkillTool{skills: skills}
}

func (t *UseSkillTool) Name() string {
	return "use_skill"
}

func (t *UseSkillTool) Description() string {
	return "加载指定 Skill 的完整说明文档。" +
		"当任务涉及某个专业领域（如代码审查、性能优化等）时，" +
		"先调用此工具获取该 Skill 的详细指南和行为规范。" +
		"可用 Skill 的名称请参考 System Prompt 中的 <skill> 标签。"
}

func (t *UseSkillTool) Schema() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{
		Name:        t.Name(),
		Description: openai.String(t.Description()),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "要加载的 Skill 名称。可用 Skill 列表见 System Prompt。",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (t *UseSkillTool) Execute(args map[string]any) (string, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return "", fmt.Errorf("缺少必填参数: name")
	}

	for _, s := range t.skills {
		if s.Name == name {
			if s.Prompt == "" {
				return fmt.Sprintf("<skill name=%q/>（该 Skill 没有额外的说明文档）", name), nil
			}
			return fmt.Sprintf("<skill name=%q>\n%s\n</skill>", name, s.Prompt), nil
		}
	}

	return "", fmt.Errorf("未找到 Skill: %s", name)
}

