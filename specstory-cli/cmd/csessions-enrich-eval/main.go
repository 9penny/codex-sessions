package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/enricheval"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/enrich"
)

func main() {
	modelsFlag := flag.String("models", "", "comma-separated model IDs")
	flag.Parse()
	var models []string
	for _, model := range strings.Split(*modelsFlag, ",") {
		if model = strings.TrimSpace(model); model != "" {
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		fmt.Fprintln(os.Stderr, "at least one --models value is required")
		os.Exit(2)
	}
	cfg, err := enrich.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	client, err := enrich.NewClient(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	for _, model := range models {
		summary := enricheval.Evaluate(context.Background(), client, model, enricheval.DefaultFixtures)
		if err := encoder.Encode(summary); err != nil {
			fmt.Fprintln(os.Stderr, "writing aggregate evaluation failed")
			os.Exit(1)
		}
	}
}
