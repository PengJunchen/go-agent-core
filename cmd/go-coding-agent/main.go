// Package main is the go-coding-agent CLI entry point.
//
// It provides two modes:
// - Interactive mode (default): REPL-based conversation with the agent
// - --print mode: single-turn execution for scripting/piping
//
// The agent is assembled via Session facade with all built-in tools
// and a layered System Prompt. Use --resume <session-id> to continue
// a previous session from JSONL log files.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/loop"
	agentsess "github.com/pengjunchen/go-agent-core/agent/session"
	toolreg "github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/capability/tools"
	"github.com/pengjunchen/go-agent-core/config"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/registry"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/prompt"

	// Provider auto-registration via init()
	_ "github.com/pengjunchen/go-agent-core/llm/adapter/eino"
)

func main() {
	// Parse CLI flags.
	var (
		providerName string
		modelName string
		printMode bool
		workspace string
		resumeSessionID string
		logDir string
	)

	flag.StringVar(&providerName, "provider", "", "Provider name (e.g., openai, gemini)")
	flag.StringVar(&modelName, "model", "", "Model name to use (e.g., gpt-4o, claude-sonnet-4-20250514)")
	flag.BoolVar(&printMode, "print", false, "Single-turn mode: print response and exit")
	flag.StringVar(&workspace, "workspace", "", "Project root directory (defaults to cwd)")
	flag.StringVar(&resumeSessionID, "resume", "", "Resume a previous session by its ID (loads history from log directory)")
	flag.StringVar(&logDir, "log-dir", filepath.Join(".", "logs"), "Log directory for session persistence and resume")
	flag.Parse()

	// Determine workspace.
	if workspace == "" {
		if dir, err := os.Getwd(); err == nil {
			workspace = dir
		} else {
			fmt.Fprintln(os.Stderr, "failed to get working directory:", err)
			os.Exit(1)
		}
	}

	// Determine provider and model.
	if providerName == "" {
		providerName = os.Getenv("GO_AGENT_PROVIDER")
	}
	if providerName == "" {
		providerName = config.DefaultProvider // default from config package
	}
	if modelName == "" {
		modelName = os.Getenv("GO_AGENT_MODEL")
	}
	if modelName == "" {
		modelName = config.DefaultModel // default from config package
	}

	// Get the query from remaining args or stdin.
	query := strings.Join(flag.Args(), " ")
	if printMode && query == "" {
		// In print mode, read from stdin if no args.
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			query = scanner.Text()
		}
	}

	// Setup signal handling.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Build the Session.
	sess, err := buildSession(ctx, workspace, providerName, modelName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to build session:", err)
		os.Exit(1)
	}
	defer sess.Close()

	// Build and set the system prompt.
	systemPrompt := prompt.NewBuilder(
		prompt.WithWorkDir(workspace),
		prompt.WithToolRegistry(&toolRegistryAdapter{reg: sess.ToolRegistry()}),
	).Build()

	if err := sess.ContextManager().SetInitialContext(ctx, []ctxpkg.TurnItem{
		{
			Role: "system",
			Content: systemPrompt,
		},
	}); err != nil {
		fmt.Fprintln(os.Stderr, "failed to set system prompt:", err)
		os.Exit(1)
	}

	// Resume a previous session if --resume is provided.
	sessionID := "repl"
	if resumeSessionID != "" {
		resumed, err := agentsess.ResumeSession(ctx, resumeSessionID, logDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed to resume session:", err)
			os.Exit(1)
		}
		if err := resumed.LoadMessagesIntoContext(ctx, sess.ContextManager()); err != nil {
			fmt.Fprintln(os.Stderr, "failed to load resumed messages:", err)
			os.Exit(1)
		}
		sessionID = resumeSessionID
		fmt.Printf("Resumed session %s (%d turns, %d messages)\n\n",
			resumeSessionID, resumed.TurnCount, len(resumed.Messages))
	}

	// Run in the appropriate mode.
	if printMode {
		os.Exit(runPrintMode(ctx, sess, query))
	}
	os.Exit(runInteractiveMode(ctx, sess, sessionID))
}

// buildSession creates a Session with all built-in tools and defaults.
func buildSession(ctx context.Context, workspace, providerName, modelName string) (*agentsess.Session, error) {
	// Create the ModelProvider via the registry.
	p, err := createProvider(providerName, modelName)
	if err != nil {
		return nil, fmt.Errorf("create provider: %w", err)
	}

	// Create and populate the tool registry.
	toolReg := agentsess.NewDefaultToolRegistry()
	if err := tools.RegisterBuiltinTools(ctx, toolReg, workspace); err != nil {
		return nil, fmt.Errorf("register builtin tools: %w", err)
	}

	// Build the Session.
	cm := agentsess.NewDefaultContextManager()
	sess, err := agentsess.NewBuilder().
		WithProvider(p).
		WithContextManager(cm).
		WithToolRegistry(toolReg).
		Build()
	if err != nil {
		return nil, fmt.Errorf("build session: %w", err)
	}

	return sess, nil
}

// createProvider resolves a ModelProvider from the global registry.
func createProvider(providerName, modelName string) (provider.ModelProvider, error) {
	// Check if any providers are registered.
	registered := registry.DefaultRegistry.ListProviders()
	if len(registered) == 0 {
		return nil, fmt.Errorf("no providers registered — import a provider package (e.g., _ \"github.com/pengjunchen/go-agent-core/llm/provider/openai\") to self-register via init()")
	}

	cfg := &registry.ProviderConfig{
		Name: providerName,
		Model: modelName,
		APIKey: os.Getenv("API_KEY"),
	}
	p, err := registry.DefaultRegistry.GetProvider(providerName, cfg)
	if err != nil {
		// List available providers in the error message.
		return nil, fmt.Errorf("provider %q not available (registered: %v): %w", providerName, registered, err)
	}
	return p, nil
}

// runPrintMode executes a single query and prints the response.
func runPrintMode(ctx context.Context, sess *agentsess.Session, query string) int {
	if query == "" {
		fmt.Fprintln(os.Stderr, "no query provided")
		return 1
	}

	eventCh, err := sess.Query(ctx, loop.AgentInput{
		Prompt: query,
		SessionID: "print",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "query error:", err)
		return 1
	}

	// Collect and print text content.
	var textContent strings.Builder
	for evt := range eventCh {
		switch evt.Type {
		case event.EventTextDelta:
			if s, ok := evt.Payload.(string); ok {
				textContent.WriteString(s)
			}
		case event.EventError:
			if evt.Error != nil {
				fmt.Fprintln(os.Stderr, "error:", evt.Error)
			}
		}
	}

	fmt.Print(textContent.String())
	if textContent.Len() > 0 && !strings.HasSuffix(textContent.String(), "\n") {
		fmt.Println()
	}
	return 0
}

// toolRegistryAdapter wraps toolreg.ToolRegistry to implement
// prompt.ToolRegistryReader, extracting ToolGuidelines from the registry.
type toolRegistryAdapter struct {
	reg toolreg.ToolRegistry
}

// ListGuidelines implements prompt.ToolRegistryReader by iterating over
// registered tools and extracting their PromptGuidelines.
func (a *toolRegistryAdapter) ListGuidelines() []prompt.ToolGuideline {
	tools, err := a.reg.ListTools(context.Background())
	if err != nil {
		return nil
	}
	var guidelines []prompt.ToolGuideline
	for _, t := range tools {
		if t.PromptGuidelines != "" {
			guidelines = append(guidelines, prompt.ToolGuideline{
				Name: t.Name,
				Guidelines: t.PromptGuidelines,
			})
		}
	}
	return guidelines
}

// runInteractiveMode runs the REPL loop.
func runInteractiveMode(ctx context.Context, sess *agentsess.Session, sessionID string) int {
	fmt.Println("go-coding-agent — type your query, /help for commands, /quit to exit")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Check for slash commands or legacy :quit/:q.
		if cmd, isCmd := agentsess.ParseSlashCommand(input); isCmd {
			output, shouldExit, err := agentsess.ExecuteSlashCommand(ctx, sess, cmd)
			if err != nil {
				fmt.Fprintln(os.Stderr, "command error:", err)
				continue
			}
			if output != "" {
				fmt.Println(output)
			}
			if shouldExit {
				break
			}
			continue
		}

		// Submit query.
		eventCh, err := sess.Query(ctx, loop.AgentInput{
			Prompt: input,
			SessionID: sessionID,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "query error:", err)
			continue
		}

		// Stream events.
		for evt := range eventCh {
			switch evt.Type {
			case event.EventTextDelta:
				if s, ok := evt.Payload.(string); ok {
					fmt.Print(s)
				}
			case event.EventToolCallStart:
				// Tool calls are handled internally; no special output needed.
			case event.EventToolCallResult:
				// Tool results are processed by the LLM's next response.
			case event.EventError:
				if evt.Error != nil {
					fmt.Fprintln(os.Stderr, "\nerror:", evt.Error)
				}
			case event.EventCompleted:
				fmt.Println() // newline after response
			}
		}
	}

	return 0
}
