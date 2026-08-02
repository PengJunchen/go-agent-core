package compactor

import (
	"context"
	"fmt"

	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// incrementalSummaryPrompt 是增量摘要更新的提示词模板。
const incrementalSummaryPrompt = `You are a conversation summarizer. Your task is to update an existing summary with new events.

Here is the existing summary:
%s

Here are the new events:
%s

Please update the summary to include the new events while preserving the key information from the existing summary.

Guidelines:
1. Retain all system instructions, user goals, and constraints from the existing summary
2. Keep a record of all tool calls made and their results from the new events
3. Preserve any decisions, conclusions, or outputs produced
4. Note any files created, modified, or read
5. Include error messages and failures if any occurred
6. Be concise but complete
7. Output only the updated summary text, no preamble or explanation

Updated Summary:`

// UpdateSummary incrementally updates an existing summary with new items,
// instead of regenerating the full summary from scratch.
//
// If newItems is empty, the existing summary is returned unchanged without
// calling the LLM. If existingSummary is empty, a fresh summary is generated
// from newItems using the standard summary prompt.
func (sc *SummaryCompactor) UpdateSummary(ctx context.Context, existingSummary string, newItems []memctx.TurnItem) (string, error) {
	if len(newItems) == 0 {
		return existingSummary, nil
	}

	if ctx.Err() != nil {
		return "", fmt.Errorf("update summary: %w", ctx.Err())
	}

	// Split overly long tool results before formatting.
	newItems = splitTurn(newItems, sc.maxItemLength)

	newEventsText := formatConversation(newItems)

	var prompt string
	if existingSummary == "" {
		// No existing summary — use the standard summary prompt.
		prompt = sc.buildPrompt(newEventsText, extractFileOps(newItems))
	} else {
		prompt = fmt.Sprintf(incrementalSummaryPrompt, existingSummary, newEventsText)
	}

	return sc.callLLM(ctx, prompt)
}
