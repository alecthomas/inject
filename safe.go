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

type SafeBinder interface {
	Bind(things ...interface{}) error
	BindTo(to interface{}, impl interface{}) error
	Install(module ...interface{}) error
}

var _ SafeBinder = &SafeInjector{}

// SafeNew creates a new SafeInjector.
//
// The injector itself is already bound, as is an implementation of the Binder interface.
func SafeNew() *SafeInjector {
	s := &SafeInjector{
		bindings: map[reflect.Type]*Binding{},
		stack:    map[reflect.Type]bool{},
		modules:  map[reflect.Type]reflect.Value{},
	}
	s.Bind(s)
	s.BindTo((*SafeBinder)(nil), s)
	return s
}

func (s *SafeInjector) Unsafe() *Injector {
	return &Injector{safe: s}
}

// Install installs a module. See Injector.Install() for details.
func (s *SafeInjector) Install(modules ...interface{}) (err error) { // nolint: gocyclo
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
		if existing, ok := s.modules[im.Type()]; ok {
			return s.handleDuplicate(existing.Addr(), m)
		}
		if module, ok := module.(Module); ok {
			// Unsafe panics are captured by the enclosing defer().
			unsafe := &Injector{safe: s}
			if err := module.Configure(unsafe); err != nil {
				return err
			}
		}
		s.modules[im.Type()] = im
		if reflect.Indirect(m).Kind() != reflect.Struct {
			return fmt.Errorf("only structs may be used as modules but got %s", m.Type())
		}
		mt := m.Type()
		for j := 0; j < m.NumMethod(); j++ {
			method := m.Method(j)
			methodType := mt.Method(j)
			if strings.HasPrefix(methodType.Name, "Provide") {
				provider := Provider(method.Interface())
				switch {
				case strings.Contains(methodType.Name, "Mapping"):
					provider = Mapping(provider)
				case strings.Contains(methodType.Name, "Sequence"):
					provider = Sequence(provider)
				case !strings.Contains(methodType.Name, "Multi"):
					provider = Singleton(provider)
				}
				if err := s.Bind(provider); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *SafeInjector) handleDuplicate(existing reflect.Value, incoming reflect.Value) error {
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

// Bind binds a value to the injector. See Injector.Bind() for details.
func (s *SafeInjector) Bind(things ...interface{}) error {
	for _, v := range things {
		annotation := Annotate(v)
		binding, err := annotation.Build(s)
		if err != nil {
			return err
		}
		if _, ok := s.bindings[binding.Provides]; ok && !(annotation.Is(&sequenceType{}) ||
			annotation.Is(&mappingType{})) {
			return fmt.Errorf("%s is already bound", binding.Provides)
		}
		s.bindings[binding.Provides] = binding
		s.bindingOrder = append(s.bindingOrder, binding.Provides)
	}
	return nil
}

// BindTo binds an implementation to an interface. See Injector.BindTo() for details.
func (s *SafeInjector) BindTo(as interface{}, impl interface{}) error {
	ift := reflect.TypeOf(as)
	binding, err := Annotate(impl).Build(s)
	if err != nil {
		return err
	}
	if _, ok := s.bindings[ift]; ok {
		return fmt.Errorf("%s is already bound", ift)
	}
	// Pointer to an interface...
	if ift.Kind() == reflect.Ptr && ift.Elem().Kind() == reflect.Interface {
		ift = ift.Elem()
		if !binding.Provides.Implements(ift) {
			return fmt.Errorf("implementation %s does not implement interface %s", binding.Provides, ift)
		}
		s.bindings[ift] = binding
	} else if binding.Provides.ConvertibleTo(ift) {
		s.bindings[ift] = &Binding{
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
	s.bindingOrder = append(s.bindingOrder, ift)
	return nil
}

func (s *SafeInjector) resolveSlice(t reflect.Type) (*Binding, error) {
	et := t.Elem()
	bindings := []*Binding{}
	for _, bt := range s.bindingOrder {
		binding := s.bindings[bt]
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
				for s := 0; s < foutv.Len(); s++ {
					out = reflect.Append(out, foutv.Index(s))
				}
			}
			return out.Interface(), nil
		},
	}, nil
}

func (s *SafeInjector) resolveMapping(t reflect.Type) (*Binding, error) {
	et := t.Elem()
	bindings := []*Binding{}
	for _, bt := range s.bindingOrder {
		binding := s.bindings[bt]
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

func (s *SafeInjector) resolve(t reflect.Type) (*Binding, error) {
	if binding, ok := s.bindings[t]; ok {
		return binding, nil
	}
	// If type is an interface attempt to find type that conforms to the interface.
	if t.Kind() == reflect.Interface {
		for bt, binding := range s.bindings {
			if bt.Implements(t) {
				return binding, nil
			}
		}
	}
	// If type is a slice of interfaces, attempt to find providers that provide slices
	// of types that implement that interface.
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Interface {
		return s.resolveSlice(t)
	}
	// If type is a map of interface values, attempt to find providers that provide maps of values
	// that implement that interface. Keys must match.
	if t.Kind() == reflect.Map && t.Elem().Kind() == reflect.Interface {
		return s.resolveMapping(t)
	}

	if s.parent != nil {
		return s.parent.resolve(t)
	}
	return &Binding{}, fmt.Errorf("unbound type %s", t.String())
}

// Get acquires a value of type t from the injector.
//
// It is usually preferable to use Call().
func (s *SafeInjector) Get(t interface{}) (interface{}, error) {
	return s.getReflected(reflect.TypeOf(t))
}

func (s *SafeInjector) getReflected(t reflect.Type) (interface{}, error) {
	if t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Interface {
		t = t.Elem()
	}
	binding, err := s.resolve(t)
	if err != nil {
		return nil, err
	}
	// Detect recursive bindings.
	if s.stack[binding.Provides] {
		return nil, fmt.Errorf("recursive binding")
	}
	s.stack[binding.Provides] = true
	defer func() { delete(s.stack, binding.Provides) }()
	return binding.Build()
}

// Call f, injecting any arguments.
func (s *SafeInjector) Call(f interface{}) ([]interface{}, error) {
	ft := reflect.TypeOf(f)
	args := []reflect.Value{}
	for ai := 0; ai < ft.NumIn(); ai++ {
		a, err := s.getReflected(ft.In(ai))
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
func (s *SafeInjector) Child() *SafeInjector {
	c := SafeNew()
	c.parent = s
	return c
}

// Validate that the function f can be called by the injector.
func (s *SafeInjector) Validate(f interface{}) error {
	ft := reflect.TypeOf(f)
	if ft.Kind() != reflect.Func {
		return fmt.Errorf("expected a function but received %s", ft)
	}
	// First, check that all existing bindings are satisfiable.
	for _, binding := range s.bindings {
		for _, req := range binding.Requires {
			if _, err := s.resolve(req); err != nil {
				return fmt.Errorf("no binding for %s required by %s: %s", req, binding.Provides, err)
			}
		}
	}
	// Next, check the function arguments are satisfiable.
	for j := 0; j < ft.NumIn(); j++ {
		at := ft.In(j)
		if _, err := s.resolve(at); err != nil {
			return fmt.Errorf("couldn't satisfy argument %d of %s: %s", j, ft, err)
		}
	}
	return nil
}
