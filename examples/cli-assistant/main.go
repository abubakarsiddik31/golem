// Command cli-assistant is a small interactive terminal agent: it streams
// answers, keeps conversation history, and gives the model bounded shell
// and file-read tools. With no provider configured it uses an offline
// streaming fake, so the loop still runs without credentials.
//
// Set OPENAI_API_KEY (and optionally OPENAI_MODEL) for OpenAI, or
// GOLEM_LOCAL_BASE_URL (and LOCAL_MODEL) for an Ollama/LM Studio
// OpenAI-compatible endpoint.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/fileread"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
	"github.com/abubakarsiddik31/golem/shell"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	client, description, err := newModel()
	if err != nil {
		fmt.Println("model:", err)
		return
	}
	fmt.Println("Using", description+". Type 'exit' or press Ctrl+C to quit.")

	root, err := os.Getwd()
	if err != nil {
		fmt.Println("getwd:", err)
		return
	}
	readFile, err := fileread.New[struct{}](fileread.Config{Root: root})
	if err != nil {
		fmt.Println("fileread.New:", err)
		return
	}
	runCommand := shell.MustNew[struct{}](shell.Config{
		Dir:               root,
		Timeout:           15 * time.Second,
		MaxBytes:          32 << 10,
		AllowedCommands:   []string{"ls", "pwd", "git", "go", "rg", "grep", "find", "wc", "head", "tail"},
		DeniedOperators:   shell.ChainingOperators,
		BlockInteractive:  true,
		DeniedEnvPatterns: shell.CredentialEnvPatterns,
	})

	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInstructions[struct{}, string]("You are a concise terminal assistant. Inspect the local workspace when it helps, and never invent command output."),
		golem.WithTools[struct{}, string](readFile, runCommand),
		golem.WithToolOutputLimit[struct{}, string](8<<10),
		golem.WithLoopDetection[struct{}, string](3),
		golem.WithRunEvents[struct{}, string](func(event golem.RunEvent) {
			switch event.Kind {
			case golem.EventToolStart:
				fmt.Printf("\n  [tool] %s\n", event.ToolName)
			case golem.EventToolEnd:
				if event.Err != nil {
					fmt.Printf("  [tool] %s failed: %v\n", event.ToolName, event.Err)
				}
			}
		}),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var history []model.Message
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nyou> ")
		if !scanner.Scan() {
			break
		}
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			continue
		}
		if prompt == "exit" || prompt == "quit" {
			break
		}
		result, err := agent.RunStreamWithHistory(ctx, golem.RunContext[struct{}]{},
			history, prompt, func(delta model.Delta) error {
				fmt.Print(delta.Content)
				return nil
			})
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			fmt.Println("\nrun:", err)
			continue
		}
		fmt.Println()
		history = result.Messages
	}
	if err := scanner.Err(); err != nil {
		fmt.Println("stdin:", err)
	}
}

func newModel() (model.StreamingModel, string, error) {
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		name := os.Getenv("OPENAI_MODEL")
		if name == "" {
			name = "gpt-4o-mini"
		}
		client, err := openai.New(openai.Config{APIKey: apiKey, Model: name})
		return client, "OpenAI " + name, err
	}
	if baseURL := os.Getenv("GOLEM_LOCAL_BASE_URL"); baseURL != "" {
		name := os.Getenv("LOCAL_MODEL")
		if name == "" {
			name = "qwen3"
		}
		client, err := openai.New(openai.Config{BaseURL: baseURL, Model: name})
		return client, "local " + name + " at " + baseURL, err
	}
	return testmodel.StreamFunc(offline), "the offline demo model (set OPENAI_API_KEY or GOLEM_LOCAL_BASE_URL for a real one)", nil
}

// offline streams a deterministic answer so the CLI loop runs without a
// provider. It answers tool results directly and echoes user prompts.
func offline(_ context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
	last := request.Messages[len(request.Messages)-1]
	text := "Offline demo: " + last.Content
	if last.Role == model.RoleTool {
		text = "Offline demo, tool " + last.ToolName + " returned:\n" + last.Content
	}
	if err := testmodel.Emit(onDelta, model.Delta{Content: text}); err != nil {
		return model.Response{}, err
	}
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: text}}, nil
}
