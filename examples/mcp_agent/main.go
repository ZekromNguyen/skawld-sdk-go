package main

import (
	"context"
	"fmt"
	"os"

	skawld "github.com/ZekromNguyen/skawld-sdk-go"
	"github.com/ZekromNguyen/skawld-sdk-go/providers"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
	"github.com/ZekromNguyen/skawld-sdk-go/tools/mcp"
)

func main() {
	agent, err := skawld.NewAgent(skawld.AgentOptions{
		Provider: providers.NewOpenAIResponsesProvider(providers.OpenAIOptions{}),
		Model:    "gpt-5",
		Tools:    tools.DefaultTools(),
		Permissions: skawld.PermissionOptions{
			Mode: skawld.PermissionModeDefault,
		},
		MCPServers: []mcp.ServerConfig{{
			Name: "local",
			Stdio: &mcp.StdioServerConfig{
				Command: "npx",
				Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "."},
			},
			Disabled: os.Getenv("SKAWLD_ENABLE_EXAMPLE_MCP") == "",
		}},
	})
	if err != nil {
		panic(err)
	}
	defer agent.Close()

	session, err := agent.Session(context.Background(), skawld.SessionOptions{})
	if err != nil {
		panic(err)
	}
	for event := range session.Run(context.Background(), "List the available MCP tools.", skawld.RunOptions{}) {
		if event.Type == skawld.EventResult {
			fmt.Println(event.FinalText)
			break
		}
	}
}
