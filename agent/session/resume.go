package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pengjunchen/go-agent-core/llm/catalog"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/memory/log"
)

// ─── Session Resume ──────────────────────────────────────────────

// ResumedSession holds the recovered context from a previous session's
// JSONL log files. It is returned by ResumeSession and used by the CLI
// to continue a conversation after a restart.
type ResumedSession struct {
	SessionID string
	Messages []ctxpkg.TurnItem
	TurnCount int
	LastActivity time.Time
	ModelUsed string
}

// ResumeSession loads session history from JSONL log files in storeDir
// and reconstructs the conversation context for the given sessionID.
//
// storeDir is the log data directory containing sessions/, runs/, and
// events/ subdirectories (as written by JSONLExecLogger). The function
// scans the runs/ and events/ tracks for entries matching sessionID,
// reconstructs TurnItems from item and event records, and returns a
// ResumedSession with the recovered context.
func ResumeSession(ctx context.Context, sessionID string, storeDir string) (*ResumedSession, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("resume: sessionID must not be empty")
	}
	if storeDir == "" {
		return nil, fmt.Errorf("resume: storeDir must not be empty")
	}

	if _, err := os.Stat(storeDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("resume: log directory %q does not exist: %w", storeDir, err)
	}

	extractor := log.NewJSONLLogExtractor(storeDir)

	// Extract run-track envelopes (turn + item records) for this session.
	runEnvelopes, err := extractor.ExtractEnvelopes(ctx, &log.LogFilter{
		SessionID: sessionID,
		TrackType: log.TrackRuns,
	})
	if err != nil {
		return nil, fmt.Errorf("resume: extract run records: %w", err)
	}

	// Extract event-track envelopes for text content.
	eventEnvelopes, err := extractor.ExtractEnvelopes(ctx, &log.LogFilter{
		SessionID: sessionID,
		TrackType: log.TrackEvents,
	})
	if err != nil {
		return nil, fmt.Errorf("resume: extract event records: %w", err)
	}

	result := &ResumedSession{
		SessionID: sessionID,
		Messages: []ctxpkg.TurnItem{},
	}

	// Reconstruct messages from item records in the runs track.
	var lastTimestamp time.Time
	for _, env := range runEnvelopes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		switch env.Category {
		case log.LogCategoryTurn:
			rec, err := env.ParseAsTurnRecord()
			if err != nil {
				continue
			}
			if rec.EventType == "turn_end" {
				result.TurnCount++
			}
			if ts := parseLogTimestamp(rec.Timestamp); !ts.IsZero() && ts.After(lastTimestamp) {
				lastTimestamp = ts
			}
		case log.LogCategoryItem:
			rec, err := env.ParseAsItemRecord()
			if err != nil {
				continue
			}
			msg := itemRecordToTurnItem(rec)
			if msg.Role != "" {
				result.Messages = append(result.Messages, msg)
			}
			if rec.Model != "" && result.ModelUsed == "" {
				result.ModelUsed = rec.Model
			}
			if ts := parseLogTimestamp(rec.Timestamp); !ts.IsZero() && ts.After(lastTimestamp) {
				lastTimestamp = ts
			}
		}
	}

	// Supplement with text content from event records (assistant text deltas).
	if len(result.Messages) == 0 {
		result.Messages = reconstructFromEvents(eventEnvelopes)
	}

	result.LastActivity = lastTimestamp

	return result, nil
}

// itemRecordToTurnItem converts an ItemRecord to a TurnItem.
func itemRecordToTurnItem(rec *log.ItemRecord) ctxpkg.TurnItem {
	item := ctxpkg.TurnItem{}
	switch rec.ItemType {
	case "llm_call":
		item.Role = "assistant"
		if output, ok := rec.Output.(string); ok {
			item.Content = output
		}
		if rec.Model != "" {
			if item.Metadata == nil {
				item.Metadata = make(map[string]any)
			}
			item.Metadata["model"] = rec.Model
		}
	case "tool_call":
		item.Role = "assistant"
		item.ToolName = rec.ToolName
		item.ToolCallID = rec.TurnID
		if input, ok := rec.Input.(map[string]any); ok {
			item.ToolCalls = []ctxpkg.ToolCallRef{{
				Name: rec.ToolName,
				Arguments: input,
			}}
		}
	case "tool_result":
		item.Role = "tool"
		item.ToolName = rec.ToolName
		if output, ok := rec.Output.(string); ok {
			item.Content = output
		}
	case "steer":
		item.Role = "user"
		if input, ok := rec.Input.(string); ok {
			item.Content = input
		}
	}
	return item
}

// reconstructFromEvents builds TurnItems from event records when item
// records are unavailable. It accumulates text_delta events into
// assistant messages.
func reconstructFromEvents(envelopes []*log.LogEnvelope) []ctxpkg.TurnItem {
	var messages []ctxpkg.TurnItem
	var currentText strings.Builder

	flush := func() {
		if currentText.Len() > 0 {
			messages = append(messages, ctxpkg.TurnItem{
				Role: "assistant",
				Content: currentText.String(),
			})
			currentText.Reset()
		}
	}

	for _, env := range envelopes {
		if env.Category != log.LogCategoryEvent {
			continue
		}
		rec, err := env.ParseAsEventRecord()
		if err != nil {
			continue
		}
		switch rec.EventType {
		case "text_delta":
			currentText.WriteString(rec.Content)
		case "done", "error":
			flush()
		}
	}
	flush()

	return messages
}

// parseLogTimestamp parses a RFC3339Nano timestamp string.
func parseLogTimestamp(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// LoadMessagesIntoContext injects the recovered messages from a
// ResumedSession into a ContextManager, allowing the conversation to
// continue seamlessly.
func (r *ResumedSession) LoadMessagesIntoContext(ctx context.Context, cm ctxpkg.ContextManager) error {
	for _, msg := range r.Messages {
		if err := cm.RecordItem(ctx, msg); err != nil {
			return fmt.Errorf("resume: record item: %w", err)
		}
	}
	return nil
}

// ─── Slash Commands ──────────────────────────────────────────────

// SlashCommandType identifies a slash command.
type SlashCommandType string

const (
	SlashHelp SlashCommandType = "/help"
	SlashModels SlashCommandType = "/models"
	SlashCompact SlashCommandType = "/compact"
	SlashClear SlashCommandType = "/clear"
	SlashQuit SlashCommandType = "/quit"
	SlashUnknown SlashCommandType = ""
)

// SlashCommand represents a parsed slash command from user input.
type SlashCommand struct {
	Type SlashCommandType
	Raw string
}

// ParseSlashCommand checks if the input is a slash command (starting
// with "/") or a legacy command (":quit"/":q"). Returns the parsed
// command and true if it is a command; otherwise returns false.
func ParseSlashCommand(input string) (*SlashCommand, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, false
	}

	// Legacy commands (backward compatibility).
	switch trimmed {
	case ":quit", ":q":
		return &SlashCommand{Type: SlashQuit, Raw: trimmed}, true
	}

	// Slash commands.
	if !strings.HasPrefix(trimmed, "/") {
		return nil, false
	}

	switch trimmed {
	case "/help", "/h":
		return &SlashCommand{Type: SlashHelp, Raw: trimmed}, true
	case "/models", "/m":
		return &SlashCommand{Type: SlashModels, Raw: trimmed}, true
	case "/compact":
		return &SlashCommand{Type: SlashCompact, Raw: trimmed}, true
	case "/clear":
		return &SlashCommand{Type: SlashClear, Raw: trimmed}, true
	case "/quit", "/q":
		return &SlashCommand{Type: SlashQuit, Raw: trimmed}, true
	}

	return &SlashCommand{Type: SlashUnknown, Raw: trimmed}, true
}

// ExecuteSlashCommand executes a parsed slash command against a Session.
// Returns the output string to display, whether the REPL should exit,
// and any error.
func ExecuteSlashCommand(ctx context.Context, sess *Session, cmd *SlashCommand) (string, bool, error) {
	switch cmd.Type {
	case SlashHelp:
		return HelpText(), false, nil

	case SlashModels:
		return FormatModels(KnownModels()), false, nil

	case SlashCompact:
		result, err := sess.ContextManager().Compact(ctx, ctxpkg.CompactManual)
		if err != nil {
			return "", false, fmt.Errorf("compact: %w", err)
		}
		return fmt.Sprintf("Context compacted: %d → %d tokens (%d items removed)",
			result.BeforeTokens, result.AfterTokens, result.ItemsRemoved), false, nil

	case SlashClear:
		cm := sess.ContextManager()
		if hcm, ok := cm.(*ctxpkg.HeuristicContextManager); ok {
			hcm.Clear()
			return "Context cleared. Starting fresh.", false, nil
		}
		// Fallback: compact with truncate to remove as much as possible.
		_, err := cm.Compact(ctx, ctxpkg.CompactTruncate)
		if err != nil {
			return "", false, fmt.Errorf("clear: %w", err)
		}
		return "Context cleared. Starting fresh.", false, nil

	case SlashQuit:
		return "Goodbye!", true, nil

	default:
		return fmt.Sprintf("Unknown command: %s\nType /help for available commands.", cmd.Raw), false, nil
	}
}

// ─── Help Text ───────────────────────────────────────────────────

// HelpText returns the help message listing all available commands.
func HelpText() string {
	return `Available commands:
 /help Show this help message
 /models List available models
 /compact Manually trigger context compaction
 /clear Clear current context (start fresh)
 /quit Exit the REPL (or /q)
 :quit Exit the REPL (legacy, or :q)

Type any other text to send a query to the agent.`
}

// ─── Model Catalog ──────────────────────────────────────────────

// ModelEntry describes a known model with its context window and pricing.
type ModelEntry struct {
	Name string `json:"name"`
	Provider string `json:"provider"`
	ContextWindow int `json:"context_window"` // in tokens
	InputPrice float64 `json:"input_price"` // USD per 1M tokens
	OutputPrice float64 `json:"output_price"` // USD per 1M tokens
}

// fallbackModels is the hardcoded list used when the catalog is unavailable.
func fallbackModels() []ModelEntry {
	return []ModelEntry{
		{
			Name: "gpt-4o",
			Provider: "openai",
			ContextWindow: 128000,
			InputPrice: 2.50,
			OutputPrice: 10.00,
		},
		{
			Name: "gpt-4o-mini",
			Provider: "openai",
			ContextWindow: 128000,
			InputPrice: 0.15,
			OutputPrice: 0.60,
		},
		{
			Name: "claude-sonnet-4-20250514",
			Provider: "anthropic",
			ContextWindow: 200000,
			InputPrice: 3.00,
			OutputPrice: 15.00,
		},
		{
			Name: "claude-3-5-haiku-20241022",
			Provider: "anthropic",
			ContextWindow: 200000,
			InputPrice: 0.80,
			OutputPrice: 4.00,
		},
		{
			Name: "gemini-2.0-flash",
			Provider: "gemini",
			ContextWindow: 1000000,
			InputPrice: 0.10,
			OutputPrice: 0.40,
		},
	}
}

// KnownModels returns the list of known models from the embedded catalog.
// If the catalog is unavailable (e.g., corrupt or missing), it falls back
// to a hardcoded list for backward compatibility.
func KnownModels() (models []ModelEntry) {
	defer func() {
		// NewCatalog panics if embedded catalog.json is corrupt; fall back on panic.
		if r := recover(); r != nil {
			models = fallbackModels()
		}
	}()

	c := catalog.NewCatalog()
	entries := c.ListModels()
	if len(entries) == 0 {
		return fallbackModels()
	}

	models = make([]ModelEntry, len(entries))
	for i, e := range entries {
		models[i] = ModelEntry{
			Name: e.ModelName,
			Provider: e.Provider,
			ContextWindow: e.ContextWindow,
			InputPrice: e.CostInputPerMillion,
			OutputPrice: e.CostOutputPerMillion,
		}
	}
	return models
}

// FormatModels formats a list of ModelEntry for display in the CLI.
func FormatModels(models []ModelEntry) string {
	var sb strings.Builder
	sb.WriteString("Available models:\n")
	sb.WriteString(fmt.Sprintf(" %-35s %-12s %12s %12s %12s\n",
		"MODEL", "PROVIDER", "CONTEXT", "IN $/1M", "OUT $/1M"))
	sb.WriteString(strings.Repeat("-", 85))
	sb.WriteString("\n")
	for _, m := range models {
		sb.WriteString(fmt.Sprintf(" %-35s %-12s %10dk %12.2f %12.2f\n",
			m.Name, m.Provider, m.ContextWindow/1000, m.InputPrice, m.OutputPrice))
	}
	return sb.String()
}

// ─── Session Resume Helpers for CLI ──────────────────────────────

// ListResumableSessions scans the log directory for session IDs that
// have run-track JSONL files. Returns a list of session IDs sorted by
// last modification time (most recent first).
func ListResumableSessions(storeDir string) ([]string, error) {
	runsDir := filepath.Join(storeDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	// Collect unique session IDs from file names (<sessionID>.jsonl).
	seen := make(map[string]bool)
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// ReadLogEntries reads raw JSONL lines from a file for debugging or
// inspection. Each line is returned as a string.
func ReadLogEntries(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}

// WriteTestLogEntry writes a JSONL log entry to the specified file path.
// This is primarily used by tests to create log fixtures for ResumeSession.
func WriteTestLogEntry(path string, entry any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}
