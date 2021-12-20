package dynreflect

import "reflect"

type Resolver interface {
	TopLevelType(pkgName, itemName string) reflect.Type
}
