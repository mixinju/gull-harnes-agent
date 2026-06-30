package agent

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
)

// Summarizer 将一组历史消息压缩成一条摘要消息。
//
// 上下文窗口有限，对话太长时不能无限堆积消息。Summarizer 负责把
// "前面聊了什么"浓缩成一段文本，替换掉旧消息，从而腾出 token 空间。
type Summarizer interface {
	// Summarize 把 messages 压缩成一条 system 摘要消息。
	Summarize(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (openai.ChatCompletionMessageParamUnion, error)
}

// LLMSummarizer 用 LLM 自身来做摘要的实现。
//
// 思路：把要压缩的历史消息作为 user 输入喂给 LLM，让它输出一段
// "对话摘要"。这条摘要作为新的 system 消息替换掉旧消息。
type LLMSummarizer struct {
	client openai.Client
	model  string
}

// NewLLMSummarizer 创建一个基于 LLM 的摘要器。
func NewLLMSummarizer(client openai.Client, model string) *LLMSummarizer {
	return &LLMSummarizer{client: client, model: model}
}

// Summarize 调用 LLM 生成摘要。
func (s *LLMSummarizer) Summarize(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (openai.ChatCompletionMessageParamUnion, error) {
	// 构造摘要请求：system 指令 + 把历史消息原样作为上下文
	summarizePrompt := "你是一个对话摘要助手。请把下面的历史对话浓缩成一段简洁的摘要，" +
		"保留关键事实、已做的决策、待办事项和重要上下文。用中文输出，不要遗漏关键信息。"

	// 把历史消息拼成文本作为 user 输入
	historyText := "以下是历史对话，请摘要：\n\n"
	for _, m := range messages {
		historyText += fmt.Sprintf("[%s] %s\n", roleOf(m), extractMessageText(m))
	}

	resp, err := s.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: s.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(summarizePrompt),
			openai.UserMessage(historyText),
		},
	})
	if err != nil {
		return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("摘要请求失败: %w", err)
	}
	if len(resp.Choices) == 0 {
		return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("摘要响应为空")
	}

	summary := resp.Choices[0].Message.Content
	// 摘要作为 system 消息返回，带前缀标识
	return openai.SystemMessage("[对话摘要] " + summary), nil
}

// roleOf 提取消息的 role 文本，用于摘要拼接。
func roleOf(msg openai.ChatCompletionMessageParamUnion) string {
	switch {
	case !paramIsSet(msg.OfSystem):
		return "system"
	case !paramIsSet(msg.OfUser):
		return "user"
	case !paramIsSet(msg.OfAssistant):
		return "assistant"
	case !paramIsSet(msg.OfTool):
		return "tool"
	default:
		return "unknown"
	}
}

// paramIsSet 是 param.IsOmitted 的语义包装（取反），纯粹为了让 roleOf 可读。
func paramIsSet[T any](p *T) bool {
	return p != nil
}

