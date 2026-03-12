package shell

import (
	"bytes"
	"context"
	"os/exec"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/executors"
)

type Executor struct{}

func New() *Executor {
	return &Executor{}
}

func (e *Executor) Kind() domain.TargetKind {
	return domain.TargetKindShell
}

func (e *Executor) Execute(ctx context.Context, req executors.ExecuteRequest) executors.Result {
	cmd := exec.CommandContext(ctx, req.Schedule.Target.Command, req.Schedule.Target.Args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := executors.Result{
		ResultJSON: domain.MustJSON(map[string]any{
			"command": req.Schedule.Target.Command,
			"args":    req.Schedule.Target.Args,
		}),
		Receipts: []executors.Receipt{
			{Kind: "stdout", ContentType: "text/plain", Body: stdout.String()},
			{Kind: "stderr", ContentType: "text/plain", Body: stderr.String()},
		},
	}
	if cmd.ProcessState != nil {
		exit := cmd.ProcessState.ExitCode()
		result.ExitCode = &exit
	}
	if err != nil {
		result.ErrorText = err.Error()
		result.Cancelled = ctx.Err() != nil
	}
	return result
}
