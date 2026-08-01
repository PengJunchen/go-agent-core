package production

import (
	"context"
	"testing"
)

func TestConfigSecurityGuard_Interface(t *testing.T) {
	var _ SecurityGuard = (*ConfigSecurityGuard)(nil)
}

func TestConfigSecurityGuard_AllowAll(t *testing.T) {
	g := NewConfigSecurityGuard(SecurityGuardConfig{})
	dec, err := g.ValidateToolCall(context.Background(), SecurityCallInfo{ToolName: "any_tool"})
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if !dec.Allowed {
		t.Error("should allow all when no restrictions")
	}
}

func TestConfigSecurityGuard_Whitelist(t *testing.T) {
	g := NewConfigSecurityGuard(SecurityGuardConfig{
		AllowedTools: map[string]bool{"search": true, "read": true},
	})
	dec, _ := g.ValidateToolCall(context.Background(), SecurityCallInfo{ToolName: "search"})
	if !dec.Allowed {
		t.Error("search should be allowed")
	}
	dec, _ = g.ValidateToolCall(context.Background(), SecurityCallInfo{ToolName: "delete"})
	if dec.Allowed {
		t.Error("delete should be blocked")
	}
	if dec.Action != SecurityBlock {
		t.Error("action should be SecurityBlock")
	}
}

func TestConfigSecurityGuard_Blacklist(t *testing.T) {
	g := NewConfigSecurityGuard(SecurityGuardConfig{
		BlockedTools: map[string]bool{"delete": true, "exec": true},
	})
	dec, _ := g.ValidateToolCall(context.Background(), SecurityCallInfo{ToolName: "search"})
	if !dec.Allowed {
		t.Error("search should be allowed")
	}
	dec, _ = g.ValidateToolCall(context.Background(), SecurityCallInfo{ToolName: "delete"})
	if dec.Allowed {
		t.Error("delete should be blocked")
	}
}

func TestConfigSecurityGuard_WhitelistPrecedence(t *testing.T) {
	g := NewConfigSecurityGuard(SecurityGuardConfig{
		AllowedTools: map[string]bool{"search": true},
		BlockedTools: map[string]bool{"search": true}, // should be ignored
	})
	dec, _ := g.ValidateToolCall(context.Background(), SecurityCallInfo{ToolName: "search"})
	if !dec.Allowed {
		t.Error("whitelist should take precedence")
	}
}

func TestConfigSecurityGuard_BlockMessage(t *testing.T) {
	g := NewConfigSecurityGuard(SecurityGuardConfig{
		BlockedTools: map[string]bool{"exec": true},
		BlockMessage: "custom blocked",
	})
	dec, _ := g.ValidateToolCall(context.Background(), SecurityCallInfo{ToolName: "exec"})
	if dec.Reason != "custom blocked" {
		t.Errorf("reason = %q, want custom blocked", dec.Reason)
	}
}

func TestConfigSecurityGuard_ValidateInput(t *testing.T) {
	g := NewConfigSecurityGuard(SecurityGuardConfig{})
	err := g.ValidateInput(context.Background(), "any input")
	if err != nil {
		t.Errorf("ValidateInput should always return nil, got %v", err)
	}
}
