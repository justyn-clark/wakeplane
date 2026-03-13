package app

import (
	"context"

	"github.com/justyn-clark/wakeplane/internal/executors"
)

type Option func(*options)

type options struct {
	workflowRegistry *executors.WorkflowRegistry
}

func WithWorkflowRegistry(registry *executors.WorkflowRegistry) Option {
	return func(opts *options) {
		opts.workflowRegistry = registry
	}
}

func WithWorkflowHandler(id string, handler executors.WorkflowHandler) Option {
	return func(opts *options) {
		if opts.workflowRegistry == nil {
			opts.workflowRegistry = executors.NewWorkflowRegistry()
		}
		opts.workflowRegistry.Register(id, handler)
	}
}

func defaultWorkflowRegistry() *executors.WorkflowRegistry {
	return executors.NewWorkflowRegistry()
}

func NoopWorkflowHandler(result map[string]any) executors.WorkflowHandler {
	return func(ctx context.Context, input map[string]any) (map[string]any, error) {
		return result, nil
	}
}
