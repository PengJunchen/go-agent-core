package extension

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	llmregistry "github.com/pengjunchen/go-agent-core/llm/registry"
)

// mockExtension 是测试用的扩展实现。
type mockExtension struct {
	name string
	priority int
	activateErr error
	deactivateErr error
	activated atomic.Bool
	deactivated atomic.Bool
	onActivate func(ctx context.Context, api *ExtensionAPI) error
	onDeactivate func(ctx context.Context) error
}

func (m *mockExtension) Name() string { return m.name }

func (m *mockExtension) Priority() int { return m.priority }

func (m *mockExtension) Activate(ctx context.Context, api *ExtensionAPI) error {
	m.activated.Store(true)
	if m.onActivate != nil {
		return m.onActivate(ctx, api)
	}
	return m.activateErr
}

func (m *mockExtension) Deactivate(ctx context.Context) error {
	m.deactivated.Store(true)
	if m.onDeactivate != nil {
		return m.onDeactivate(ctx)
	}
	return m.deactivateErr
}

// EX-001: Extension Activate/Deactivate lifecycle
func TestEX001_ExtensionLifecycle(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	ext := &mockExtension{name: "test-ext"}
	runner.Register(ext)

	if err := runner.ActivateAll(context.Background()); err != nil {
		t.Fatalf("ActivateAll failed: %v", err)
	}
	if !ext.activated.Load() {
		t.Error("expected extension to be activated")
	}

	runner.DeactivateAll(context.Background())
	if !ext.deactivated.Load() {
		t.Error("expected extension to be deactivated")
	}
}

// EX-002: EventListener receives events
func TestEX002_EventListenerReceivesEvents(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	var receivedEvent Event
	api.OnEvent(EventAgentStart, func(e Event) *EventResult {
		receivedEvent = e
		return nil
	})

	expected := Event{
		Type: EventAgentStart,
		SessionID: "session-123",
		TurnID: "turn-456",
		Payload: map[string]any{"key": "value"},
	}
	runner.EmitEvent(expected)

	if receivedEvent.Type != expected.Type {
		t.Errorf("expected type %s, got %s", expected.Type, receivedEvent.Type)
	}
	if receivedEvent.SessionID != expected.SessionID {
		t.Errorf("expected sessionID %s, got %s", expected.SessionID, receivedEvent.SessionID)
	}
	if receivedEvent.TurnID != expected.TurnID {
		t.Errorf("expected turnID %s, got %s", expected.TurnID, receivedEvent.TurnID)
	}
}

// EX-003: RegisterTool through ExtensionAPI
func TestEX003_RegisterTool(t *testing.T) {
	reg := registry.NewDefaultToolRegistry()
	api := NewExtensionAPI(reg)

	tool := registry.ToolDefinition{
		Name: "test-tool",
		Description: "a test tool",
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "ok"}, nil
		},
	}

	if err := api.RegisterTool(context.Background(), tool); err != nil {
		t.Fatalf("RegisterTool failed: %v", err)
	}

	got, err := reg.GetTool(context.Background(), "test-tool")
	if err != nil {
		t.Fatalf("GetTool failed: %v", err)
	}
	if got.Name != "test-tool" {
		t.Errorf("expected tool name 'test-tool', got '%s'", got.Name)
	}
}

// EX-004: RegisterCommand and GetCommand
func TestEX004_RegisterCommand(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())

	called := false
	handler := func(_ context.Context, args map[string]any) (any, error) {
		called = true
		return args["echo"], nil
	}

	api.RegisterCommand("test-cmd", handler)

	got, ok := api.GetCommand("test-cmd")
	if !ok {
		t.Fatal("expected command to be found")
	}

	result, err := got(context.Background(), map[string]any{"echo": "hello"})
	if err != nil {
		t.Fatalf("command execution failed: %v", err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %v", result)
	}
	if !called {
		t.Error("expected handler to be called")
	}

	// Test not found
	_, ok = api.GetCommand("nonexistent")
	if ok {
		t.Error("expected nonexistent command to not be found")
	}
}

// EX-005: Multiple extensions can be registered
func TestEX005_MultipleExtensions(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	ext1 := &mockExtension{name: "ext-1"}
	ext2 := &mockExtension{name: "ext-2"}
	ext3 := &mockExtension{name: "ext-3"}

	runner.Register(ext1)
	runner.Register(ext2)
	runner.Register(ext3)

	if err := runner.ActivateAll(context.Background()); err != nil {
		t.Fatalf("ActivateAll failed: %v", err)
	}

	for _, ext := range []*mockExtension{ext1, ext2, ext3} {
		if !ext.activated.Load() {
			t.Errorf("extension %s was not activated", ext.name)
		}
	}

	runner.DeactivateAll(context.Background())

	for _, ext := range []*mockExtension{ext1, ext2, ext3} {
		if !ext.deactivated.Load() {
			t.Errorf("extension %s was not deactivated", ext.name)
		}
	}
}

// EX-006: ExtensionRunner.EmitEvent dispatches to correct listeners
func TestEX006_EmitEventDispatch(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	var startCount, endCount, errorCount atomic.Int32

	api.OnEvent(EventTurnStart, func(_ Event) *EventResult {
		startCount.Add(1)
		return nil
	})
	api.OnEvent(EventTurnEnd, func(_ Event) *EventResult {
		endCount.Add(1)
		return nil
	})
	api.OnEvent(EventError, func(_ Event) *EventResult {
		errorCount.Add(1)
		return nil
	})

	runner.EmitEvent(Event{Type: EventTurnStart})
	runner.EmitEvent(Event{Type: EventTurnStart})
	runner.EmitEvent(Event{Type: EventTurnEnd})
	runner.EmitEvent(Event{Type: EventError})

	if s := startCount.Load(); s != 2 {
		t.Errorf("expected 2 start events, got %d", s)
	}
	if e := endCount.Load(); e != 1 {
		t.Errorf("expected 1 end event, got %d", e)
	}
	if e := errorCount.Load(); e != 1 {
		t.Errorf("expected 1 error event, got %d", e)
	}
}

// EX-007: Panic in listener doesn't crash the runner
func TestEX007_PanicInListener(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	var panicCalled, normalCalled atomic.Bool

	api.OnEvent(EventTurnStart, func(_ Event) *EventResult {
		panicCalled.Store(true)
		panic("boom")
	})
	api.OnEvent(EventTurnStart, func(_ Event) *EventResult {
		normalCalled.Store(true)
		return nil
	})

	// Should not panic
	runner.EmitEvent(Event{Type: EventTurnStart})

	if !panicCalled.Load() {
		t.Error("panicking listener was not called")
	}
	if !normalCalled.Load() {
		t.Error("normal listener was not called after panic")
	}
}

// EX-008: ActivateAll returns error on first failure
func TestEX008_ActivateAllError(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	ext1 := &mockExtension{
		name: "failing-ext",
		activateErr: errors.New("activation failed"),
	}
	ext2 := &mockExtension{name: "normal-ext"}

	runner.Register(ext1)
	runner.Register(ext2)

	err := runner.ActivateAll(context.Background())
	if err == nil {
		t.Fatal("expected error from ActivateAll")
	}

	if !ext1.activated.Load() {
		t.Error("expected first extension to have attempted activation")
	}
	if ext2.activated.Load() {
		t.Error("expected second extension to NOT be activated after first failure")
	}
}

// EX-009: RegisterProvider works when providerReg is set, errors when nil
func TestEX009_RegisterProvider(t *testing.T) {
	// Case 1: providerReg is nil → error
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	factory := func(_ *llmregistry.ProviderConfig) (provider.ModelProvider, error) {
		return nil, nil
	}
	if err := api.RegisterProvider("foo", factory); err == nil {
		t.Fatal("expected error when providerReg is nil")
	}

	// Case 2: providerReg is set via variadic → success
	reg := llmregistry.NewProviderRegistry()
	api2 := NewExtensionAPI(registry.NewDefaultToolRegistry(), reg)
	if err := api2.RegisterProvider("foo", factory); err != nil {
		t.Fatalf("RegisterProvider failed: %v", err)
	}
	if names := reg.ListProviders(); len(names) != 1 || names[0] != "foo" {
		t.Errorf("expected [foo], got %v", names)
	}

	// Case 3: providerReg set via WithProviderRegistry → success
	api3 := NewExtensionAPI(registry.NewDefaultToolRegistry()).WithProviderRegistry(reg)
	if err := api3.RegisterProvider("bar", factory); err != nil {
		t.Fatalf("RegisterProvider failed: %v", err)
	}
	if names := reg.ListProviders(); len(names) != 2 {
		t.Errorf("expected 2 providers, got %d", len(names))
	}
}

// EX-010: EventBlockAction — a listener returning Block causes EmitEvent to return Block
func TestEX010_EventBlockAction(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	var called atomic.Bool
	api.OnEvent(EventToolCallStart, func(_ Event) *EventResult {
		called.Store(true)
		return &EventResult{Action: EventActionBlock, Reason: "blocked by policy"}
	})
	var afterCalled atomic.Bool
	api.OnEvent(EventToolCallStart, func(_ Event) *EventResult {
		afterCalled.Store(true)
		return nil
	})

	result := runner.EmitEvent(Event{Type: EventToolCallStart})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Action != EventActionBlock {
		t.Errorf("expected EventActionBlock, got %v", result.Action)
	}
	if result.Reason != "blocked by policy" {
		t.Errorf("expected reason 'blocked by policy', got '%s'", result.Reason)
	}
	if !called.Load() {
		t.Error("blocking listener was not called")
	}
	if afterCalled.Load() {
		t.Error("subsequent listener should not be called after Block")
	}
}

// EX-011: EventCancelAction — a listener returning Cancel causes EmitEvent to return Cancel
func TestEX011_EventCancelAction(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	api.OnEvent(EventTurnStart, func(_ Event) *EventResult {
		return &EventResult{Action: EventActionCancel, Reason: "cancelled by user"}
	})

	result := runner.EmitEvent(Event{Type: EventTurnStart})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Action != EventActionCancel {
		t.Errorf("expected EventActionCancel, got %v", result.Action)
	}
	if result.Reason != "cancelled by user" {
		t.Errorf("expected reason 'cancelled by user', got '%s'", result.Reason)
	}
}

// EX-013: AC-4 — Event type count >= 15
func TestEX013_EventTypeCount(t *testing.T) {
	allEvents := []EventType{
		EventAgentStart,
		EventAgentStop,
		EventTurnStart,
		EventTurnEnd,
		EventToolCallStart,
		EventToolCallResult,
		EventError,
		EventBeforeProviderRequest,
		EventAfterProviderResponse,
		EventMessageStart,
		EventMessageEnd,
		EventSessionStart,
		EventSessionEnd,
		EventCompactionStart,
		EventCompactionEnd,
	}
	if got := len(allEvents); got < 15 {
		t.Errorf("expected at least 15 event types, got %d", got)
	}
	// Verify no duplicates
	seen := make(map[EventType]string, len(allEvents))
	for _, e := range allEvents {
		if prev, dup := seen[e]; dup {
			t.Errorf("duplicate event type %q (also defined as %q)", e, prev)
		}
		seen[e] = string(e)
	}
}

// EX-014: AC-5 — Priority sorting: multiple extensions subscribing to same event
// are called in priority order (highest first)
func TestEX014_PrioritySorting(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	var callOrder []string

	makeExt := func(name string, prio int) *mockExtension {
		return &mockExtension{
			name: name,
			priority: prio,
			onActivate: func(_ context.Context, a *ExtensionAPI) error {
				a.OnEvent(EventTurnStart, func(_ Event) *EventResult {
					callOrder = append(callOrder, name)
					return nil
				})
				return nil
			},
		}
	}

	runner.Register(makeExt("low", 1))
	runner.Register(makeExt("high", 10))
	runner.Register(makeExt("medium", 5))

	if err := runner.ActivateAll(context.Background()); err != nil {
		t.Fatalf("ActivateAll failed: %v", err)
	}

	runner.EmitEvent(Event{Type: EventTurnStart})

	expected := []string{"high", "medium", "low"}
	if !reflect.DeepEqual(callOrder, expected) {
		t.Errorf("expected call order %v, got %v", expected, callOrder)
	}
}

// EX-015: BaseExtension provides default Priority() and Name()
func TestEX015_BaseExtensionDefaults(t *testing.T) {
	b := BaseExtension{}
	if b.Priority() != 0 {
		t.Errorf("expected default priority 0, got %d", b.Priority())
	}
	if b.Name() != "" {
		t.Errorf("expected default name '', got %q", b.Name())
	}
}

// EX-012: EventReplaceAction — a listener returning Replace causes EmitEvent to return Replace
func TestEX012_EventReplaceAction(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	api.OnEvent(EventToolCallResult, func(_ Event) *EventResult {
		return &EventResult{
			Action: EventActionReplace,
			Reason: "replaced payload",
			Replace: map[string]any{"modified": true},
		}
	})

	result := runner.EmitEvent(Event{Type: EventToolCallResult})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Action != EventActionReplace {
		t.Errorf("expected EventActionReplace, got %v", result.Action)
	}
	replace, ok := result.Replace.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result.Replace)
	}
	if replace["modified"] != true {
		t.Errorf("expected modified=true, got %v", replace["modified"])
	}

	// Verify last Replace wins when multiple listeners return Replace
	api2 := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner2 := NewExtensionRunner(api2)
	api2.OnEvent(EventToolCallResult, func(_ Event) *EventResult {
		return &EventResult{Action: EventActionReplace, Replace: "first"}
	})
	api2.OnEvent(EventToolCallResult, func(_ Event) *EventResult {
		return &EventResult{Action: EventActionReplace, Replace: "second"}
	})
	result2 := runner2.EmitEvent(Event{Type: EventToolCallResult})
	if result2 == nil || result2.Replace != "second" {
		t.Errorf("expected last replace 'second', got %v", result2)
	}
}

// EX-016: AC-2 — Init via Factory pattern
func TestEX016_InitViaFactory(t *testing.T) {
	factoryName := "test-init-factory-ex016"
	RegisterFactory(factoryName, func() Extension {
		return &mockExtension{name: "factory-created"}
	})

	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	// Look up and call factory to create extension
	f, ok := LookupFactory(factoryName)
	if !ok {
		t.Fatal("factory not found after RegisterFactory")
	}
	ext := f()
	runner.Register(ext)

	if err := runner.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	me, ok := ext.(*mockExtension)
	if !ok {
		t.Fatalf("expected *mockExtension, got %T", ext)
	}
	if !me.activated.Load() {
		t.Error("expected extension to be activated after Init")
	}
}

// EX-017: AC-2 — Init returns error on first activation failure
func TestEX017_InitError(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	ext1 := &mockExtension{name: "failing", activateErr: errors.New("boom")}
	ext2 := &mockExtension{name: "ok"}

	runner.Register(ext1)
	runner.Register(ext2)

	err := runner.Init(context.Background())
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !ext1.activated.Load() {
		t.Error("expected first extension to have attempted activation")
	}
	if ext2.activated.Load() {
		t.Error("expected second extension to NOT be activated after first failure")
	}
}

// EX-018: AC-3 — Shutdown graceful close (reverse priority order)
func TestEX018_ShutdownGraceful(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	var order []string
	ext1 := &mockExtension{
		name: "high-prio",
		priority: 10,
		onDeactivate: func(_ context.Context) error {
			order = append(order, "high-prio")
			return nil
		},
	}
	ext2 := &mockExtension{
		name: "low-prio",
		priority: 1,
		onDeactivate: func(_ context.Context) error {
			order = append(order, "low-prio")
			return nil
		},
	}

	runner.Register(ext1)
	runner.Register(ext2)

	if err := runner.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	// Deactivate in reverse priority: low-prio first, then high-prio
	expected := []string{"low-prio", "high-prio"}
	if !reflect.DeepEqual(order, expected) {
		t.Errorf("expected deactivation order %v, got %v", expected, order)
	}
	if !ext1.deactivated.Load() || !ext2.deactivated.Load() {
		t.Error("expected all extensions to be deactivated")
	}
}

// EX-019: AC-3 — Shutdown aggregates deactivation errors
func TestEX019_ShutdownErrorAggregation(t *testing.T) {
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	ext1 := &mockExtension{name: "err1", deactivateErr: errors.New("deactivate err 1")}
	ext2 := &mockExtension{name: "err2", deactivateErr: errors.New("deactivate err 2")}

	runner.Register(ext1)
	runner.Register(ext2)

	_ = runner.Init(context.Background())

	err := runner.Shutdown(context.Background())
	if err == nil {
		t.Fatal("expected aggregated error from Shutdown")
	}
	// Both errors should be mentioned
	if !strings.Contains(err.Error(), "deactivate err 1") {
		t.Errorf("error should contain 'deactivate err 1', got: %v", err)
	}
	if !strings.Contains(err.Error(), "deactivate err 2") {
		t.Errorf("error should contain 'deactivate err 2', got: %v", err)
	}
}

// EX-020: AC-1 — Load from directory (manifest-based factory loading)
func TestEX020_LoadFromDirectory(t *testing.T) {
	var activated atomic.Bool
	factoryName := "test-load-factory-ex020"
	RegisterFactory(factoryName, func() Extension {
		return &mockExtension{
			name: "loaded-ext",
			onActivate: func(_ context.Context, _ *ExtensionAPI) error {
				activated.Store(true)
				return nil
			},
		}
	})

	dir := t.TempDir()
	manifest := `{"extensions":[{"name":"loaded-ext","factory":"test-load-factory-ex020"}]}`
	manifestPath := filepath.Join(dir, "extensions.json")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	if err := runner.Load(context.Background(), dir); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if err := runner.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if !activated.Load() {
		t.Error("expected loaded extension to be activated")
	}
}

// EX-021: AC-1 — Load returns error when manifest file is missing
func TestEX021_LoadMissingManifest(t *testing.T) {
	dir := t.TempDir()
	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	err := runner.Load(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error when manifest is missing")
	}
}

// EX-022: AC-1 — Load returns error when factory is not registered
func TestEX022_LoadUnknownFactory(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"extensions":[{"name":"ext","factory":"nonexistent-factory-ex022"}]}`
	manifestPath := filepath.Join(dir, "extensions.json")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	api := NewExtensionAPI(registry.NewDefaultToolRegistry())
	runner := NewExtensionRunner(api)

	err := runner.Load(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error for unknown factory")
	}
}

// EX-023: AC-6 — SCAN-011 compliance: all events go through EmitEvent pipeline
// New event types must follow the same Block/Cancel/Replace and priority pipeline.
func TestEX023_Scan011Compliance(t *testing.T) {
	// Test that new event types work through EmitEvent with Block/Cancel/Replace
	t.Run("block_on_new_event_type", func(t *testing.T) {
		api2 := NewExtensionAPI(registry.NewDefaultToolRegistry())
		runner2 := NewExtensionRunner(api2)

		var called atomic.Bool
		api2.OnEvent(EventBeforeProviderRequest, func(_ Event) *EventResult {
			called.Store(true)
			return &EventResult{Action: EventActionBlock, Reason: "policy"}
		})
		var afterCalled atomic.Bool
		api2.OnEvent(EventBeforeProviderRequest, func(_ Event) *EventResult {
			afterCalled.Store(true)
			return nil
		})

		result := runner2.EmitEvent(Event{Type: EventBeforeProviderRequest})
		if result == nil || result.Action != EventActionBlock {
			t.Errorf("expected Block, got %v", result)
		}
		if !called.Load() {
			t.Error("blocking listener was not called")
		}
		if afterCalled.Load() {
			t.Error("subsequent listener should not be called after Block")
		}
	})

	t.Run("cancel_on_new_event_type", func(t *testing.T) {
		api3 := NewExtensionAPI(registry.NewDefaultToolRegistry())
		runner3 := NewExtensionRunner(api3)

		api3.OnEvent(EventAfterProviderResponse, func(_ Event) *EventResult {
			return &EventResult{Action: EventActionCancel, Reason: "user cancel"}
		})

		result := runner3.EmitEvent(Event{Type: EventAfterProviderResponse})
		if result == nil || result.Action != EventActionCancel {
			t.Errorf("expected Cancel, got %v", result)
		}
	})

	t.Run("replace_on_new_event_type", func(t *testing.T) {
		api4 := NewExtensionAPI(registry.NewDefaultToolRegistry())
		runner4 := NewExtensionRunner(api4)

		api4.OnEvent(EventSessionStart, func(_ Event) *EventResult {
			return &EventResult{Action: EventActionReplace, Replace: "new-session"}
		})

		result := runner4.EmitEvent(Event{Type: EventSessionStart})
		if result == nil || result.Action != EventActionReplace || result.Replace != "new-session" {
			t.Errorf("expected Replace with 'new-session', got %v", result)
		}
	})

	t.Run("priority_sorting_on_new_event_type", func(t *testing.T) {
		api5 := NewExtensionAPI(registry.NewDefaultToolRegistry())
		runner5 := NewExtensionRunner(api5)

		var order []string
		ext1 := &mockExtension{
			name: "low-prio", priority: 1,
			onActivate: func(_ context.Context, a *ExtensionAPI) error {
				a.OnEvent(EventCompactionStart, func(_ Event) *EventResult {
					order = append(order, "low-prio")
					return nil
				})
				return nil
			},
		}
		ext2 := &mockExtension{
			name: "high-prio", priority: 100,
			onActivate: func(_ context.Context, a *ExtensionAPI) error {
				a.OnEvent(EventCompactionStart, func(_ Event) *EventResult {
					order = append(order, "high-prio")
					return nil
				})
				return nil
			},
		}

		runner5.Register(ext1)
		runner5.Register(ext2)
		_ = runner5.Init(context.Background())

		runner5.EmitEvent(Event{Type: EventCompactionStart})

		expected := []string{"high-prio", "low-prio"}
		if !reflect.DeepEqual(order, expected) {
			t.Errorf("expected %v, got %v", expected, order)
		}
	})
}
