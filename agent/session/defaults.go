package session

import (
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/memory/compactor"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
)

// NewDefaultContextManager creates a HeuristicContextManager with
// a TruncatingCompactor and HeuristicTokenEstimator for auto-compaction.
// This is the zero-configuration default for Session.
func NewDefaultContextManager() ctxpkg.ContextManager {
	estimator := compactor.NewHeuristicTokenEstimator()
	truncator := compactor.TruncatingCompactor{Estimator: estimator}
	return ctxpkg.NewHeuristicContextManager(
		ctxpkg.WithEstimator(estimator),
		ctxpkg.WithTruncatingCompactor(truncator),
		ctxpkg.WithMaxTokens(8192),
	)
}

// NewDefaultToolRegistry creates a DefaultToolRegistry.
// This is the zero-configuration default for Session.
func NewDefaultToolRegistry() registry.ToolRegistry {
	return registry.NewDefaultToolRegistry()
}
