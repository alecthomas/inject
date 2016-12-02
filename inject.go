// Package inject implements an Inversion of Control container (dependency injection) for Go.
//
// Example usage:
//
// 		injector := New()
// 		injector.Bind(http.DefaultServeMux)
// 		injector.Call(func(mux *http.ServeMux) {
// 		})
//
// It supports static bindings:
//
// 		injector.Bind(http.DefaultServeMux)
//
// As well as recursive provider functions:
//
//		type MongoURI string
//
//		injector.Bind(func(uri MongoURI) *mgo.Database {
//			s, err := mgo.Dial(string(uri))
//			if err != nil {
//				panic(err)
//			}
//			return s.DB("my_db")
//		})
//
// 		injector.Bind(func(db *mgo.Database) *mgo.Collection {
// 			return db.C("my_collection")
// 		})
//
// 		injector.Call(func(c *mgo.Collection) {
// 			// ...
// 		})
//
// To bind a function as a value, use Literal:
//
// 		injector.Bind(Literal(fmt.Sprintf))
//
// Mapping bindings are supported, in which case multiple bindings to the same map type will
// merge the maps:
//
// 		injector.Bind(Mapping(map[string]int{"one": 1}))
// 		injector.Bind(Mapping(map[string]int{"two": 2}))
//		injector.Bind(Mapping(func() map[string]int { return map[string]int{"three": 3} }))
//		injector.Call(func(m map[string]int) {
//		  // m == map[string]int{"one": 1, "two": 2, "three": 3}
//		})
//
// As are sequences, where multiple bindings will merge into a single slice
// (note that order is arbitrary):
//
// 		injector.Bind(Sequence([]int{1, 2}))
// 		injector.Bind(Sequence([]int{3, 4}))
// 		injector.Bind(Sequence(func() []int { return  []int{5, 6} }))
// 		injector.Call(func(s []int) {
// 			// s = []int{1, 2, 3, 4, 5, 6}
// 		})
//
// The equivalent of "named" values can be achieved with type aliases:
//
// 		type Name string
//
// 		injector.Bind(Name("Bob"))
// 		injector.Get(Name(""))
//
// Modules are also supported, see the Install() method for details.
package inject

import (
	"fmt"
	"reflect"
	"strings"
)

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// Binding represents a function that resolves to a value given a set of input values.
type Binding struct {
	Provides reflect.Type
	Requires []reflect.Type
	Build    func() (interface{}, error)
}

// Injector is a IoC container.
type Injector struct {
	parent   *Injector
	bindings map[reflect.Type]*Binding
	stack    map[reflect.Type]bool
	modules  map[reflect.Type]reflect.Value
}

// New creates a new Injector.
//
// The injector itself is already bound.
func New() *Injector {
	i := &Injector{
		bindings: map[reflect.Type]*Binding{},
		stack:    map[reflect.Type]bool{},
		modules:  map[reflect.Type]reflect.Value{},
	}
	_ = i.Bind(i)
	return i
}

// Install a module. A module is a struct whose methods are providers. This is useful for grouping
// configuration data together with providers.
//
// Duplicate modules are allowed as long as all fields are identical.
//
// Any method starting with "Provide" will be bound as a Provider. If the method name contains
// "Multi" it will not be a singleton provider. If the method name contains "Sequence" it must
// return a slice which is merged with slices of the same type. If the method name contains
// "Mapping" it must return a mapping which will be merged with mappings of the same type. Mapping
// and Sequence can not be used simultaneously.
//
// For example, the following method will be called only once:
//
// 		ProvideLog() *log.Logger { return log.New(...) }
//
// While this method will be called each time a *log.Logger is injected.
//
// 		ProvideMultiLog() *log.Logger { return log.New(...) }
//
func (i *Injector) Install(module interface{}) error {
	m := reflect.ValueOf(module)
	im := reflect.Indirect(m)
	// Duplicate module?
	if existing, ok := i.modules[im.Type()]; ok {
		if !reflect.DeepEqual(im.Interface(), existing.Interface()) {
			return fmt.Errorf("duplicate unequal module, %s, installed", im.Type())
		}
		return nil
	}
	i.modules[im.Type()] = im
	if reflect.Indirect(m).Kind() != reflect.Struct {
		return fmt.Errorf("only structs may be used as modules but got %s", m.Type())
	}
	mt := m.Type()
	for j := 0; j < m.NumMethod(); j++ {
		method := m.Method(j)
		methodType := mt.Method(j)
		if strings.HasPrefix(methodType.Name, "Provide") {
			provider := Provider(method.Interface())
			if strings.Contains(methodType.Name, "Mapping") {
				provider = Mapping(provider)
			} else if strings.Contains(methodType.Name, "Sequence") {
				provider = Sequence(provider)
			} else if !strings.Contains(methodType.Name, "Multi") {
				provider = Singleton(provider)
			}
			if err := i.Bind(provider); err != nil {
				return err
			}
		}
	}
	return nil
}

// MustInstall installs a module and panics if it errors.
func (i *Injector) MustInstall(module interface{}) {
	err := i.Install(module)
	if err != nil {
		panic(err)
	}
}

// Bind a value to the injector.
func (i *Injector) Bind(v interface{}) error {
	annotation := Annotate(v)
	binding, err := annotation.Build(i)
	if err != nil {
		return err
	}
	if _, ok := i.bindings[binding.Provides]; ok && !(annotation.Is(&sequenceType{}) ||
		annotation.Is(&mappingType{})) {
		return fmt.Errorf("%s is already bound", binding.Provides)
	}
	i.bindings[binding.Provides] = binding
	return nil
}

// MustBind is like Bind except any errors will cause a panic.
func (i *Injector) MustBind(v interface{}) {
	if err := i.Bind(v); err != nil {
		panic(err)
	}
}

// BindTo binds an interface to a value.
//
// "as" should either be a nil pointer to the required interface:
//
//		i.BindTo((*fmt.Stringer)(nil), impl)
//
// Or a type to convert to:
//
// 		i.BindTo(int64(0), 10)
//
func (i *Injector) BindTo(as interface{}, impl interface{}) error {
	ift := reflect.TypeOf(as)
	binding, err := Annotate(impl).Build(i)
	if err != nil {
		return err
	}
	if _, ok := i.bindings[ift]; ok {
		return fmt.Errorf("%s is already bound", ift)
	}
	// Pointer to an interface...
	if ift.Kind() == reflect.Ptr && ift.Elem().Kind() == reflect.Interface {
		ift = ift.Elem()
		if !binding.Provides.Implements(ift) {
			return fmt.Errorf("implementation %s does not implement interface %s", binding.Provides, ift)
		}
		i.bindings[ift] = binding
	} else if binding.Provides.ConvertibleTo(ift) {
		i.bindings[ift] = &Binding{
			Provides: binding.Provides,
			Requires: binding.Requires,
			Build: func() (interface{}, error) {
				v, err := binding.Build()
				if err != nil {
					return nil, err
				}
				return reflect.ValueOf(v).Convert(ift).Interface(), nil
			},
		}
	} else {
		return fmt.Errorf("implementation %s can not be converted to %s", binding.Provides, ift)
	}
	return nil
}

// MustBindTo is like BindTo except any errors will cause a panic.
func (i *Injector) MustBindTo(iface interface{}, impl interface{}) {
	if err := i.BindTo(iface, impl); err != nil {
		panic(err)
	}
}

func (i *Injector) resolveSlice(t reflect.Type) (*Binding, error) {
	et := t.Elem()
	bindings := []*Binding{}
	for bt, binding := range i.bindings {
		if bt.Kind() == reflect.Slice && bt.Elem().Implements(et) {
			bindings = append(bindings, binding)
		}
	}
	requires := []reflect.Type{}
	for _, binding := range bindings {
		requires = append(requires, binding.Requires...)
	}
	return &Binding{
		Provides: t,
		Requires: requires,
		Build: func() (interface{}, error) {
			out := reflect.MakeSlice(t, 0, 0)
			for _, binding := range bindings {
				fout, err := binding.Build()
				if err != nil {
					return nil, err
				}
				foutv := reflect.ValueOf(fout)
				for i := 0; i < foutv.Len(); i++ {
					out = reflect.Append(out, foutv.Index(i))
				}
			}
			return out.Interface(), nil
		},
	}, nil
}

func (i *Injector) resolveMapping(t reflect.Type) (*Binding, error) {
	et := t.Elem()
	bindings := []*Binding{}
	for bt, binding := range i.bindings {
		if bt.Kind() == reflect.Map && bt.Key() == t.Key() && bt.Elem().Implements(et) {
			bindings = append(bindings, binding)
		}
	}
	requires := []reflect.Type{}
	for _, binding := range bindings {
		requires = append(requires, binding.Requires...)
	}
	return &Binding{
		Provides: t,
		Requires: requires,
		Build: func() (interface{}, error) {
			out := reflect.MakeMap(t)
			for _, binding := range bindings {
				fout, err := binding.Build()
				if err != nil {
					return nil, err
				}
				foutv := reflect.ValueOf(fout)
				for _, key := range foutv.MapKeys() {
					out.SetMapIndex(key, foutv.MapIndex(key))
				}
			}
			return out.Interface(), nil
		},
	}, nil
}

func (i *Injector) resolve(t reflect.Type) (*Binding, error) {
	if binding, ok := i.bindings[t]; ok {
		return binding, nil
	}
	// If type is an interface attempt to find type that conforms to the interface.
	if t.Kind() == reflect.Interface {
		for bt, binding := range i.bindings {
			if bt.Implements(t) {
				return binding, nil
			}
		}
	}
	// If type is a slice of interfaces, attempt to find providers that provide slices
	// of types that implement that interface.
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Interface {
		return i.resolveSlice(t)
	}
	// If type is a map of interface values, attempt to find providers that provide maps of values
	// that implement that interface. Keys must match.
	if t.Kind() == reflect.Map && t.Elem().Kind() == reflect.Interface {
		return i.resolveMapping(t)
	}

	if i.parent != nil {
		return i.parent.resolve(t)
	}
	return &Binding{}, fmt.Errorf("unbound type %s", t.String())
}

// Get acquires a value of type t from the injector.
//
// It is usually preferable to use Call().
func (i *Injector) Get(t reflect.Type) (interface{}, error) {
	binding, err := i.resolve(t)
	if err != nil {
		return nil, err
	}
	// Detect recursive bindings.
	if i.stack[binding.Provides] {
		return nil, fmt.Errorf("recursive binding")
	}
	i.stack[binding.Provides] = true
	defer func() { delete(i.stack, binding.Provides) }()
	return binding.Build()
}

// MustGet is like Get except any errors will cause a panic.
func (i *Injector) MustGet(t reflect.Type) interface{} {
	v, err := i.Get(t)
	if err != nil {
		panic(err)
	}
	return v
}

// Call f, injecting any arguments.
func (i *Injector) Call(f interface{}) ([]interface{}, error) {
	ft := reflect.TypeOf(f)
	args := []reflect.Value{}
	for ai := 0; ai < ft.NumIn(); ai++ {
		a, err := i.Get(ft.In(ai))
		if err != nil {
			return nil, fmt.Errorf("couldn't inject argument %d of %s: %s", ai+1, ft, err)
		}
		args = append(args, reflect.ValueOf(a))
	}
	returns := reflect.ValueOf(f).Call(args)
	last := len(returns) - 1
	if len(returns) > 0 && returns[last].Type() == errorType && !returns[last].IsNil() {
		return nil, returns[last].Interface().(error)
	}
	out := []interface{}{}
	for _, r := range returns {
		out = append(out, r.Interface())
	}
	return out, nil
}

// MustCall calls f, injecting any arguments, and panics if the function errors.
func (i *Injector) MustCall(f interface{}) []interface{} {
	r, err := i.Call(f)
	if err != nil {
		panic(err)
	}
	return r
}

// Child creates a child Injector whose bindings overlay those of the parent.
//
// The parent will never be modified by the child.
func (i *Injector) Child() *Injector {
	c := New()
	c.parent = i
	return c
}

// Validate that the function f can be called by the injector.
func (i *Injector) Validate(f interface{}) error {
	ft := reflect.TypeOf(f)
	if ft.Kind() != reflect.Func {
		return fmt.Errorf("expected a function but received %s", ft)
	}
	// First, check that all existing bindings are satisfiable.
	for _, binding := range i.bindings {
		for _, req := range binding.Requires {
			if _, err := i.resolve(req); err != nil {
				return fmt.Errorf("no binding for %s required by %s: %s", req, binding.Provides, err)
			}
		}
	}
	// Next, check the function arguments are satisfiable.
	for j := 0; j < ft.NumIn(); j++ {
		at := ft.In(j)
		if _, err := i.resolve(at); err != nil {
			return fmt.Errorf("couldn't satisfy argument %d of %s: %s", j, ft, err)
		}
	}
	return nil
}
