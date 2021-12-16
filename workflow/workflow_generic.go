//go:build go1.18
// +build go1.18

package workflow

func ProxyActivitiesGen[A any](v A, opts ActivityOptions) A {
	panic(ErrNotInInterpreter)
}