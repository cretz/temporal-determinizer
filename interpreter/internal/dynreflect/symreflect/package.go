package symreflect

import (
	"fmt"
	"go/types"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/modern-go/reflect2"
	"golang.org/x/tools/go/packages"
)

type Packages map[string]*Package

type Package struct {
	Info *types.Package
	// Includes functions but not vars (yet?)
	TopLevelTypes map[string]reflect.Type
	Funcs         map[string]*runtime.Func
}

type LoadPackagesConfig struct {
	// Appends to Packages based on patterns
	Patterns []string
	Packages []*types.Package
}

// Results may not have all pieces if they could not be found. May mutate
// config. Result is keyed by qualified package path.
func LoadPackages(config LoadPackagesConfig) (Packages, error) {
	// If there are patterns, load them and append to preloaded packages
	if len(config.Patterns) > 0 {
		// TODO(cretz): Make any of this configurable?
		pkgs, err := packages.Load(&packages.Config{
			Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps | packages.NeedTypes,
		}, config.Patterns...)
		if err != nil {
			return nil, fmt.Errorf("failed loading packages: %w", err)
		}
		for _, pkg := range pkgs {
			config.Packages = append(config.Packages, pkg.Types)
		}
	}

	// Prepare results
	pkgs := make(Packages, len(config.Packages))
	for _, pkg := range config.Packages {
		pkgs[pkg.Path()] = &Package{
			Info:          pkg,
			TopLevelTypes: map[string]reflect.Type{},
			Funcs:         map[string]*runtime.Func{},
		}
	}

	// Load up funcs
	pkgs.loadFuncs()
	// Load up top-level types
	pkgs.loadTopLevelTypes()

	return pkgs, nil
}

//go:linkname modulesSlice runtime.modulesSlice
var modulesSlice *reflect.SliceHeader

func (p Packages) loadFuncs() {
	// Inspired by https://github.com/alangpierce/go-forceexport

	// Get the module slice type
	modDataType := reflect2.TypeByPackageName("runtime", "moduledata").Type1()

	// Reflect the linked var
	modSliceVal := reflect.NewAt(reflect.SliceOf(reflect.PtrTo(modDataType)), unsafe.Pointer(modulesSlice)).Elem()

	// Iterate to collect functions
	for i := 0; i < modSliceVal.Len(); i++ {
		modDataVal := modSliceVal.Index(i).Elem()

		// Get the lookup table
		pclntable := getUnexportedField(modDataVal.FieldByName("pclntable")).([]byte)

		// Get the function table and loop over it
		ftabVal := modDataVal.FieldByName("ftab")
		for j := 0; j < ftabVal.Len(); j++ {
			// Get offset
			off := getUnexportedField(ftabVal.Index(j).FieldByName("funcoff")).(uintptr)
			// Sometimes the offset is out of range, ignore it
			if int(off) >= len(pclntable) {
				continue
			}
			// Convert and separate package from func name
			fn := (*runtime.Func)(unsafe.Pointer(&pclntable[off]))
			fnName := fn.Name()
			if fnName == "" {
				continue
			}
			pkgPathEndIndex := strings.LastIndex(fnName, "/")
			pkgPathEndIndex += 1 + strings.Index(fnName[pkgPathEndIndex+1:], ".")
			if pkgPathEndIndex < 0 {
				continue
			}
			pkgPath, fnName := fnName[:pkgPathEndIndex], fnName[pkgPathEndIndex+1:]
			// Set on package if there
			if pkg := p[pkgPath]; pkg != nil {
				pkg.Funcs[fnName] = fn
			}
		}
	}
}

func (p Packages) loadTopLevelTypes() {
	allTypes := allTopLevelTypes()

	// Go over each package
	for _, pkg := range p {
		scope := pkg.Info.Scope()
		for _, name := range scope.Names() {
			pkg.TopLevelTypes[name] = findReflectType(allTypes, scope.Lookup(name).Type())
		}
	}
}

func getUnexportedField(field reflect.Value) interface{} {
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()
}

func ReflectType(t types.Type) reflect.Type {
	return findReflectType(allTopLevelTypes(), t)
}

func findReflectType(all map[string]map[string]reflect.Type, t types.Type) reflect.Type {
	switch t := t.(type) {
	case *types.Array:
		return reflect.ArrayOf(int(t.Len()), findReflectType(all, t.Elem()))
	case *types.Basic:
		// TODO(cretz): Cache this
		return reflect.TypeOf(basicZero(t))
	case *types.Chan:
		elem := findReflectType(all, t.Elem())
		if t.Dir() == types.SendRecv {
			return reflect.ChanOf(reflect.BothDir, elem)
		} else if t.Dir() == types.SendOnly {
			return reflect.ChanOf(reflect.SendDir, elem)
		} else {
			return reflect.ChanOf(reflect.RecvDir, elem)
		}
	// TODO(cretz): Dynamic interfaces, probably requires lookup - case *types.Interface:
	case *types.Map:
		return reflect.MapOf(findReflectType(all, t.Key()), findReflectType(all, t.Elem()))
	case *types.Named:
		// If it's an interface or a struct, we look it up
		_, isStructOrIface := t.Underlying().(*types.Struct)
		if !isStructOrIface {
			_, isStructOrIface = t.Underlying().(*types.Interface)
		}
		if isStructOrIface {
			// Find the type in our global map
			var pkgName string
			if t.Obj().Pkg() != nil {
				pkgName = t.Obj().Pkg().Path()
			}
			ret := all[pkgName][t.Obj().Name()]
			if ret == nil {
				panic(fmt.Sprintf("cannot find type for %v", t))
			}
			return ret
		}
		// Otherwise, just recurse
		return findReflectType(all, t.Underlying())
	case *types.Pointer:
		return reflect.PtrTo(findReflectType(all, t.Elem()))
	case *types.Signature:
		if t.Recv() != nil {
			// TODO(cretz): Need to support with receiver?
			panic(fmt.Sprintf("%T not supported since it has receiver", t))
		}
		in := make([]reflect.Type, t.Params().Len())
		for i := 0; i < t.Params().Len(); i++ {
			in[i] = findReflectType(all, t.Params().At(i).Type())
		}
		out := make([]reflect.Type, t.Results().Len())
		for i := 0; i < t.Results().Len(); i++ {
			out[i] = findReflectType(all, t.Results().At(i).Type())
		}
		return reflect.FuncOf(in, out, t.Variadic())
	case *types.Slice:
		return reflect.SliceOf(findReflectType(all, t.Elem()))
	// TODO(cretz): Dynamic structs, probably requires lookup - case *types.Struct:
	default:
		panic(fmt.Sprintf("%T not supported, unknown type", t))
	}
}

func basicZero(t *types.Basic) interface{} {
	switch t.Kind() {
	case types.Bool, types.UntypedBool:
		return false
	case types.Int:
		return int(0)
	case types.Int8:
		return int8(0)
	case types.Int16:
		return int16(0)
	case types.Int32, types.UntypedRune:
		return int32(0)
	case types.Int64, types.UntypedInt:
		return int64(0)
	case types.Uint:
		return uint(0)
	case types.Uint8:
		return uint8(0)
	case types.Uint16:
		return uint16(0)
	case types.Uint32:
		return uint32(0)
	case types.Uint64:
		return uint64(0)
	case types.Uintptr:
		return uintptr(0)
	case types.Float32:
		return float32(0)
	case types.Float64, types.UntypedFloat:
		return float64(0)
	case types.Complex64:
		return complex64(0)
	case types.Complex128, types.UntypedComplex:
		return complex128(0)
	case types.String, types.UntypedString:
		return ""
	case types.UnsafePointer:
		var ptr unsafe.Pointer
		return ptr
	default:
		panic(fmt.Sprintf("unrecognized basic kind for type: %v", t))
	}
}

// No vars/funcs
var allTopLevelTypesMap map[string]map[string]reflect.Type
var allTopLevelTypesOnce sync.Once

//go:linkname typelinks reflect.typelinks
func typelinks() (sections []unsafe.Pointer, offset [][]int32)

//go:linkname resolveTypeOff reflect.resolveTypeOff
func resolveTypeOff(rtype unsafe.Pointer, off int32) unsafe.Pointer

type emptyInterface struct {
	typ  unsafe.Pointer
	word unsafe.Pointer
}

func allTopLevelTypes() map[string]map[string]reflect.Type {
	allTopLevelTypesOnce.Do(func() {
		// Inspired by https://github.com/modern-go/reflect2
		allTopLevelTypesMap = map[string]map[string]reflect.Type{}
		var obj interface{} = reflect.TypeOf(0)
		sections, offset := typelinks()
		for i, offs := range offset {
			rodata := sections[i]
			for _, off := range offs {
				(*emptyInterface)(unsafe.Pointer(&obj)).word = resolveTypeOff(unsafe.Pointer(rodata), off)
				typ := obj.(reflect.Type)
				if typ.Kind() == reflect.Ptr {
					loadedType := typ.Elem()
					pkgTypes := allTopLevelTypesMap[loadedType.PkgPath()]
					if pkgTypes == nil {
						pkgTypes = map[string]reflect.Type{}
						allTopLevelTypesMap[loadedType.PkgPath()] = pkgTypes
					}
					pkgTypes[loadedType.Name()] = loadedType
				}
			}
		}

		// Add additional
		// TODO(cretz): Why?
		allTopLevelTypesMap["time"]["ticker"] = reflect.TypeOf(time.Ticker{})
	})
	return allTopLevelTypesMap
}
