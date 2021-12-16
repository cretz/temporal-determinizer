package interp

import (
	"go.temporal.io/sdk/workflow"
	"golang.org/x/tools/go/ssa"
)

type hijacker interface {
	visitInstr(*frame, ssa.Instruction) (handled bool, cont continuation)
}

func InterpretWorkflow(ctx workflow.Context, pkg *ssa.Package, fnName string, args ...interface{}) []interface{} {
	panic("TODO")
}

type temporalHijacker struct {
	ctx workflow.Context
}

var _ hijacker = &temporalHijacker{}

func (t *temporalHijacker) visitInstr(fr *frame, instr ssa.Instruction) (bool, continuation) {
	return false, kNext
}
