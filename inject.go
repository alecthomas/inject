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
	"sync"
)

var errorType = reflect.TypeOf((*error)(nil)).Elem()

type BuilderFunc func() (interface{}, error)

// An Annotation modifies how a type is built and retrieved from the Injector.
type Annotation interface {
	// Build returns the type associated with the value being bound, and a function that builds that
	// value at runtime.
	Build(*Injector) (reflect.Type, BuilderFunc, error)
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
			return rv[0].Interface(), nil
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
			if !rv[1].IsNil() {
				return nil, rv[1].Interface().(error)
			}
			return rv[0].Interface(), nil
		}, nil
	}
	return nil, nil, fmt.Errorf("provider must return (<type>[, <error>])")
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
	if _, ok := next.(*providerType); !ok {
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

// Sequence annotates a provider or binding to indicate it is part of a slice of values implementing
// the given type.
//
// 		injector.Bind(Sequence(1))
// 		injector.Bind(Sequence(2))
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
	sliceType := reflect.SliceOf(t)
	next, ok := i.Bindings[sliceType]
	return sliceType, func() (interface{}, error) {
		out := reflect.MakeSlice(sliceType, 0, 0)
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
		out = reflect.Append(out, reflect.ValueOf(v))
		return out.Interface(), nil
	}, nil
}

type mappingType struct {
	k interface{}
	v interface{}
}

// Mapping annotates a provider or binding to indicate it is part of a mapping of keys to values.
//
//		injector.Bind(Mapping("one", 1))
//		injector.Bind(Mapping("two", 2))
//		injector.Provide(Mapping("three", func() int { return 3 }))
//
// 		expected := map[string]int{"one": 1, "two": 2, "three": 3}
// 		actual := injector.Get(reflect.TypeOf(map[string]int{}))
// 		assert.Equal(t, actual, expected)
func Mapping(k, v interface{}) Annotation {
	return &mappingType{k, v}
}

func (m *mappingType) Build(i *Injector) (reflect.Type, BuilderFunc, error) {
	t, builder, err := Annotate(m.v).Build(i)
	if err != nil {
		return nil, nil, err
	}
	kv := reflect.ValueOf(m.k)
	mapType := reflect.MapOf(reflect.TypeOf(m.k), t)
	next, ok := i.Bindings[mapType]
	return mapType, func() (interface{}, error) {
		var out reflect.Value
		if ok {
			v, err := next()
			if err != nil {
				return nil, err
			}
			out = reflect.ValueOf(v)
		} else {
			out = reflect.MakeMap(mapType)
		}
		v, err := builder()
		if err != nil {
			return nil, err
		}
		out.SetMapIndex(kv, reflect.ValueOf(v))
		return out.Interface(), nil
	}, nil
}

// Injector is a IoC container.
type Injector struct {
	Parent   *Injector
	Bindings map[reflect.Type]BuilderFunc
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

// Bind a value to the injector.
func (i *Injector) Bind(v interface{}) error {
	typ, provider, err := Annotate(v).Build(i)
	if err != nil {
		return err
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
// "iface" must be a nil pointer to the required interface. eg.
//
//		i.BindTo((*fmt.Stringer)(nil), impl)
//
func (i *Injector) BindTo(iface interface{}, impl interface{}) error {
	ift := reflect.TypeOf(iface).Elem()
	implt, builder, err := Annotate(impl).Build(i)
	if err != nil {
		return err
	}
	if !implt.Implements(ift) {
		return fmt.Errorf("implementation %s does not implement interface %s", implt, ift)
	}
	i.Bindings[ift] = builder
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
func (i *Injector) Call(f interface{}) ([]reflect.Value, error) {
	ft := reflect.TypeOf(f)
	args := []reflect.Value{}
	for ai := 0; ai < ft.NumIn(); ai++ {
		a, err := i.Get(ft.In(ai))
		if err != nil {
			return nil, fmt.Errorf("couldn't inject argument %d of %s: %s", ai, ft, err)
		}
		args = append(args, reflect.ValueOf(a))
	}
	return reflect.ValueOf(f).Call(args), nil
}

// Child creates a child Injector whose bindings overlay those of the parent.
//
// The parent will never be modified by the child.
func (i *Injector) Child() *Injector {
	c := New()
	c.Parent = i
	return c
}
