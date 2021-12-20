package interp

import (
	"fmt"
	"go/token"
	"go/types"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"

	"go.temporal.io/sdk/workflow"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

var standardPackages = map[string]bool{}

func init() {
	pkgs, err := packages.Load(nil, "std")
	if err != nil {
		panic(err)
	}
	for _, pkg := range pkgs {
		standardPackages[pkg.PkgPath] = true
	}
}

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
	}
	i.hijacker = &temporalHijacker{
		ctx: ctx,
		i:   i,
		// hijackCallPackages: stdHijackCallPackages,
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
	i   *interpreter
	// hijackCallPackages map[string]*hijackCallPackage
}

var _ hijacker = &temporalHijacker{}

func (t *temporalHijacker) visitInstr(fr *frame, instr ssa.Instruction) (bool, continuation) {
	// if t.i.mode&EnableTracing != 0 && instr.Pos() != token.NoPos {
	// 	t.debugf("\t\t%v", loc(fr.i.prog.Fset, instr.Pos()))
	// }

	// // Check if the cal is hijacked
	// if call, _ := instr.(*ssa.Call); call != nil {
	// 	// TODO(cretz): This is done twice for interpreted calls (here and
	// 	// elsewhere), so optimize
	// 	fn, args := prepareCall(fr, &call.Call)
	// 	if ssaFn, _ := fn.(*ssa.Function); ssaFn != nil {
	// 		callPkg := t.hijackCallPackages[ssaFn.Pkg.Pkg.Path()]
	// 		if callPkg != nil {
	// 			action := callPkg.functionActions[ssaFn.Name()]
	// 			if action == nil {
	// 				action = callPkg.defaultAction
	// 			}
	// 			if action != nil {
	// 				action(t, fr, call, ssaFn, args)
	// 				return true, kNext
	// 			}
	// 		}
	// 	}
	// }
	return false, kNext
}

func (t *temporalHijacker) debugf(f string, v ...interface{}) {
	if t.i.mode&EnableTracing != 0 {
		fmt.Fprintf(os.Stderr, f+"\n", v...)
	}
}

func init() {
	// Ignore all std inits
	pkgs, err := packages.Load(nil, "std")
	if err != nil {
		panic(err)
	}
	for _, pkg := range pkgs {
		externals[pkg.PkgPath+".init"] = extNoop
	}

	// Add a bunch of forwards
	addExtForwards(
		fmt.Errorf,
	)
}

func addExtForwards(fns ...interface{}) {
	for _, fn := range fns {
		externals[runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()] = extForward(fn)
	}
}

func extNoop(*frame, []value) value { return nil }

func extForward(to interface{}) externalFn {
	// TODO(cretz): Methods, variadic, etc
	return func(fr *frame, args []value) value {
		argVals := make([]reflect.Value, len(args))
		for i, arg := range args {
			argVals[i] = reflect.ValueOf(arg)
		}
		retVals := reflect.ValueOf(to).Call(argVals)
		if len(retVals) == 0 {
			return nil
		} else if len(retVals) == 1 {
			return retVals[0].Interface()
		}
		ret := make(tuple, len(retVals))
		for i, retVal := range retVals {
			ret[i] = retVal.Interface
		}
		return ret
	}
}

// var stdHijackCallPackages = map[string]*hijackCallPackage{}

// func init() {
// 	// Load all std packages
// 	pkgs, err := packages.Load(nil, "std")
// 	if err != nil {
// 		panic(err)
// 	}
// 	for _, pkg := range pkgs {
// 		// Ignore inits, forward everything else (TODO(cretz): for now)
// 		stdHijackCallPackages[pkg.PkgPath] = &hijackCallPackage{
// 			functionActions: map[string]hijackCallAction{"init": hijackCallActionIgnore},
// 		}
// 	}
// }

// type hijackCallPackage struct {
// 	functionActions map[string]hijackCallAction
// 	defaultAction   hijackCallAction
// }

// type hijackCallAction func(t *temporalHijacker, caller *frame, call *ssa.Call, fn *ssa.Function, args []value)

// func hijackCallActionIgnore(t *temporalHijacker, caller *frame, call *ssa.Call, fn *ssa.Function, args []value) {
// 	t.debugf("Ignoring %v", fn)
// 	caller.env[call] = nil
// }

// func hijackCallActionForward(to interface{}) hijackCallAction {
// 	return func(t *temporalHijacker, caller *frame, call *ssa.Call, fn *ssa.Function, args []value) {
// 		// t.debugf("Forwarding %v", fn)
// 		caller.env[call] = callSSA(t.i, caller, call.Pos(), fn, args, nil)
// 	}
// }
