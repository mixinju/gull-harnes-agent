package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func main() {
	apiKey := os.Getenv("GULL_OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("GULL_OPENAI_API_KEY environment variable is not set")
	}

	baseURL := os.Getenv("GULL_OPENAI_BASE_URL")
	if baseURL == "" {
		log.Fatal("GULL_OPENAI_BASE_URL environment variable is not set")
	}

	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	)

	prompt := "用一句话介绍你自己。"

	resp, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: openai.ChatModelGPT4oMini,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("你是一个简洁友好的助手。"),
			openai.UserMessage(prompt),
		},
	})
	if err != nil {
		log.Fatalf("chat completion failed: %v", err)
	}

	fmt.Println(resp.Choices[0].Message.Content)
}
