package provider

import (
	"context"
	"sync"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// LazyProvider wraps a ModelProvider factory with sync.Once initialization.
// The underlying provider is only created on first use.
type LazyProvider struct {
	once sync.Once
	factory func() (ModelProvider, error)
	provider ModelProvider
	err error
}

// NewLazyProvider constructs a LazyProvider from the given factory.
func NewLazyProvider(factory func() (ModelProvider, error)) *LazyProvider {
	return &LazyProvider{factory: factory}
}

func (p *LazyProvider) init() error {
	p.once.Do(func() {
		p.provider, p.err = p.factory()
	})
	return p.err
}

// StreamChat lazily initializes the underlying provider and delegates.
func (p *LazyProvider) StreamChat(ctx context.Context, msgs []message.Message, opts *ChatOptions) (<-chan stream.StreamEvent, error) {
	if err := p.init(); err != nil {
		return nil, err
	}
	return p.provider.StreamChat(ctx, msgs, opts)
}

// Generate lazily initializes the underlying provider and delegates.
func (p *LazyProvider) Generate(ctx context.Context, msgs []message.Message, opts *ChatOptions) (*message.Message, error) {
	if err := p.init(); err != nil {
		return nil, err
	}
	return p.provider.Generate(ctx, msgs, opts)
}

// ModelInfo lazily initializes the underlying provider and returns its info.
// If initialization fails, returns a placeholder ModelInfo.
func (p *LazyProvider) ModelInfo() *ModelInfo {
	if err := p.init(); err != nil {
		return &ModelInfo{Provider: "lazy", ModelName: "uninitialized"}
	}
	return p.provider.ModelInfo()
}
