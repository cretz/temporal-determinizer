package workflows

import (
	"context"
	"time"

	"github.com/cretz/temporal-determinizer/interpreter/internal/test"
	"github.com/cretz/temporal-determinizer/workflow"
	"go.temporal.io/sdk/activity"
)

func init() {
	test.AddCase(test.Case{
		Workflows:  HelloWorkflow,
		Activities: HelloActivity,
	})
}

func HelloWorkflow(ctx context.Context, name string) (string, error) {
	workflow.Log().Info("HelloWorld workflow started", "name", name)

	var result string
	err := workflow.ExecuteActivity(ctx, workflow.ActivityOptions{
		Activity:            HelloActivity,
		StartToCloseTimeout: 10 * time.Second,
	}, name).Get(ctx, &result)
	if err != nil {
		workflow.Log().Error("Activity failed", "error", err)
		return "", err
	}

	workflow.Log().Info("HelloWorld workflow completed", "result", result)
	return result, nil
}

func HelloActivity(ctx context.Context, name string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Activity", "name", name)
	return "Hello " + name + "!", nil
}
