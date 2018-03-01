package inject

import (
	"reflect"
)

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// Binding represents a function that resolves to a value given a set of input values.
type Binding struct {
	Provides reflect.Type
	Requires []reflect.Type
	Build    func() (interface{}, error)
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

// SafeInjector is an IoC container.
type Injector struct {
	safe *SafeInjector
}

// New creates a new Injector.
//
// An unsafe injector panics on any error. This is commonly used because DI failures are generally not user-recoverable.
//
// The injector itself is already bound, as is an implementation of the Binder interface.
func New() *Injector {
	return &Injector{safe: SafeNew()}
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
	err := i.safe.Install(modules...)
	if err != nil {
		panic(err)
	}
	return i
}

// Bind binds a value to the injector. Panics on error. See the README
// (https://github.com/alecthomas/inject/blob/master/README.md) for more details.
func (i *Injector) Bind(things ...interface{}) Binder {
	if err := i.safe.Bind(things...); err != nil {
		panic(err)
	}
	return i
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
	if err := i.safe.BindTo(iface, impl); err != nil {
		panic(err)
	}
	return i
}

// Get acquires a value of type t from the injector.
//
// It is usually preferable to use Call().
func (i *Injector) Get(t reflect.Type) interface{} {
	v, err := i.safe.Get(t)
	if err != nil {
		panic(err)
	}
	return v
}

// Call calls f, injecting any arguments, and panics if the function errors.
func (i *Injector) Call(f interface{}) []interface{} {
	r, err := i.safe.Call(f)
	if err != nil {
		panic(err)
	}
	return r
}

// Child creates a child Injector whose bindings overlay those of the parent.
//
// The parent will never be modified by the child.
func (i *Injector) Child() *Injector {
	return &Injector{safe: i.safe.Child()}
}

// Validate that the function f can be called by the injector.
func (i *Injector) Validate(f interface{}) error {
	return i.safe.Validate(f)
}

// Safe returns the underlying SafeInjector.
func (i *Injector) Safe() *SafeInjector {
	return i.safe
}
