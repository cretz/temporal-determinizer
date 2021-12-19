package interp

import (
	"fmt"
	"go/token"
	"go/types"
	"runtime/debug"

	"go.temporal.io/sdk/workflow"
	"golang.org/x/tools/go/ssa"
)

type hijacker interface {
	visitInstr(*frame, ssa.Instruction) (handled bool, cont continuation)
}

type WorkflowPanicError struct {
	Value            interface{}
	Reason           string
	InterpreterStack []byte
	// TODO(cretz): RuntimeStack []byte
}

func (w *WorkflowPanicError) Error() string {
	return fmt.Sprintf("workflow panic - reason: %v, value: %v", w.Reason, w.Value)
}

func InterpretWorkflow(
	ctx workflow.Context,
	pkg *ssa.Package,
	fnName string,
	args ...interface{},
) (ret []interface{}, panicErr *WorkflowPanicError) {
	// Much of the logic here mimics Interpret

	// Create interpreter
	i := &interpreter{
		// Do not pass OS args
		osArgs:  []value{"interpreted-exe"},
		prog:    pkg.Prog,
		globals: make(map[ssa.Value]*value),
		mode:    EnableTracing,
		// Set sizes as common 64-bit
		sizes:      &types.StdSizes{WordSize: 8, MaxAlign: 8},
		goroutines: 1,
		hijacker:   &temporalHijacker{ctx},
	}
	runtimePkg := i.prog.ImportedPackage("runtime")
	if runtimePkg == nil {
		panic("ssa.Program doesn't include runtime package")
	}
	i.runtimeErrorString = runtimePkg.Type("errorString").Object().Type()

	// Init reflection
	initReflect(i)

	// Init globals
	var workflowPkg *ssa.Package
	for _, pkg := range i.prog.AllPackages() {
		for _, m := range pkg.Members {
			switch v := m.(type) {
			case *ssa.Global:
				cell := zero(deref(v.Type()))
				i.globals[v] = &cell
			}
		}

		// Set the current workflow context global
		if pkg.Pkg.Path() == "github.com/cretz/temporal-determinizer/workflow" {
			workflowPkg = pkg
		}
	}

	// Put the context on the workflow package
	if workflowPkg == nil {
		panic("cannot find workflow package")
	}
	var ctxValue value = ctx
	i.globals[workflowPkg.Members["currentWorkflowContext"].(*ssa.Global)] = &ctxValue

	// Handle any panic
	completed := false
	defer func() {
		if completed {
			return
		}
		p := recover()
		switch p := p.(type) {
		case exitPanic:
			panicErr = &WorkflowPanicError{Value: int(p), Reason: "called os.Exit"}
		case targetPanic:
			// TODO(cretz): Capture caller stack trace
			panicErr = &WorkflowPanicError{Value: p.v, Reason: "called panic"}
		default:
			panicErr = &WorkflowPanicError{Value: p, Reason: "unknown"}
		}

		workflow.GetLogger(ctx).Warn(panicErr.Error() + ", stack: " + string(debug.Stack()))
	}()

	// Call init
	call(i, nil, token.NoPos, pkg.Func("init"), nil)

	// Convert args
	// TODO(cretz): Confirm workflow.Context can be used as context.Context
	argValues := make([]value, len(args))
	for i, arg := range args {
		argValues[i] = arg
	}

	// Call and convert response
	retValues := []value{call(i, nil, token.NoPos, pkg.Func(fnName), argValues)}
	completed = true
	// Close global channel
	close((*i.globals[workflowPkg.Members["entireWorkflowDoneCh"].(*ssa.Global)]).(chan struct{}))

	// Expand the tuple
	if toExpand, ok := retValues[0].(tuple); ok {
		retValues = toExpand
	}
	// Convert back to interfaces
	ret = make([]interface{}, len(retValues))
	for i, retValue := range retValues {
		ret[i] = retValue
	}
	return
}

type temporalHijacker struct {
	ctx workflow.Context
}

var _ hijacker = &temporalHijacker{}

func (t *temporalHijacker) visitInstr(fr *frame, instr ssa.Instruction) (bool, continuation) {
	return false, kNext
}
