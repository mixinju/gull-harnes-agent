package skill

import (
	"log"
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
// 降级策略：
//   - 目录不存在时静默跳过，允许用户按需创建目录
//   - 目录读取失败或单个 Skill 解析失败时只记录日志并跳过，
//     不影响其他 Skill 的加载
//
// 因此 Load 永远不会返回错误，调用方只需处理返回的 skills 列表。
func (l *Loader) Load() []*Skill {
	var skills []*Skill

	for _, dir := range l.dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			log.Printf("读取 skill 目录 %s 失败，跳过: %v", dir, err)
			continue
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
				log.Printf("解析 skill %s 失败，跳过: %v", skillDir, err)
				continue
			}

			skills = append(skills, s)
		}
	}

	return skills
}

