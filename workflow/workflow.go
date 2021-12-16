package workflow

import (
	"context"
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
)

type Future interface {
	Get(context.Context, interface{}) error
	IsReady() bool
}

type ActivityOptions struct {
	Activity               string
	TaskQueue              string
	ScheduleToCloseTimeout time.Duration
	ScheduleToStartTimeout time.Duration
	StartToCloseTimeout    time.Duration
	HeartbeatTimeout       time.Duration
	WaitForCancellation    bool
	ActivityID             string
	RetryPolicy            *temporal.RetryPolicy
}

var ErrNotInInterpreter = errors.New("can only be called in interpreter")

func ExecuteActivity(opts ActivityOptions, args ...interface{}) Future {
	panic(ErrNotInInterpreter)
}
