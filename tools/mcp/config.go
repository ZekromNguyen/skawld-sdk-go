package mcp

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const protocolVersion = "2025-06-18"

type ServerConfig struct {
	Name     string
	Stdio    *StdioServerConfig
	HTTP     *HTTPServerConfig
	Disabled bool
}

type StdioServerConfig struct {
	Command string
	Args    []string
	Env     map[string]string
	CWD     string
}

type HTTPServerConfig struct {
	URL     string
	Headers map[string]string
}

func (c ServerConfig) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("mcp server name is required")
	}
	if NormalizeName(c.Name) == "" {
		return fmt.Errorf("mcp server name %q has no valid tool-name characters", c.Name)
	}
	transports := 0
	if c.Stdio != nil {
		transports++
		if strings.TrimSpace(c.Stdio.Command) == "" {
			return fmt.Errorf("mcp stdio server %q requires command", c.Name)
		}
	}
	if c.HTTP != nil {
		transports++
		if strings.TrimSpace(c.HTTP.URL) == "" {
			return fmt.Errorf("mcp http server %q requires url", c.Name)
		}
		if _, err := url.ParseRequestURI(c.HTTP.URL); err != nil {
			return fmt.Errorf("mcp http server %q has invalid url: %w", c.Name, err)
		}
	}
	if transports != 1 {
		return fmt.Errorf("mcp server %q must configure exactly one transport", c.Name)
	}
	return nil
}

func ToolName(serverName, remoteTool string) string {
	server := NormalizeName(serverName)
	tool := NormalizeName(remoteTool)
	if server == "" {
		server = "server"
	}
	if tool == "" {
		tool = "tool"
	}
	return "mcp__" + server + "__" + tool
}

func UniqueToolName(serverName, remoteTool string, used map[string]struct{}) string {
	base := ToolName(serverName, remoteTool)
	name := base
	for i := 2; ; i++ {
		if _, exists := used[name]; !exists {
			used[name] = struct{}{}
			return name
		}
		name = fmt.Sprintf("%s_%d", base, i)
	}
}

var invalidNameRun = regexp.MustCompile(`[^A-Za-z0-9_]+`)

func NormalizeName(value string) string {
	value = strings.TrimSpace(value)
	value = invalidNameRun.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	return value
}
