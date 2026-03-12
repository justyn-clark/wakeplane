package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/executors"
)

type Executor struct {
	client *stdhttp.Client
}

func New() *Executor {
	return &Executor{client: &stdhttp.Client{Timeout: 0}}
}

func (e *Executor) Kind() domain.TargetKind {
	return domain.TargetKindHTTP
}

func (e *Executor) Execute(ctx context.Context, req executors.ExecuteRequest) executors.Result {
	var body []byte
	if req.Schedule.Target.Body != nil {
		body, _ = json.Marshal(req.Schedule.Target.Body)
	}
	httpReq, err := stdhttp.NewRequestWithContext(ctx, strings.ToUpper(req.Schedule.Target.Method), req.Schedule.Target.URL, bytes.NewReader(body))
	if err != nil {
		return executors.Result{ErrorText: err.Error()}
	}
	for k, v := range req.Schedule.Target.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := e.client.Do(httpReq)
	if err != nil {
		cancelled := ctx.Err() != nil
		return executors.Result{ErrorText: err.Error(), Cancelled: cancelled}
	}
	defer resp.Body.Close()
	status := resp.StatusCode
	receipt := executors.Receipt{Kind: "http_response", ContentType: resp.Header.Get("Content-Type"), Body: resp.Status}
	return executors.Result{
		HTTPStatusCode: &status,
		ResultJSON:     domain.MustJSON(map[string]any{"status": resp.Status, "url": req.Schedule.Target.URL}),
		Receipts:       []executors.Receipt{receipt},
		ErrorText:      httpError(resp.StatusCode),
	}
}

func httpError(code int) string {
	if code >= 200 && code < 300 {
		return ""
	}
	return fmt.Sprintf("http executor returned status %d", code)
}

var _ = time.Second
