package workflow

import (
	"context"
	"time"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func Log() log.Logger { return workflow.GetLogger(workflowContext()) }

type Future interface {
	Get(context.Context, interface{}) error
	IsReady() bool
}

type future struct{ underlying workflow.Future }

func (f *future) Get(ctx context.Context, v interface{}) error {
	return f.underlying.Get(withGoContext(ctx), v)
}

func (f *future) IsReady() bool {
	return f.underlying.IsReady()
}

type ActivityOptions struct {
	Activity               interface{}
	TaskQueue              string
	ScheduleToCloseTimeout time.Duration
	ScheduleToStartTimeout time.Duration
	StartToCloseTimeout    time.Duration
	HeartbeatTimeout       time.Duration
	WaitForCancellation    bool
	ActivityID             string
	RetryPolicy            *temporal.RetryPolicy
}

func ExecuteActivity(ctx context.Context, opts ActivityOptions, args ...interface{}) Future {
	wCtx := workflow.WithActivityOptions(withGoContext(ctx), workflow.ActivityOptions{
		TaskQueue:              opts.TaskQueue,
		ScheduleToCloseTimeout: opts.ScheduleToCloseTimeout,
		ScheduleToStartTimeout: opts.ScheduleToStartTimeout,
		StartToCloseTimeout:    opts.StartToCloseTimeout,
		HeartbeatTimeout:       opts.HeartbeatTimeout,
		WaitForCancellation:    opts.WaitForCancellation,
		ActivityID:             opts.ActivityID,
		RetryPolicy:            opts.RetryPolicy,
	})
	return &future{workflow.ExecuteActivity(wCtx, opts.Activity, args...)}
}

func ProxyActivities(opts ActivityOptions, v interface{}) interface{} {
	panic(ErrNotInRuntime)
}
