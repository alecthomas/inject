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
// Mapping bindings are supported:
//
// 		injector.Bind(Mapping("one", 1))
// 		injector.Bind(Mapping("two", 1))
// 		injector.Call(func(m map[string]int) {
// 			// ...
// 		})
//
// As are sequences:
//
// 		injector.Bind(Sequence(1))
// 		injector.Bind(Sequence(2))
// 		injector.Call(func(s []int) {
// 			// ...
// 		})
//
// The equivalent of "named" values can be achieved with type aliases:
//
// 		type Name string
//
// 		injector.Bind(Name("Bob"))
// 		injector.Get(Name(""))
package inject

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

var errorType = reflect.TypeOf((*error)(nil)).Elem()

type BuilderFunc func() (interface{}, error)

// An Annotation modifies how a type is built and retrieved from the Injector.
type Annotation interface {
	// Build returns the type associated with the value being bound, and a function that builds that
	// value at runtime.
	Build(*Injector) (reflect.Type, BuilderFunc, error)
	// Is checks if the annotation or any children are of the given annotation type.
	Is(annotation Annotation) bool
}

// Annotate ensures that v is an annotation, and returns it.
//
// Specifically:
//
// - If v is already an Annotation it will be returned as-is.
// - If v is a function it will be converted to a Provider().
// - Any other value will become a Literal().
func Annotate(v interface{}) Annotation {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Func {
		return Provider(v)
	}
	if a, ok := v.(Annotation); ok {
		return a
	}
	return Literal(v)
}

func Literal(v interface{}) Annotation {
	return &literalAnnotation{v}
}

type literalAnnotation struct {
	v interface{}
}

func (l *literalAnnotation) String() string {
	return fmt.Sprintf("%v", l.v)
}

func (l *literalAnnotation) Build(*Injector) (reflect.Type, BuilderFunc, error) {
	return reflect.TypeOf(l.v), func() (interface{}, error) { return l.v, nil }, nil
}

func (l *literalAnnotation) Is(annotation Annotation) bool {
	return reflect.TypeOf(annotation) == reflect.TypeOf(&literalAnnotation{})
}

type providerType struct {
	v interface{}
}

// Provider annotates a function to indicate it should be called whenever the type of its return
// value is requested.
func Provider(v interface{}) Annotation {
	return &providerType{v}
}

func (p *providerType) Build(i *Injector) (reflect.Type, BuilderFunc, error) {
	f := reflect.ValueOf(p.v)
	ft := f.Type()
	if ft.Kind() != reflect.Func {
		return nil, nil, fmt.Errorf("provider must be a function returning (<type>[, <error>])")
	}
	rt := ft.Out(0)
	switch ft.NumOut() {
	case 1:
		if rt == errorType {
			return nil, nil, fmt.Errorf("provider must return (<type>[, <error>])")
		}
		return rt, func() (interface{}, error) {
			rv, err := i.Call(p.v)
			if err != nil {
				return nil, err
			}
			return rv[0], nil
		}, nil
	case 2:
		if ft.Out(1) != errorType {
			return nil, nil, fmt.Errorf("provider must return (<type>[, <error>])")
		}
		return rt, func() (interface{}, error) {
			rv, err := i.Call(p.v)
			if err != nil {
				return nil, err
			}
			if rv[1] != nil {
				return nil, rv[1].(error)
			}
			return rv[0], nil
		}, nil
	}
	return nil, nil, fmt.Errorf("provider must return (<type>[, <error>])")
}

func (p *providerType) Is(annotation Annotation) bool {
	return reflect.TypeOf(annotation) == reflect.TypeOf(&providerType{})
}

// Singleton annotates a provider function to indicate that the provider will only be called once,
// and that its return value will be used for all subsequent retrievals of the given type.
//
//		count := 0
// 		injector.Bind(Singleton(func() int {
// 			count++
// 			return 123
// 		}))
// 		injector.Get(reflect.TypeOf(1))
// 		injector.Get(reflect.TypeOf(1))
// 		assert.Equal(t, 1, count)
//
func Singleton(v interface{}) Annotation {
	return &singletonType{v}
}

type singletonType struct {
	v interface{}
}

func (s *singletonType) Build(i *Injector) (reflect.Type, BuilderFunc, error) {
	next := Annotate(s.v)
	if !next.Is(&providerType{}) {
		return nil, nil, fmt.Errorf("only providers can be singletons")
	}
	typ, builder, err := next.Build(i)
	if err != nil {
		return nil, nil, err
	}
	lock := sync.Mutex{}
	isCached := false
	var cached interface{}
	return typ, func() (interface{}, error) {
		lock.Lock()
		defer lock.Unlock()
		var err error
		if !isCached {
			cached, err = builder()
			isCached = true
		}
		return cached, err
	}, nil
}

func (s *singletonType) Is(annotation Annotation) bool {
	return reflect.TypeOf(annotation) == reflect.TypeOf(&singletonType{}) ||
		Annotate(s.v).Is(annotation)
}

// Sequence annotates a provider or binding to indicate it is part of a slice of values implementing
// the given type.
//
// 		injector.Bind(Sequence([]int{1}))
// 		injector.Bind(Sequence([]int{2}))
//
//		expected := []int{1, 2}
//		actual := injector.Get(reflect.TypeOf([]int{}))
// 		assert.Equal(t, actual, expected)
//
func Sequence(v interface{}) Annotation {
	return &sequenceType{v}
}

type sequenceType struct {
	v interface{}
}

func (s *sequenceType) Build(i *Injector) (reflect.Type, BuilderFunc, error) {
	t, builder, err := Annotate(s.v).Build(i)
	if err != nil {
		return nil, nil, err
	}
	if t.Kind() != reflect.Slice {
		return nil, nil, fmt.Errorf("Sequence() must be bound to a slice not %s", t)
	}
	next, ok := i.Bindings[t]
	return t, func() (interface{}, error) {
		out := reflect.MakeSlice(t, 0, 0)
		if ok {
			v, err := next()
			if err != nil {
				return nil, err
			}
			out = reflect.AppendSlice(out, reflect.ValueOf(v))
		}
		v, err := builder()
		if err != nil {
			return nil, err
		}
		out = reflect.AppendSlice(out, reflect.ValueOf(v))
		return out.Interface(), nil
	}, nil
}

func (s *sequenceType) Is(annotation Annotation) bool {
	return reflect.TypeOf(annotation) == reflect.TypeOf(&sequenceType{}) ||
		Annotate(s.v).Is(annotation)
}

type mappingType struct {
	v interface{}
}

// Mapping annotates a provider or binding to indicate it is part of a mapping of keys to values.
//
//		injector.Bind(Mapping(map[string]int{"one": 1}))
//		injector.Bind(Mapping(map[string]int{"two": 2}))
//		injector.Provide(Mapping(func() map[string]int { return map[string]int{"three": 3} }))
//
// 		expected := map[string]int{"one": 1, "two": 2, "three": 3}
// 		actual := injector.Get(reflect.TypeOf(map[string]int{}))
// 		assert.Equal(t, actual, expected)
func Mapping(v interface{}) Annotation {
	return &mappingType{v}
}

func (m *mappingType) Build(i *Injector) (reflect.Type, BuilderFunc, error) {
	t, builder, err := Annotate(m.v).Build(i)
	if err != nil {
		return nil, nil, err
	}
	if t.Kind() != reflect.Map {
		return nil, nil, fmt.Errorf("Mapping() must be bound to a map not %s", t)
	}
	// Previous mapping binding. Capture it and merge when requested.
	prev, havePrev := i.Bindings[t]
	return t, func() (interface{}, error) {
		out := reflect.MakeMap(t)
		if havePrev {
			v, err := prev()
			if err != nil {
				return nil, err
			}
			prevMap := reflect.ValueOf(v)
			for _, k := range prevMap.MapKeys() {
				out.SetMapIndex(k, prevMap.MapIndex(k))
			}
		}
		v, err := builder()
		if err != nil {
			return nil, err
		}
		nextMap := reflect.ValueOf(v)
		for _, k := range nextMap.MapKeys() {
			out.SetMapIndex(k, nextMap.MapIndex(k))
		}
		return out.Interface(), nil
	}, nil
}

func (m *mappingType) Is(annotation Annotation) bool {
	return reflect.TypeOf(annotation) == reflect.TypeOf(&mappingType{})
}

// Config for an Injector.
type Config struct {
	// If true, empty sequences will be implicitly provided.
	ImplicitSequences bool
	// If true, empty mappings will be implicitly provided.
	ImplicitMappings bool
}

// Injector is a IoC container.
type Injector struct {
	Parent   *Injector
	Bindings map[reflect.Type]BuilderFunc
	Config   Config
}

// New creates a new Injector.
//
// The injector itself is already bound.
func New() *Injector {
	i := &Injector{
		Bindings: map[reflect.Type]BuilderFunc{},
	}
	i.Bind(i)
	return i
}

// Configure the injector.
func (i *Injector) Configure(config Config) *Injector {
	i.Config = config
	return i
}

// Install a module. A module is a struct whose methods are providers. This is useful for grouping
// configuration data together with providers.
//
// Any method starting with "Provide" will be bound as a Provider. If the method name contains
// "Multi" it will not be a singleton provider. If the method name contains "Sequence" it will
// contribute to a sequence of its return type.
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

func (i *Injector) MustInstall(module interface{}) {
	err := i.Install(module)
	if err != nil {
		panic(err)
	}
}

// Bind a value to the injector.
func (i *Injector) Bind(v interface{}) error {
	annotation := Annotate(v)
	typ, provider, err := annotation.Build(i)
	if err != nil {
		return err
	}
	if _, ok := i.Bindings[typ]; ok && !(annotation.Is(&sequenceType{}) ||
		annotation.Is(&mappingType{})) {
		return fmt.Errorf("%s is already bound", typ)
	}
	i.Bindings[typ] = provider
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
	implt, builder, err := Annotate(impl).Build(i)
	if err != nil {
		return err
	}
	if _, ok := i.Bindings[ift]; ok {
		return fmt.Errorf("%s is already bound", ift)
	}
	// Pointer to an interface...
	if ift.Kind() == reflect.Ptr && ift.Elem().Kind() == reflect.Interface {
		ift = ift.Elem()
		if !implt.Implements(ift) {
			return fmt.Errorf("implementation %s does not implement interface %s", implt, ift)
		}
		i.Bindings[ift] = builder
	} else if implt.ConvertibleTo(ift) {
		i.Bindings[ift] = func() (interface{}, error) {
			v, err := builder()
			if err != nil {
				return nil, err
			}
			return reflect.ValueOf(v).Convert(ift).Interface(), nil
		}
	} else {
		return fmt.Errorf("implementation %s can not be converted to %s", implt, ift)
	}
	return nil
}

// MustBindTo is like BindTo except any errors will cause a panic.
func (i *Injector) MustBindTo(iface interface{}, impl interface{}) {
	if err := i.BindTo(iface, impl); err != nil {
		panic(err)
	}
}

// Get acquires a value of type t from the injector.
//
// This should generally only be used for testing.
func (i *Injector) Get(t reflect.Type) (interface{}, error) {
	if f, ok := i.Bindings[t]; ok {
		return f()
	}
	// If type is an interface attempt to find type that conforms to the interface.
	if t.Kind() == reflect.Interface {
		for bt, f := range i.Bindings {
			if bt.Implements(t) {
				return f()
			}
		}
	}
	// If type is a slice of interfaces, attempt to find providers that provide slices
	// of types that implement that interface.
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Interface {
		et := t.Elem()
		for bt, f := range i.Bindings {
			if bt.Kind() == reflect.Slice {
				if bt.Elem().Implements(et) {
					fout, err := f()
					if err != nil {
						return nil, err
					}
					foutv := reflect.ValueOf(fout)
					out := reflect.MakeSlice(t, 0, 0)
					for i := 0; i < foutv.Len(); i++ {
						out = reflect.Append(out, foutv.Index(i))
					}
					return out.Interface(), nil
				}
			}
		}
	}
	// If type is a map of interface values, attempt to find providers that provide maps of values
	// that implement that interface. Keys must match.
	if t.Kind() == reflect.Map && t.Elem().Kind() == reflect.Interface {
		et := t.Elem()
		for bt, f := range i.Bindings {
			if bt.Kind() == reflect.Map && bt.Key() == t.Key() {
				if bt.Elem().Implements(et) {
					fout, err := f()
					if err != nil {
						return nil, err
					}
					foutv := reflect.ValueOf(fout)
					out := reflect.MakeMap(t)
					for _, key := range foutv.MapKeys() {
						out.SetMapIndex(key, foutv.MapIndex(key))
					}
					return out.Interface(), nil
				}
			}
		}
	}

	// Special case slices to always return something... this allows sequences to be injected
	// when they don't have any providers.
	if i.Config.ImplicitSequences && t.Kind() == reflect.Slice {
		return reflect.MakeSlice(t, 0, 0).Interface(), nil
	}
	// Special case maps to always return something... this allows mappings to be injected
	// when they don't have any providers.
	if i.Config.ImplicitMappings && t.Kind() == reflect.Map {
		return reflect.MakeMap(t).Interface(), nil
	}
	if i.Parent != nil {
		return i.Parent.Get(t)
	}
	return nil, fmt.Errorf("unbound type %s", t.String())
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
	c.Parent = i
	return c
}
