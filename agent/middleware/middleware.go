// Package middleware 定义中间件链抽象。
//
// Middleware 在 Turn 级别注入逻辑（BeforeTurn/AfterTurn/BeforeCompact/
// AfterCompact）。中间件链由 LoopAgent 在调度时执行，
// ChatModelAgentMiddleware。
package middleware

import "context"

// Middleware 是 Turn 级中间件接口。
type Middleware interface {
	BeforeTurn(ctx context.Context, turnID string) error
	AfterTurn(ctx context.Context, turnID string) error
	BeforeCompact(ctx context.Context) error
	AfterCompact(ctx context.Context) error
}

// Chain 是中间件链。
type Chain struct {
	middlewares []Middleware
}

// NewChain 构造空链。
func NewChain() *Chain {
	return &Chain{}
}

// Add 追加一个中间件。
func (c *Chain) Add(m Middleware) {
	c.middlewares = append(c.middlewares, m)
}

// BeforeTurn 按顺序执行所有 BeforeTurn。
func (c *Chain) BeforeTurn(ctx context.Context, turnID string) error {
	for _, m := range c.middlewares {
		if err := m.BeforeTurn(ctx, turnID); err != nil {
			return err
		}
	}
	return nil
}

// AfterTurn 按逆序执行所有 AfterTurn。
func (c *Chain) AfterTurn(ctx context.Context, turnID string) error {
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		if err := c.middlewares[i].AfterTurn(ctx, turnID); err != nil {
			return err
		}
	}
	return nil
}

// BeforeCompact 按顺序执行所有 BeforeCompact。
func (c *Chain) BeforeCompact(ctx context.Context) error {
	for _, m := range c.middlewares {
		if err := m.BeforeCompact(ctx); err != nil {
			return err
		}
	}
	return nil
}

// AfterCompact 按逆序执行所有 AfterCompact。
func (c *Chain) AfterCompact(ctx context.Context) error {
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		if err := c.middlewares[i].AfterCompact(ctx); err != nil {
			return err
		}
	}
	return nil
}
