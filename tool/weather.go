package tool

import (
	"fmt"
	"math/rand"

	"github.com/openai/openai-go"
)

// WeatherTool 是一个模拟的天气查询工具（从 main.go 迁移而来）。
// 用于演示工具注册表与 Agent Loop 的集成。
type WeatherTool struct{}

// NewWeatherTool 创建一个 WeatherTool 实例。
func NewWeatherTool() *WeatherTool {
	return &WeatherTool{}
}

func (t *WeatherTool) Name() string {
	return "getWeather"
}

func (t *WeatherTool) Description() string {
	return "查询指定城市的当前天气，返回天气状况、气温和湿度。"
}

func (t *WeatherTool) Schema() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{
		Name:        t.Name(),
		Description: openai.String(t.Description()),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{
					"type":        "string",
					"description": "要查询天气的城市名，例如：北京、上海",
				},
			},
			"required": []string{"city"},
		},
	}
}

func (t *WeatherTool) Execute(args map[string]any) (string, error) {
	city, ok := args["city"].(string)
	if !ok || city == "" {
		return "", fmt.Errorf("缺少必填参数: city")
	}

	conditions := []string{"晴", "多云", "阴", "小雨", "中雨", "雷阵雨", "小雪"}
	condition := conditions[rand.Intn(len(conditions))]
	tempC := rand.Intn(30) + 5     // 5 ~ 34 ℃
	humidity := rand.Intn(60) + 30 // 30 ~ 89 %

	return fmt.Sprintf("%s 当前天气：%s，气温 %d℃，湿度 %d%%", city, condition, tempC, humidity), nil
}

