package skill

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Skill 代表一个从 SKILL.md 文件中加载的专业领域能力。
//
// 每个 Skill 对应 skills/ 目录下的一个子文件夹，
// 子文件夹中包含一个 SKILL.md 文件，其 frontmatter 定义元数据，
// 正文内容作为额外的 System Prompt 注入 Agent。
type Skill struct {
	// Name 是 Skill 的名称，对应 SKILL.md frontmatter 中的 name 字段。
	Name string

	// Description 是 Skill 的简要描述，对应 SKILL.md frontmatter 中的 description 字段。
	Description string

	// Prompt 是 SKILL.md 的正文内容（frontmatter 之后的部分），
	// 会作为额外的 System Prompt 注入到 Agent 的上下文中。
	Prompt string

	// Path 是 Skill 目录在文件系统中的绝对路径。
	Path string
}

// Parse 解析指定路径的 SKILL.md 文件，返回一个 Skill 实例。
//
// SKILL.md 文件格式：
//
//	---
//	name: skill-name
//	description: 简要描述
//	---
//	正文内容（会被用作额外的 System Prompt）
//
// frontmatter 部分使用 --- 作为分隔符，内部为简单的 key: value 格式。
func Parse(skillDir string) (*Skill, error) {
	mdPath := filepath.Join(skillDir, "SKILL.md")
	f, err := os.Open(mdPath)
	if err != nil {
		return nil, err
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {

		}
	}(f)

	s := &Skill{Path: skillDir}

	scanner := bufio.NewScanner(f)
	var inFrontmatter bool
	var frontmatterLines []string
	var bodyLines []string

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			// 第二个 ---：frontmatter 结束
			inFrontmatter = false
			continue
		}

		if inFrontmatter {
			frontmatterLines = append(frontmatterLines, trimmed)
		} else {
			bodyLines = append(bodyLines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// 解析 frontmatter
	for _, fl := range frontmatterLines {
		if fl == "" {
			continue
		}
		parts := strings.SplitN(fl, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "name":
			s.Name = val
		case "description":
			s.Description = val
		}
	}

	// 去掉正文首尾的空行
	for len(bodyLines) > 0 && strings.TrimSpace(bodyLines[0]) == "" {
		bodyLines = bodyLines[1:]
	}
	for len(bodyLines) > 0 && strings.TrimSpace(bodyLines[len(bodyLines)-1]) == "" {
		bodyLines = bodyLines[:len(bodyLines)-1]
	}

	s.Prompt = strings.Join(bodyLines, "\n")
	return s, nil
}

