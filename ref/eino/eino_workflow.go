package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/cloudwego/eino-examples/flow/agent/react/tools"
)

func newChatModel(ctx context.Context) model.ToolCallingChatModel {
	openaiAPIKey := os.Getenv("OAI_API_KEY")
	openaiModel := os.Getenv("OAI_MODEL")
	openaiBaseURL := os.Getenv("OAI_BASE_URL")

	var cm model.ToolCallingChatModel
	var err error
	var temp float32 = 0
	cm, err = openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:      openaiAPIKey,
		BaseURL:     openaiBaseURL,
		Model:       openaiModel,
		Temperature: &temp,
	})
	if err != nil {
		log.Fatal(err)
	}
	return cm
}

func main() {
	ctx := context.Background()
	model := newChatModel(ctx)

	// prepare tools
	restaurantTool := tools.GetRestaurantTool() // 查询餐厅信息的工具
	dishTool := tools.GetDishTool()             // 查询餐厅菜品信息的工具

	// prepare persona (system prompt) (optional)
	persona := `# Character:
你是一个帮助用户推荐餐厅和菜品的助手，根据用户的需要，查询餐厅信息并推荐，查询餐厅的菜品并推荐。
`

	ragent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: model,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: []tool.BaseTool{restaurantTool, dishTool},
		},
	})
	if err != nil {
		log.Fatalf("failed to create agent: %v", err)
		return
	}

	opt := []agent.AgentOption{
		agent.WithComposeOptions(compose.WithCallbacks(&LoggerCallback{})),
	}

	sr, err := ragent.Stream(ctx, []*schema.Message{
		{
			Role:    schema.System,
			Content: persona,
		},
		{
			Role:    schema.User,
			Content: "我在北京，给我推荐一些菜，需要有口味辣一点的菜，至少推荐有 2 家餐厅",
		},
	}, opt...)
	if err != nil {
		log.Fatalf("failed to stream: %v", err)
		return
	}

	defer sr.Close() // remember to close the stream

	// log.Printf("\n\n===== start streaming =====\n\n")

	// for {
	// 	msg, err := sr.Recv()
	// 	if err != nil {
	// 		if errors.Is(err, io.EOF) {
	// 			// finish
	// 			break
	// 		}
	// 		// error
	// 		log.Printf("failed to recv: %v", err)
	// 		return
	// 	}

	// 	// 打字机打印
	// 	log.Printf("%v", msg.Content)
	// }

	// log.Printf("\n\n===== finished =====\n")
	// time.Sleep(2 * time.Second)
}

type LoggerCallback struct {
	callbacks.HandlerBuilder // 可以用 callbacks.HandlerBuilder 来辅助实现 callback
}

func (cb *LoggerCallback) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	fmt.Println("==================")
	inputStr, _ := json.MarshalIndent(input, "", "  ")
	fmt.Printf("[OnStart] %s\n", string(inputStr))
	return ctx
}

func (cb *LoggerCallback) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	fmt.Println("=========[OnEnd]=========")
	outputStr, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(outputStr))
	return ctx
}

func (cb *LoggerCallback) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	fmt.Println("=========[OnError]=========")
	fmt.Println(err)
	return ctx
}

func (cb *LoggerCallback) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo,
	output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {

	var graphInfoName = react.GraphName
	fmt.Println("graphInfoName", graphInfoName)

	go func() {
		defer func() {
			if err := recover(); err != nil {
				fmt.Println("[OnEndStream] panic err:", err)
			}
		}()

		defer output.Close() // remember to close the stream in defer

		fmt.Println("=========[OnEndStream]=========")
		for {
			frame, err := output.Recv()
			if errors.Is(err, io.EOF) {
				// finish
				break
			}
			if err != nil {
				fmt.Printf("internal error: %s\n", err)
				return
			}

			s, err := json.Marshal(frame)
			if err != nil {
				fmt.Printf("internal error: %s\n", err)
				return
			}

			// if info.Name == graphInfoName { // 仅打印 graph 的输出, 否则每个 stream 节点的输出都会打印一遍
			fmt.Printf("XXXXXXXXXXXXXXX %s: %s\n", info.Name, string(s))
			// }
		}

	}()
	return ctx
}

func (cb *LoggerCallback) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo,
	input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	defer input.Close()
	return ctx
}
