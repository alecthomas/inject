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
	Build(*Injector) (*Binding, error)
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

// Literal annotates a value as being provided as-is with no further transformation.
func Literal(v interface{}) Annotation {
	return &literalAnnotation{v}
}

type literalAnnotation struct {
	v interface{}
}

func (l *literalAnnotation) String() string {
	return fmt.Sprintf("%v", l.v)
}

func (l *literalAnnotation) Build(*Injector) (*Binding, error) {
	return &Binding{
		Provides: reflect.TypeOf(l.v),
		Build:    func() (interface{}, error) { return l.v, nil },
	}, nil
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

func (p *providerType) Build(i *Injector) (*Binding, error) {
	f := reflect.ValueOf(p.v)
	ft := f.Type()
	if ft.Kind() != reflect.Func {
		return &Binding{}, fmt.Errorf("provider must be a function returning (<type>[, <error>])")
	}
	rt := ft.Out(0)
	inputs := []reflect.Type{}
	for i := 0; i < ft.NumIn(); i++ {
		inputs = append(inputs, ft.In(i))
	}
	switch ft.NumOut() {
	case 1:
		if rt == errorType {
			return &Binding{}, fmt.Errorf("provider must return (<type>[, <error>])")
		}
		return &Binding{
			Provides: rt,
			Requires: inputs,
			Build: func() (interface{}, error) {
				rv, err := i.Call(p.v)
				if err != nil {
					return nil, err
				}
				return rv[0], nil
			},
		}, nil
	case 2:
		if ft.Out(1) != errorType {
			return &Binding{}, fmt.Errorf("provider must return (<type>[, <error>])")
		}
		return &Binding{
			Provides: rt,
			Requires: inputs,
			Build: func() (interface{}, error) {
				rv, err := i.Call(p.v)
				if err != nil {
					return nil, err
				}
				if rv[1] != nil {
					return nil, rv[1].(error)
				}
				return rv[0], nil
			},
		}, nil
	}
	return &Binding{}, fmt.Errorf("provider must return (<type>[, <error>])")
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

func (s *singletonType) Build(i *Injector) (*Binding, error) {
	next := Annotate(s.v)
	if !next.Is(&providerType{}) {
		return &Binding{}, fmt.Errorf("only providers can be singletons")
	}
	builder, err := next.Build(i)
	if err != nil {
		return &Binding{}, err
	}
	lock := sync.Mutex{}
	isCached := false
	var cached interface{}
	return &Binding{
		Provides: builder.Provides,
		Requires: builder.Requires,
		Build: func() (interface{}, error) {
			lock.Lock()
			defer lock.Unlock()
			var err error
			if !isCached {
				cached, err = builder.Build()
				isCached = true
			}
			return cached, err
		},
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

func (s *sequenceType) Build(i *Injector) (*Binding, error) {
	binding, err := Annotate(s.v).Build(i)
	if err != nil {
		return &Binding{}, err
	}
	if binding.Provides.Kind() != reflect.Slice {
		return &Binding{}, fmt.Errorf("Sequence() must be bound to a slice not %s", binding.Provides)
	}
	next, ok := i.bindings[binding.Provides]
	return &Binding{
		Provides: binding.Provides,
		Requires: binding.Requires,
		Build: func() (interface{}, error) {
			out := reflect.MakeSlice(binding.Provides, 0, 0)
			if ok {
				v, err := next.Build()
				if err != nil {
					return nil, err
				}
				out = reflect.AppendSlice(out, reflect.ValueOf(v))
			}
			v, err := binding.Build()
			if err != nil {
				return nil, err
			}
			out = reflect.AppendSlice(out, reflect.ValueOf(v))
			return out.Interface(), nil
		},
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

func (m *mappingType) Build(i *Injector) (*Binding, error) {
	binding, err := Annotate(m.v).Build(i)
	if err != nil {
		return &Binding{}, err
	}
	if binding.Provides.Kind() != reflect.Map {
		return &Binding{}, fmt.Errorf("Mapping() must be bound to a map not %s", binding.Provides)
	}
	// Previous mapping binding. Capture it and merge when requested.
	prev, havePrev := i.bindings[binding.Provides]
	return &Binding{
		Provides: binding.Provides,
		Requires: binding.Requires,
		Build: func() (interface{}, error) {
			out := reflect.MakeMap(binding.Provides)
			if havePrev {
				v, err := prev.Build()
				if err != nil {
					return nil, err
				}
				prevMap := reflect.ValueOf(v)
				for _, k := range prevMap.MapKeys() {
					out.SetMapIndex(k, prevMap.MapIndex(k))
				}
			}
			v, err := binding.Build()
			if err != nil {
				return nil, err
			}
			nextMap := reflect.ValueOf(v)
			for _, k := range nextMap.MapKeys() {
				out.SetMapIndex(k, nextMap.MapIndex(k))
			}
			return out.Interface(), nil
		},
	}, nil
}

func (m *mappingType) Is(annotation Annotation) bool {
	return reflect.TypeOf(annotation) == reflect.TypeOf(&mappingType{})
}
