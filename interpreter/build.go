package interpreter

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"

	"github.com/cretz/temporal-determinizer/interpreter/internal/interp"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func RegisterWorkflow(reg worker.WorkflowRegistry, fn interface{}, opts ...BuildOption) error {
	wrappedFn, funcName, err := BuildFunc(append([]BuildOption{WithWorkflowFunc(fn)}, opts...)...)
	if err != nil {
		return err
	}
	reg.RegisterWorkflowWithOptions(wrappedFn, workflow.RegisterOptions{Name: funcName})
	return nil
}

type buildOptions struct {
	prebuilt            *ssa.Program
	packagesConfig      *packages.Config
	workflowFunc        interface{}
	workflowFuncRefPkg  string
	workflowFuncRefName string
	workflowFuncRefType reflect.Type
}

type BuildOption func(*buildOptions)

func BuildFunc(opts ...BuildOption) (fn interface{}, funcName string, err error) {
	// Apply options
	var buildOpts buildOptions
	for _, opt := range opts {
		opt(&buildOpts)
	}

	// Get workflow func package name and func name
	pkgName := buildOpts.workflowFuncRefPkg
	funcName = buildOpts.workflowFuncRefName
	funcType := buildOpts.workflowFuncRefType
	// If no func ref given explicitly, derive from func
	if pkgName == "" {
		if buildOpts.workflowFunc == nil {
			return nil, "", fmt.Errorf("no workflow function given")
		}
		reflectFunc := reflect.ValueOf(buildOpts.workflowFunc)
		funcType = reflectFunc.Type()
		fullFuncName := runtime.FuncForPC(reflectFunc.Pointer()).Name()
		if strings.HasSuffix(fullFuncName, "-fm") {
			return nil, "", fmt.Errorf("methods not currently supported")
		}
		lastDot := strings.LastIndex(fullFuncName, ".")
		pkgName, funcName = fullFuncName[:lastDot], fullFuncName[lastDot+1:]
	}

	// Use prebuilt or build
	prog := buildOpts.prebuilt
	if prog == nil {
		// Load the packages
		var packagesConfig packages.Config
		if buildOpts.packagesConfig != nil {
			packagesConfig = *buildOpts.packagesConfig
		}
		var err error
		prog, err = Prebuild(&packagesConfig, pkgName)
		if err != nil {
			return nil, "", fmt.Errorf("failed building packages: %w", err)
		}
	} else if buildOpts.packagesConfig != nil {
		return nil, "", fmt.Errorf("cannot have prebuilt and packages config")
	}

	// Find the package and confirm function exists
	var pkg *ssa.Package
	for _, maybePkg := range prog.AllPackages() {
		if maybePkg.Pkg.Path() == pkgName {
			pkg = maybePkg
			break
		}
	}
	if pkg == nil {
		return nil, "", fmt.Errorf("cannot find package %v", pkgName)
	} else if pkg.Func(funcName) == nil {
		return nil, "", fmt.Errorf("cannot find function %v in package %v", pkgName, funcName)
	}

	// Validate and convert the function type  to accept a workflow context for
	// the workflow call
	funcType, err = validateAndConvertFuncType(funcType)
	if err != nil {
		return nil, "", fmt.Errorf("invalid function type: %w", err)
	}

	// Make a dynamic function
	fn = reflect.MakeFunc(funcType, func(args []reflect.Value) []reflect.Value {
		// Take off initial arg as context and just take interfaces for the rest of
		// the args
		workflowContext := args[0].Interface().(workflow.Context)
		argIfaces := make([]interface{}, len(args)-1)
		for i, arg := range args[1:] {
			argIfaces[i] = arg.Interface()
		}
		resIfaces, panicErr := interp.InterpretWorkflow(workflowContext, pkg, funcName, argIfaces...)
		results := make([]reflect.Value, funcType.NumOut())
		// If there was a panic, make all but the last result the zero value and set
		// error as non-retryable
		if panicErr != nil {
			for i := 0; i < funcType.NumOut()-1; i++ {
				results[i] = reflect.Zero(funcType.Out(i))
			}
			results[len(results)-1] = reflect.ValueOf(temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("interpreted workflow panicked: %v", panicErr), "INTERPRETER_PANIC", panicErr))
		} else {
			for i, resIface := range resIfaces {
				results[i] = reflect.ValueOf(resIface)
			}
		}
		return results
	}).Interface()
	return
}

func WithPrebuilt(prog *ssa.Program) BuildOption {
	return func(b *buildOptions) { b.prebuilt = prog }
}

func WithPackagesConfig(cfg *packages.Config) BuildOption {
	return func(b *buildOptions) { b.packagesConfig = cfg }
}

func WithWorkflowFunc(fn interface{}) BuildOption {
	return func(b *buildOptions) { b.workflowFunc = fn }
}

func WithWorkflowFuncRef(pkg, funcName string, funcType reflect.Type) BuildOption {
	return func(b *buildOptions) {
		b.workflowFuncRefPkg = pkg
		b.workflowFuncRefName = funcName
		b.workflowFuncRefType = funcType
	}
}

// Config will be mutated to have Mode as LoadAllSyntax
func Prebuild(config *packages.Config, pkg ...string) (*ssa.Program, error) {
	config.Mode |= packages.LoadAllSyntax
	pkgs, err := packages.Load(config, pkg...)
	if err != nil {
		return nil, fmt.Errorf("failed loading packages: %w", err)
	}
	// Make sure no packages have errors
	var pkgErrs []string
	for _, pkg := range pkgs {
		for _, pkgErr := range pkg.Errors {
			pkgErrs = append(pkgErrs, fmt.Sprintf("package %v error: %v", pkg.Name, pkgErr.Error()))
		}
	}
	if len(pkgErrs) > 0 {
		return nil, fmt.Errorf("%v error(s) loading packages:\n  %v", len(pkgErrs), strings.Join(pkgErrs, "  \n"))
	}

	// Load and build SSA
	// TODO(cretz): What's the practical difference between Packages and
	// AllPackages here?
	// TODO(cretz): What other builder modes might we want?
	ssaProg, _ := ssautil.AllPackages(pkgs, ssa.SanityCheckFunctions)
	ssaProg.Build()
	return ssaProg, nil
}

var (
	contextType         = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType           = reflect.TypeOf((*error)(nil)).Elem()
	workflowContextType = reflect.TypeOf((*workflow.Context)(nil)).Elem()
)

// Validates a workflow func type and converts the first param from
// workflow.Context to context.Context
func validateAndConvertFuncType(t reflect.Type) (reflect.Type, error) {
	// Check that it's a func that has a context.Context as the first param
	if t.Kind() != reflect.Func {
		return nil, fmt.Errorf("expected function type, got %v", t.Kind())
	} else if t.NumIn() == 0 || t.In(0) != contextType {
		return nil, fmt.Errorf("first parameter must be a context.Context")
	}

	// Check that it returns error or result + error
	if t.NumOut() < 1 || t.NumOut() > 2 {
		return nil, fmt.Errorf("result must be an error or result + error")
	} else if !t.Out(t.NumOut() - 1).Implements(errorType) {
		return nil, fmt.Errorf("result must be an error or a result + error")
	}

	// Copy in and out, but change in[0] to a workflow.Context
	in := make([]reflect.Type, t.NumIn())
	in[0] = workflowContextType
	for i := 1; i < t.NumIn(); i++ {
		in[i] = t.In(i)
	}
	out := make([]reflect.Type, t.NumOut())
	for i := 0; i < t.NumOut(); i++ {
		out[i] = t.Out(i)
	}
	return reflect.FuncOf(in, out, t.IsVariadic()), nil
}
