package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	skawld "github.com/skawld/skawld-sdk-go"
	"github.com/skawld/skawld-sdk-go/providers"
	"github.com/skawld/skawld-sdk-go/tools"
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
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Fprint(os.Stdout, "> ")
		if !scanner.Scan() {
			break
		}
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" || prompt == "exit" {
			break
		}
		for event := range session.Run(context.Background(), prompt, skawld.RunOptions{}) {
			if event.Type == skawld.EventAssistant {
				for _, block := range event.Message.Content {
					if block.Type == skawld.BlockText {
						fmt.Fprint(os.Stdout, block.Text)
					}
				}
			}
			if event.Type == skawld.EventResult {
				fmt.Fprintln(os.Stdout)
				break
			}
		}
	}
}
