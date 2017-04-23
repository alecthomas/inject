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

	"github.com/jinzhu/copier"
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

// Binder is an interface allowing bindings to be added.
type Binder interface {
	Bind(things ...interface{}) Binder
	BindTo(to interface{}, impl interface{}) Binder
	Install(module ...interface{}) Binder
}

// A Module implementing this interface will have its Configure() method called at Install() time.
type Module interface {
	Configure(binder Binder) error
}

// New creates a new Injector.
//
// The injector itself is already bound, as is an implementation of the Binder interface.
func New() *Injector {
	i := &Injector{
		bindings: map[reflect.Type]*Binding{},
		stack:    map[reflect.Type]bool{},
		modules:  map[reflect.Type]reflect.Value{},
	}
	i.Bind(i)
	i.BindTo((*Binder)(nil), i)
	return i
}

// SafeInstall is like Install except it returns an error rather than panicking.
func (i *Injector) SafeInstall(modules ...interface{}) (err error) {
	// Capture panics and return them as errors.
	defer func() {
		if e := recover(); e != nil {
			err = e.(error)
		}
	}()
	for _, module := range modules {
		m := reflect.ValueOf(module)
		im := reflect.Indirect(m)
		// Duplicate module?
		if existing, ok := i.modules[im.Type()]; ok {
			return i.handleDuplicate(existing.Addr(), m)
		}
		if module, ok := module.(Module); ok {
			if err := module.Configure(i); err != nil {
				return err
			}
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
				if err := i.SafeBind(provider); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Install a module. A module is a struct whose methods are providers. This is useful for grouping
// configuration data together with providers.
//
// Duplicate modules are allowed as long as all fields are identical or either the existing module,
// or the new module, are zero value.
//
// Any method starting with "Provide" will be bound as a Provider. If the method name contains
// "Multi" it will not be a singleton provider. If the method name contains "Sequence" it must
// return a slice which is merged with slices of the same type. If the method name contains
// "Mapping" it must return a mapping which will be merged with mappings of the same type. Mapping
// and Sequence can not be used simultaneously.
//
// Arguments to provider methods are injected.
//
// For example, the following method will be called only once:
//
// 		ProvideLog() *log.Logger { return log.New(...) }
//
// While this method will be called each time a *log.Logger is injected.
//
// 		ProvideMultiLog() *log.Logger { return log.New(...) }
//
func (i *Injector) Install(modules ...interface{}) Binder {
	err := i.SafeInstall(modules...)
	if err != nil {
		panic(err)
	}
	return i
}

func (i *Injector) handleDuplicate(existing reflect.Value, incoming reflect.Value) error {
	if reflect.DeepEqual(incoming.Interface(), existing.Interface()) {
		return nil
	}
	zero := reflect.New(incoming.Type().Elem()).Interface()
	// Incoming is the zero value, we keep our existing copy.
	if reflect.DeepEqual(incoming.Interface(), zero) {
		return nil
	} else if reflect.DeepEqual(existing.Interface(), zero) {
		return copier.Copy(existing.Interface(), incoming.Interface())
	}
	return fmt.Errorf("duplicate unequal module: %#v != %#v", incoming.Interface(), existing.Interface())
}

// SafeBind binds a value to the injector.
func (i *Injector) SafeBind(things ...interface{}) error {
	for _, v := range things {
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
	}
	return nil
}

// Bind is like SafeBind except any errors will cause a panic.
func (i *Injector) Bind(things ...interface{}) Binder {
	if err := i.SafeBind(things...); err != nil {
		panic(err)
	}
	return i
}

// SafeBindTo is like BindTo except it returns an error rather than panicking.
func (i *Injector) SafeBindTo(as interface{}, impl interface{}) error {
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

// BindTo binds an interface to a value. Panics on error.
//
// "as" should either be a nil pointer to the required interface:
//
//		i.BindTo((*fmt.Stringer)(nil), impl)
//
// Or a type to convert to:
//
// 		i.BindTo(int64(0), 10)
//
func (i *Injector) BindTo(iface interface{}, impl interface{}) Binder {
	if err := i.SafeBindTo(iface, impl); err != nil {
		panic(err)
	}
	return i
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

// SafeGet acquires a value of type t from the injector.
//
// It is usually preferable to use Call().
func (i *Injector) SafeGet(t reflect.Type) (interface{}, error) {
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

// Get is like SafeGet except any errors will cause a panic.
func (i *Injector) Get(t reflect.Type) interface{} {
	v, err := i.SafeGet(t)
	if err != nil {
		panic(err)
	}
	return v
}

// SafeCall f, injecting any arguments.
func (i *Injector) SafeCall(f interface{}) ([]interface{}, error) {
	ft := reflect.TypeOf(f)
	args := []reflect.Value{}
	for ai := 0; ai < ft.NumIn(); ai++ {
		a, err := i.SafeGet(ft.In(ai))
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

// Call calls f, injecting any arguments, and panics if the function errors.
func (i *Injector) Call(f interface{}) []interface{} {
	r, err := i.SafeCall(f)
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
