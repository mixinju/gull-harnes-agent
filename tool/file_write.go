package tool

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/openai/openai-go"
)

// FileWriteTool 向文件中写入内容，会自动创建不存在的父目录。
type FileWriteTool struct{}

// NewFileWriteTool 创建一个 FileWriteTool 实例。
func NewFileWriteTool() *FileWriteTool {
	return &FileWriteTool{}
}

func (t *FileWriteTool) Name() string {
	return "file_write"
}

func (t *FileWriteTool) Description() string {
	return "将文本内容写入指定文件。" +
		"适用场景：创建新文件、修改已有文件、生成代码或配置文件等。" +
		"会自动创建不存在的父目录，省去手动 mkdir 的步骤。" +
		"注意：写入会覆盖文件的原有内容。"
}

func (t *FileWriteTool) Schema() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{
		Name:        t.Name(),
		Description: openai.String(t.Description()),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "要写入的文件路径（相对或绝对路径）。",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "要写入文件的文本内容。",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t *FileWriteTool) Execute(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", fmt.Errorf("缺少必填参数: path")
	}

	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("缺少必填参数: content")
	}

	// 自动创建父目录
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建目录 %s 失败: %v", dir, err)
	}

	// 写入文件（权限 0644）
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("写入文件 %s 失败: %v", path, err)
	}

	return fmt.Sprintf("已成功写入文件 %s（%d 字符）", path, len(content)), nil
}
