package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"ai-shell/internal/config"
	"ai-shell/internal/ollama"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "ai: %v\n", err)
		os.Exit(1)
	}
}

type queryOptions struct {
	modelOverride string
	prompt        string
}

func run(args []string, stdout, stderr io.Writer) error {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	model, err := selectModel(cfg.Ollama, opts.modelOverride)
	if err != nil {
		return err
	}

	client, err := ollama.New(cfg.Ollama)
	if err != nil {
		return err
	}

	err = client.Generate(context.Background(), ollama.GenerateRequest{
		Model:  model,
		Prompt: opts.prompt,
	}, func(chunk ollama.GenerateChunk) error {
		if chunk.Response == "" {
			return nil
		}
		_, err := fmt.Fprint(stdout, chunk.Response)
		return err
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(stdout)
	return err
}

func parseArgs(args []string, stderr io.Writer) (queryOptions, error) {
	flags := flag.NewFlagSet("ai", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var opts queryOptions
	flags.StringVar(&opts.modelOverride, "model", "", "use a model tag for this query only")

	if err := flags.Parse(args); err != nil {
		return queryOptions{}, err
	}

	remaining := flags.Args()
	if len(remaining) == 0 {
		return queryOptions{}, errors.New("usage: ai [--model <tag>] <query>")
	}

	switch remaining[0] {
	case "daemon", "model", "models":
		return queryOptions{}, fmt.Errorf("%q is not implemented until a later build step", remaining[0])
	}

	opts.prompt = strings.Join(remaining, " ")
	return opts, nil
}

func selectModel(cfg config.OllamaConfig, override string) (string, error) {
	if override != "" {
		return override, nil
	}

	if cfg.DefaultProfile == "" {
		return "", errors.New("ollama.default_profile is empty")
	}

	model, ok := cfg.Models[cfg.DefaultProfile]
	if !ok || model == "" {
		return "", fmt.Errorf("ollama.default_profile %q is not defined in ollama.models", cfg.DefaultProfile)
	}

	return model, nil
}
