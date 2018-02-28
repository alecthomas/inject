package inject

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/jinzhu/copier"
)

// SafeInjector is an IoC container.
type SafeInjector struct {
	parent       *SafeInjector
	bindings     map[reflect.Type]*Binding
	bindingOrder []reflect.Type
	stack        map[reflect.Type]bool
	modules      map[reflect.Type]reflect.Value
}

// SafeNew creates a new SafeInjector.
//
// The injector itself is already bound, as is an implementation of the Binder interface.
func SafeNew() *SafeInjector {
	i := &SafeInjector{
		bindings: map[reflect.Type]*Binding{},
		stack:    map[reflect.Type]bool{},
		modules:  map[reflect.Type]reflect.Value{},
	}
	i.Bind(i)
	i.BindTo((*Binder)(nil), i)
	return i
}

// Install is like Install except it returns an error rather than panicking.
func (i *SafeInjector) Install(modules ...interface{}) (err error) {
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
			// Unsafe panics are captured by the enclosing defer().
			unsafe := &Injector{safe: i}
			if err := module.Configure(unsafe); err != nil {
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
				if err := i.Bind(provider); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (i *SafeInjector) handleDuplicate(existing reflect.Value, incoming reflect.Value) error {
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

// Bind binds a value to the injector.
func (i *SafeInjector) Bind(things ...interface{}) error {
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
		i.bindingOrder = append(i.bindingOrder, binding.Provides)
	}
	return nil
}

// BindTo is like BindTo except it returns an error rather than panicking.
func (i *SafeInjector) BindTo(as interface{}, impl interface{}) error {
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
	i.bindingOrder = append(i.bindingOrder, ift)
	return nil
}

func (i *SafeInjector) resolveSlice(t reflect.Type) (*Binding, error) {
	et := t.Elem()
	bindings := []*Binding{}
	for _, bt := range i.bindingOrder {
		binding := i.bindings[bt]
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

func (i *SafeInjector) resolveMapping(t reflect.Type) (*Binding, error) {
	et := t.Elem()
	bindings := []*Binding{}
	for _, bt := range i.bindingOrder {
		binding := i.bindings[bt]
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

func (i *SafeInjector) resolve(t reflect.Type) (*Binding, error) {
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
func (i *SafeInjector) Get(t reflect.Type) (interface{}, error) {
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

// Call f, injecting any arguments.
func (i *SafeInjector) Call(f interface{}) ([]interface{}, error) {
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

// Child creates a child SafeInjector whose bindings overlay those of the parent.
//
// The parent will never be modified by the child.
func (i *SafeInjector) Child() *SafeInjector {
	c := SafeNew()
	c.parent = i
	return c
}

// Validate that the function f can be called by the injector.
func (i *SafeInjector) Validate(f interface{}) error {
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
