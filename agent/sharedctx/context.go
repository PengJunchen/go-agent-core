package sharedctx

import "context"

type contextKey struct{}

// WithSharedContext 将 SharedContext 附加到 context.Context。
func WithSharedContext(ctx context.Context, sc *SharedContext) context.Context {
	return context.WithValue(ctx, contextKey{}, sc)
}

// FromContext 从 context.Context 中检索 SharedContext。
func FromContext(ctx context.Context) (*SharedContext, bool) {
	sc, ok := ctx.Value(contextKey{}).(*SharedContext)
	return sc, ok
}

// MustFromContext 从 context.Context 中检索 SharedContext，不存在则 panic。
func MustFromContext(ctx context.Context) *SharedContext {
	sc, ok := FromContext(ctx)
	if !ok {
		panic("sharedctx: SharedContext not found in context")
	}
	return sc
}
