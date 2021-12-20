package workflow

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.temporal.io/sdk/workflow"
)

var ErrNotInRuntime = errors.New("can only be called in alternative runtime")

var currentWorkflowContext workflow.Context
var entireWorkflowDoneCh = make(chan struct{}, 1)

func workflowContext() workflow.Context {
	if currentWorkflowContext == nil {
		panic(ErrNotInRuntime)
	}
	return currentWorkflowContext
}

type combinedContext struct {
	workflowContext       workflow.Context
	goContext             context.Context
	goContextDoneErr      error
	goContextDoneErrMutex sync.RWMutex
}

func withGoContext(ctx context.Context) workflow.Context {
	// Create a new context that is canceled when this context is
	// TODO(cretz): Make a better combination?
	wCtx, cancel := workflow.WithCancel(workflowContext())
	c := &combinedContext{workflowContext: wCtx, goContext: ctx}
	// Wait for go context cancelled or top-level context cancelled
	go func() {
		select {
		case <-ctx.Done():
			c.goContextDoneErrMutex.Lock()
			// Only set if the workflow context error isn't already set
			if wCtx.Err() != nil {
				c.goContextDoneErr = ctx.Err()
				// Translate to workflow equivalent
				if c.goContextDoneErr == context.Canceled {
					c.goContextDoneErr = workflow.ErrCanceled
				} else if c.goContextDoneErr == context.DeadlineExceeded {
					c.goContextDoneErr = workflow.ErrDeadlineExceeded
				}
			}
			c.goContextDoneErrMutex.Unlock()
		case <-entireWorkflowDoneCh:
		}
		cancel()
	}()
	return c
}

func (c *combinedContext) Deadline() (time.Time, bool) {
	d1, ok1 := c.workflowContext.Deadline()
	d2, ok2 := c.goContext.Deadline()
	if !ok1 {
		return d2, ok2
	} else if !ok2 {
		return d1, ok1
	} else if d1.Before(d2) {
		return d1, true
	}
	return d2, true
}

func (c *combinedContext) Done() workflow.Channel { return c.workflowContext.Done() }

func (c *combinedContext) Err() error {
	c.goContextDoneErrMutex.RLock()
	err := c.goContextDoneErr
	c.goContextDoneErrMutex.RUnlock()
	if err == nil {
		err = c.workflowContext.Err()
	}
	return err
}

func (c *combinedContext) Value(key interface{}) interface{} {
	// Try the Go context first
	v := c.goContext.Value(key)
	if v == nil {
		v = c.workflowContext.Value(key)
	}
	return v
}
