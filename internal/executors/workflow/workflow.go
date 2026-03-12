package workflow

import (
	"context"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/executors"
)

type Executor struct {
	registry *executors.WorkflowRegistry
}

func New(registry *executors.WorkflowRegistry) *Executor {
	return &Executor{registry: registry}
}

func (e *Executor) Kind() domain.TargetKind {
	return domain.TargetKindWorkflow
}

func (e *Executor) Execute(ctx context.Context, req executors.ExecuteRequest) executors.Result {
	result, err := e.registry.Execute(ctx, req.Schedule.Target.WorkflowID, req.Schedule.Target.Input)
	if err != nil {
		return executors.Result{
			ErrorText: err.Error(),
			Cancelled: ctx.Err() != nil,
		}
	}
	return executors.Result{
		ResultJSON: domain.MustJSON(result),
		Receipts: []executors.Receipt{
			{
				Kind:        "workflow_result",
				ContentType: "application/json",
				Body:        string(domain.MustJSON(result)),
			},
		},
	}
}
