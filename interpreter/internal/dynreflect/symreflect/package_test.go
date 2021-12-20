package symreflect_test

import (
	"fmt"
	"testing"

	"github.com/cretz/temporal-determinizer/interpreter/internal/dynreflect/symreflect"
	"github.com/stretchr/testify/require"
)

func TestPackageFunc(t *testing.T) {
	t.Skip("Some types are missing when loading via symbols")
	pkgs, err := symreflect.LoadPackages(symreflect.LoadPackagesConfig{Patterns: []string{"time"}})
	require.NoError(t, err)

	for _, pkg := range pkgs {
		fmt.Printf("PKG: %v\n", pkg.Info.Path())
		for k, v := range pkg.TopLevelTypes {
			fmt.Printf("  TYPE: %v - %v\n", k, v)
		}
		for k, v := range pkg.Funcs {
			fmt.Printf("  FUNC: %v - %v\n", k, v)
		}
	}
}
