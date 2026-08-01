package production

import (
	"context"
	"fmt"
)

// SecurityGuardConfig configures which tools are allowed or blocked.
type SecurityGuardConfig struct {
	// AllowedTools is a set of tool names that are allowed.
	// If non-empty, only these tools are allowed (whitelist mode).
	AllowedTools map[string]bool

	// BlockedTools is a set of tool names that are blocked.
	// If non-empty, these tools are blocked (blacklist mode).
	// AllowedTools takes precedence over BlockedTools.
	BlockedTools map[string]bool

	// BlockMessage is the message returned when a tool is blocked.
	BlockMessage string
}

// ConfigSecurityGuard validates tool calls based on allow/block lists.
type ConfigSecurityGuard struct {
	config SecurityGuardConfig
}

// NewConfigSecurityGuard creates a guard with the given config.
func NewConfigSecurityGuard(config SecurityGuardConfig) *ConfigSecurityGuard {
	return &ConfigSecurityGuard{config: config}
}

func (g *ConfigSecurityGuard) ValidateToolCall(_ context.Context, call SecurityCallInfo) (*SecurityDecision, error) {
	// Whitelist mode: only allowed tools pass
	if len(g.config.AllowedTools) > 0 {
		if g.config.AllowedTools[call.ToolName] {
			return &SecurityDecision{Allowed: true, Action: SecurityAllow}, nil
		}
		reason := fmt.Sprintf("tool %q not in allowed list", call.ToolName)
		if g.config.BlockMessage != "" {
			reason = g.config.BlockMessage
		}
		return &SecurityDecision{Allowed: false, Reason: reason, Action: SecurityBlock}, nil
	}

	// Blacklist mode: blocked tools are rejected
	if len(g.config.BlockedTools) > 0 {
		if g.config.BlockedTools[call.ToolName] {
			reason := fmt.Sprintf("tool %q is blocked", call.ToolName)
			if g.config.BlockMessage != "" {
				reason = g.config.BlockMessage
			}
			return &SecurityDecision{Allowed: false, Reason: reason, Action: SecurityBlock}, nil
		}
		return &SecurityDecision{Allowed: true, Action: SecurityAllow}, nil
	}

	// No restrictions: allow all
	return &SecurityDecision{Allowed: true, Action: SecurityAllow}, nil
}

func (g *ConfigSecurityGuard) ValidateInput(_ context.Context, input string) error {
	// Config-based guard does not validate input content
	return nil
}
