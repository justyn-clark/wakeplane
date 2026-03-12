package executors

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

type Receipt struct {
	Kind        string
	ContentType string
	Body        string
}

type Result struct {
	HTTPStatusCode *int
	ExitCode       *int
	ResultJSON     json.RawMessage
	ErrorText      string
	Receipts       []Receipt
	Cancelled      bool
}

type ExecuteRequest struct {
	Schedule domain.Schedule
	Run      domain.Run
	Timeout  int
}

type Executor interface {
	Kind() domain.TargetKind
	Execute(ctx context.Context, req ExecuteRequest) Result
}

type WorkflowHandler func(ctx context.Context, input map[string]any) (map[string]any, error)

type WorkflowRegistry struct {
	mu       sync.RWMutex
	handlers map[string]WorkflowHandler
}

func NewWorkflowRegistry() *WorkflowRegistry {
	return &WorkflowRegistry{handlers: map[string]WorkflowHandler{}}
}

func (r *WorkflowRegistry) Register(id string, handler WorkflowHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[id] = handler
}

func (r *WorkflowRegistry) Execute(ctx context.Context, workflowID string, input map[string]any) (map[string]any, error) {
	r.mu.RLock()
	handler, ok := r.handlers[workflowID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("workflow %q is not registered", workflowID)
	}
	return handler(ctx, input)
}

type Registry struct {
	executors map[domain.TargetKind]Executor
}

func NewRegistry(execs ...Executor) *Registry {
	r := &Registry{executors: map[domain.TargetKind]Executor{}}
	for _, exec := range execs {
		r.executors[exec.Kind()] = exec
	}
	return r
}

func (r *Registry) Get(kind domain.TargetKind) (Executor, bool) {
	exec, ok := r.executors[kind]
	return exec, ok
}
