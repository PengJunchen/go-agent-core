package compactor

import (
	"fmt"

	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// splitTurn splits overly long tool result TurnItems into multiple smaller items.
//
// When a tool result's Content exceeds maxItemLength, it is split into N parts,
// each prefixed with a "[part N/M]" marker. The split preserves the semantic
// relationship (tool call → tool result) by copying ToolCallID and ToolName
// to each split part. Non-tool items are never split.
func splitTurn(items []memctx.TurnItem, maxItemLength int) []memctx.TurnItem {
	if len(items) == 0 {
		return nil
	}
	if maxItemLength <= 0 {
		return items
	}

	var result []memctx.TurnItem
	for _, item := range items {
		if item.Role != "tool" || len(item.Content) <= maxItemLength {
			result = append(result, item)
			continue
		}

		// Split the tool result content into chunks.
		chunks := splitContent(item.Content, maxItemLength)
		total := len(chunks)
		for i, chunk := range chunks {
			split := memctx.TurnItem{
				Role: item.Role,
				Content: fmt.Sprintf("[part %d/%d] %s", i+1, total, chunk),
				ToolCallID: item.ToolCallID,
				ToolName: item.ToolName,
				Metadata: item.Metadata,
			}
			result = append(result, split)
		}
	}

	return result
}

// splitContent splits a string into chunks of at most maxLen characters.
// It tries to split at newline boundaries when possible.
func splitContent(content string, maxLen int) []string {
	if len(content) <= maxLen {
		return []string{content}
	}

	var chunks []string
	for len(content) > 0 {
		if len(content) <= maxLen {
			chunks = append(chunks, content)
			break
		}

		// Try to split at the last newline before maxLen.
		cut := maxLen
		if idx := lastIndexByteBefore(content, '\n', maxLen); idx > 0 {
			cut = idx + 1 // include the newline in the left part
		}

		chunks = append(chunks, content[:cut])
		content = content[cut:]
	}

	return chunks
}

// lastIndexByteBefore finds the last occurrence of b in s[:limit].
func lastIndexByteBefore(s string, b byte, limit int) int {
	end := limit
	if end > len(s) {
		end = len(s)
	}
	for i := end - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
