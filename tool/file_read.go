package tool

import (
	"fmt"
	"os"
	"strings"

	"github.com/openai/openai-go"
)

// FileReadTool 读取文件内容，支持分片读取和行号标注。
type FileReadTool struct{}

// NewFileReadTool 创建一个 FileReadTool 实例。
func NewFileReadTool() *FileReadTool {
	return &FileReadTool{}
}

func (t *FileReadTool) Name() string {
	return "file_read"
}

func (t *FileReadTool) Description() string {
	return "读取指定文件的内容，带行号标注。" +
		"适用场景：查看源代码、阅读日志文件、检查配置文件等。" +
		"支持通过 offset（起始行）和 limit（行数限制）进行分片读取，避免一次输出过多内容。"
}

func (t *FileReadTool) Schema() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{
		Name:        t.Name(),
		Description: openai.String(t.Description()),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "要读取的文件路径（相对或绝对路径）。",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "起始行号（从 1 开始），默认从文件开头读取。",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "最多读取的行数，默认读取全部内容。大文件建议设为 100-200。",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *FileReadTool) Execute(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", fmt.Errorf("缺少必填参数: path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("文件不存在: %s", path)
		}
		return "", fmt.Errorf("读取文件失败: %v", err)
	}

	lines := strings.Split(string(data), "\n")
	// 去掉末尾因 ReadFile 产生的空行
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	totalLines := len(lines)
	if totalLines == 0 {
		return fmt.Sprintf("文件 %s 为空", path), nil
	}

	// 解析 offset（从 1 开始）
	offset := 1
	if v, ok := args["offset"]; ok {
		offset = toInt(v, 1)
	}
	if offset < 1 {
		offset = 1
	}
	if offset > totalLines {
		return fmt.Sprintf("文件 %s 共 %d 行，offset=%d 已超出范围", path, totalLines, offset), nil
	}

	// 解析 limit
	limit := totalLines - offset + 1
	if v, ok := args["limit"]; ok {
		limit = toInt(v, limit)
	}
	if limit < 1 {
		limit = totalLines - offset + 1
	}

	end := offset + limit - 1
	if end > totalLines {
		end = totalLines
	}

	// 构建带行号的输出
	var b strings.Builder
	header := fmt.Sprintf("文件: %s（共 %d 行，显示第 %d-%d 行）", path, totalLines, offset, end)
	b.WriteString(header)
	b.WriteString("\n" + strings.Repeat("-", len(header)) + "\n")

	// 行号宽度按总行数的位数决定，保证对齐
	width := len(fmt.Sprintf("%d", totalLines))
	for i := offset - 1; i < end; i++ {
		b.WriteString(fmt.Sprintf("%*d|%s\n", width, i+1, lines[i]))
	}

	if end < totalLines {
		b.WriteString(fmt.Sprintf(
			"\n...（还有 %d 行未显示，设置 offset=%d 可继续读取）",
			totalLines-end, end+1,
		))
	}

	return b.String(), nil
}

// toInt 将 args 中的 JSON number 转为 int。
// JSON unmarshal 后 number 类型可能是 float64 或 int（取决于解码方式），这里统一处理。
func toInt(v any, defaultVal int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return defaultVal
	}
}
