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

// An Annotation modifies how a type is built and retrieved from the Injector.
type Annotation interface {
	// Build returns the type associated with the value being bound, and a function that builds that
	// value at runtime.
	Build(*Injector) (reflect.Type, func() interface{})
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

func (l *literalAnnotation) Build(*Injector) (reflect.Type, func() interface{}) {
	return reflect.TypeOf(l.v), func() interface{} { return l.v }
}

type providerType struct {
	v interface{}
}

// Provider annotates a function to indicate it should be called whenever the type of its return
// value is requested.
func Provider(v interface{}) Annotation {
	return &providerType{v}
}

func (p *providerType) Build(i *Injector) (reflect.Type, func() interface{}) {
	f := reflect.ValueOf(p.v)
	ft := f.Type()
	if ft.Kind() != reflect.Func {
		panic("provider must be a function")
	}
	if ft.NumOut() != 1 {
		panic("provider must return exactly one value")
	}
	rt := ft.Out(0)
	return rt, func() interface{} {
		return i.Call(p.v)[0].Interface()
	}
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

func (s *singletonType) Build(i *Injector) (reflect.Type, func() interface{}) {
	next := Annotate(s.v)
	if _, ok := next.(*providerType); !ok {
		panic("only providers can be singletons")
	}
	typ, builder := next.Build(i)
	lock := sync.Mutex{}
	isCached := false
	var cached interface{}
	return typ, func() interface{} {
		lock.Lock()
		defer lock.Unlock()
		if !isCached {
			cached = builder()
			isCached = true
		}
		return cached
	}
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

func (s *sequenceType) Build(i *Injector) (reflect.Type, func() interface{}) {
	t, builder := Annotate(s.v).Build(i)
	sliceType := reflect.SliceOf(t)
	next, ok := i.Bindings[sliceType]
	return sliceType, func() interface{} {
		out := reflect.MakeSlice(sliceType, 0, 0)
		if ok {
			out = reflect.AppendSlice(out, reflect.ValueOf(next()))
		}
		out = reflect.Append(out, reflect.ValueOf(builder()))
		return out.Interface()
	}
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

func (m *mappingType) Build(i *Injector) (reflect.Type, func() interface{}) {
	t, builder := Annotate(m.v).Build(i)
	kv := reflect.ValueOf(m.k)
	mapType := reflect.MapOf(reflect.TypeOf(m.k), t)
	next, ok := i.Bindings[mapType]
	return mapType, func() interface{} {
		var out reflect.Value
		if ok {
			out = reflect.ValueOf(next())
		} else {
			out = reflect.MakeMap(mapType)
		}
		out.SetMapIndex(kv, reflect.ValueOf(builder()))
		return out.Interface()
	}
}

// Injector is a IoC container.
type Injector struct {
	Parent   *Injector
	Bindings map[reflect.Type]func() interface{}
}

// New creates a new Injector.
//
// The injector itself is already bound.
func New() *Injector {
	i := &Injector{
		Bindings: map[reflect.Type]func() interface{}{},
	}
	i.Bind(i)
	return i
}

// Bind a value to the injector.
func (i *Injector) Bind(v interface{}) {
	typ, provider := Annotate(v).Build(i)
	i.Bindings[typ] = provider
}

// BindTo binds an interface to a value.
//
// "iface" must be a nil pointer to the required interface. eg.
//
//		i.BindTo((*fmt.Stringer)(nil), impl)
//
func (i *Injector) BindTo(iface interface{}, impl interface{}) {
	ift := reflect.TypeOf(iface).Elem()
	implt, builder := Annotate(impl).Build(i)
	if !implt.Implements(ift) {
		panic("implementation does not implement interface")
	}
	i.Bindings[ift] = builder
}

// Get acquires a value of type t from the injector.
//
// This should generally only be used for testing.
func (i *Injector) Get(t reflect.Type) interface{} {
	if f, ok := i.Bindings[t]; ok {
		return f()
	}
	if i.Parent != nil {
		return i.Parent.Get(t)
	}
	panic("unbound type " + t.String())
}

// Call f, injecting any arguments.
func (i *Injector) Call(f interface{}) []reflect.Value {
	ft := reflect.TypeOf(f)
	args := []reflect.Value{}
	for ai := 0; ai < ft.NumIn(); ai++ {
		a := i.Get(ft.In(ai))
		args = append(args, reflect.ValueOf(a))
	}
	return reflect.ValueOf(f).Call(args)
}

// Child creates a child Injector whose bindings overlay those of the parent.
//
// The parent will never be modified by the child.
func (i *Injector) Child() *Injector {
	c := New()
	c.Parent = i
	return c
}
