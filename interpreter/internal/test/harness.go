package test

import (
	"reflect"
	"runtime"
	"strings"
)

type Case struct {
	// Derived from first workflow if not set
	Name         string
	Workflows    interface{}
	WorkflowArgs []interface{}
	Activities   interface{}

	workflowsSlice  []interface{}
	activitiesSlice []interface{}
}

// Not thread safe, never mutate outside of AddCase
var Cases []*Case

func AddCase(c Case) {
	c.workflowsSlice = rawToSlice(c.Workflows)
	if len(c.workflowsSlice) == 0 {
		panic("at least one workflow required")
	}
	c.activitiesSlice = rawToSlice(c.Activities)
	c.Name, _ = getFunctionName(c.workflowsSlice[0])
	Cases = append(Cases, &c)
}

func (c *Case) AllWorkflows() []interface{}  { return c.workflowsSlice }
func (c *Case) AllActivities() []interface{} { return c.activitiesSlice }

func getFunctionName(i interface{}) (name string, isMethod bool) {
	if fullName, ok := i.(string); ok {
		return fullName, false
	}
	fullName := runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
	// Full function name that has a struct pointer receiver has the following format
	// <prefix>.(*<type>).<function>
	isMethod = strings.ContainsAny(fullName, "*")
	elements := strings.Split(fullName, ".")
	shortName := elements[len(elements)-1]
	// This allows to call activities by method pointer
	// Compiler adds -fm suffix to a function name which has a receiver
	// Note that this works even if struct pointer used to get the function is nil
	// It is possible because nil receivers are allowed.
	// For example:
	// var a *Activities
	// ExecuteActivity(ctx, a.Foo)
	// will call this function which is going to return "Foo"
	return strings.TrimSuffix(shortName, "-fm"), isMethod
}

func rawToSlice(v interface{}) []interface{} {
	val := reflect.ValueOf(v)
	if !val.IsValid() {
		return nil
	} else if val.Kind() != reflect.Slice {
		return []interface{}{v}
	}
	ret := make([]interface{}, val.Len())
	for i := 0; i < val.Len(); i++ {
		ret[i] = val.Index(i).Interface()
	}
	return ret
}
