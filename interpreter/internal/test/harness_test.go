package test_test

import (
	"testing"

	"github.com/cretz/temporal-determinizer/interpreter"
	"github.com/cretz/temporal-determinizer/interpreter/internal/test"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	// All the test workflows
	_ "github.com/cretz/temporal-determinizer/interpreter/internal/test/workflows"
)

func TestDeterminizer(t *testing.T) {
	// Run each case as a separate test
	for _, c := range test.Cases {
		t.Run(c.Name, (&run{Case: c}).run)
	}
}

type run struct {
	*test.Case
	*require.Assertions
}

func (r *run) run(t *testing.T) {
	r.Assertions = require.New(t)
	// We want to run this three ways:
	// 1. In a test environment
	// 2. Against a real server
	// 3. In replay with history from #2

	// Build all workflows as completely separate funcs
	var firstWorkflowName string
	builtWorkflows := map[string]interface{}{}
	for _, w := range r.AllWorkflows() {
		fn, funcName, err := interpreter.BuildFunc(interpreter.WithWorkflowFunc(w))
		r.NoError(err, "failed building %v", r.Name)
		if firstWorkflowName == "" {
			firstWorkflowName = funcName
		}
		builtWorkflows[funcName] = fn
	}

	// Run in test environment
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	for name, fn := range builtWorkflows {
		env.RegisterWorkflowWithOptions(fn, workflow.RegisterOptions{Name: name})
	}
	for _, a := range r.AllActivities() {
		env.RegisterActivity(a)
	}
	env.ExecuteWorkflow(firstWorkflowName, r.WorkflowArgs...)
	r.NoError(env.GetWorkflowError())
}
