package main

import (
	"context"
	"fmt"
	"os"

	skawld "github.com/ZekromNguyen/skawld-sdk-go"
	"github.com/ZekromNguyen/skawld-sdk-go/providers"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

func main() {
	agent, err := skawld.NewAgent(skawld.AgentOptions{
		Provider: providers.NewOpenAIResponsesProvider(providers.OpenAIOptions{}),
		Model:    "gpt-5",
		Tools:    tools.DefaultTools(),
		Permissions: skawld.PermissionOptions{
			Mode: skawld.PermissionModeDefault,
		},
	})
	if err != nil {
		panic(err)
	}
	defer agent.Close()

	session, err := agent.Session(context.Background(), skawld.SessionOptions{})
	if err != nil {
		panic(err)
	}

	for event := range session.Run(context.Background(), "List the files in the current directory.", skawld.RunOptions{}) {
		if event.Type == skawld.EventAssistant {
			for _, block := range event.Message.Content {
				if block.Type == skawld.BlockText {
					fmt.Fprint(os.Stdout, block.Text)
				}
			}
		}
		if event.Type == skawld.EventResult {
			break
		}
	}
}
