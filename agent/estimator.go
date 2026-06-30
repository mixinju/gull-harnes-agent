package agent

import (
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
)

// TokenEstimator 估算消息列表的 token 数量。
//
// 为什么需要估算而不是直接用 API 返回的 usage？
// 因为 usage 是"上一次请求"的精确值，而我们想在"发下一次请求前"预判是否该压缩。
// 估算用于 Append 时的增量计算，usage 用于请求回来后的校准——两者配合。
type TokenEstimator interface {
	// Estimate 估算单条消息的 token 数。
	Estimate(msg openai.ChatCompletionMessageParamUnion) int
}

// CharRatioEstimator 基于字符比例的粗略 token 估算器。
//
// 不依赖外部库，按经验比例估算：约 2.5 字符/token（中英文混杂的折中值）。
// 精度不高，但用于"判断是否该压缩"足够了——真正的精确值由 API 的 usage 校准。
type CharRatioEstimator struct {
	ratio float64
}

// NewCharRatioEstimator 创建一个字符比例估算器，默认比例 2.5 字符/token。
func NewCharRatioEstimator() *CharRatioEstimator {
	return &CharRatioEstimator{ratio: 2.5}
}

// Estimate 估算单条消息的 token 数。
func (e *CharRatioEstimator) Estimate(msg openai.ChatCompletionMessageParamUnion) int {
	text := extractMessageText(msg)
	charCount := float64(len([]rune(text)))
	return int(charCount/e.ratio) + 4 // +4 为 role 等结构开销的粗略补偿
}

// extractMessageText 从 ChatCompletionMessageParamUnion 中提取文本内容。
//
// openai-go 的消息是结构体 union（OfSystem/OfUser/OfAssistant/OfTool 指针字段），
// Content 也是 union（OfString/OfArrayOfContentParts）。逐层取值即可。
func extractMessageText(msg openai.ChatCompletionMessageParamUnion) string {
	if !param.IsOmitted(msg.OfSystem) {
		return systemContentToString(msg.OfSystem.Content)
	}
	if !param.IsOmitted(msg.OfUser) {
		return userContentToString(msg.OfUser.Content)
	}
	if !param.IsOmitted(msg.OfAssistant) {
		return assistantContentToString(msg.OfAssistant)
	}
	if !param.IsOmitted(msg.OfTool) {
		return toolContentToString(msg.OfTool.Content)
	}
	return ""
}

func systemContentToString(c openai.ChatCompletionSystemMessageParamContentUnion) string {
	if !param.IsOmitted(c.OfString) {
		return c.OfString.Value
	}
	var sb []byte
	for _, part := range c.OfArrayOfContentParts {
		sb = append(sb, part.Text...)
	}
	return string(sb)
}

func userContentToString(c openai.ChatCompletionUserMessageParamContentUnion) string {
	if !param.IsOmitted(c.OfString) {
		return c.OfString.Value
	}
	var sb []byte
	for _, part := range c.OfArrayOfContentParts {
		if !param.IsOmitted(part.OfText) {
			sb = append(sb, part.OfText.Text...)
		}
	}
	return string(sb)
}

func toolContentToString(c openai.ChatCompletionToolMessageParamContentUnion) string {
	if !param.IsOmitted(c.OfString) {
		return c.OfString.Value
	}
	var sb []byte
	for _, part := range c.OfArrayOfContentParts {
		sb = append(sb, part.Text...)
	}
	return string(sb)
}

// assistantContentToString 从 AssistantMessageParam 提取文本。
// assistant 的 Content 是 ContentUnion（含 OfString），取值后为空时统计 tool_calls。
func assistantContentToString(m *openai.ChatCompletionAssistantMessageParam) string {
	if !param.IsOmitted(m.Content.OfString) {
		return m.Content.OfString.Value
	}
	var sb []byte
	for _, call := range m.ToolCalls {
		sb = append(sb, call.Function.Arguments...)
		sb = append(sb, call.Function.Name...)
	}
	return string(sb)
}

