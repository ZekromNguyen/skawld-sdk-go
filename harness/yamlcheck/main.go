package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yamlcheck <file>")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	var doc struct {
		Jobs map[string]struct {
			Name string `yaml:"name"`
			Needs []string `yaml:"needs"`
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
				Uses string `yaml:"uses"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}
	for k, j := range doc.Jobs {
		fmt.Printf("job %-15s  name=%q  needs=%v  steps=%d\n", k, j.Name, j.Needs, len(j.Steps))
	}
}
