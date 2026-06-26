package skill

import (
	"fmt"
	"os"
	"path/filepath"
)

// Loader 负责从指定目录中扫描并加载 Skill。
//
// 扫描逻辑：遍历每个目录，检查其所有一级子目录中是否包含 SKILL.md 文件。
// 如果包含，则解析为 Skill 实例。
type Loader struct {
	dirs []string
}

// NewLoader 创建一个 Loader 实例，dirs 是要扫描的目录列表。
func NewLoader(dirs ...string) *Loader {
	return &Loader{dirs: dirs}
}

// Load 扫描所有已配置的目录，返回找到的 Skill 列表。
//
// 加载流程：
//  1. 遍历 dirs 中的每个目录
//  2. 读取该目录下的所有子目录
//  3. 检查每个子目录中是否存在 SKILL.md 文件
//  4. 如果存在，调用 Parse 解析为 Skill
//
// 如果一个目录不存在，会静默跳过（不报错），允许用户按需创建目录。
func (l *Loader) Load() ([]*Skill, error) {
	var skills []*Skill

	for _, dir := range l.dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// 目录不存在时跳过，不报错
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("读取 skill 目录 %s 失败: %w", dir, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			skillDir := filepath.Join(dir, entry.Name())
			mdPath := filepath.Join(skillDir, "SKILL.md")

			if _, err := os.Stat(mdPath); err != nil {
				continue
			}

			s, err := Parse(skillDir)
			if err != nil {
				return nil, fmt.Errorf("解析 skill %s 失败: %w", skillDir, err)
			}

			skills = append(skills, s)
		}
	}

	return skills, nil
}

