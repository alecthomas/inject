# Inject - Guice-ish dependency-injection for Go.
[![](https://godoc.org/github.com/alecthomas/inject?status.svg)](http://godoc.org/github.com/alecthomas/inject) [![Build Status](https://travis-ci.org/alecthomas/inject.png)](https://travis-ci.org/alecthomas/inject) [![Gitter chat](https://badges.gitter.im/alecthomas.png)](https://gitter.im/alecthomas/Lobby)

Inject provides dependency injection for Go. For small Go applications,
manually constructing all required objects is more than sufficient. But for
large, modular code bases, dependency injection can alleviate a lot of
boilerplate.

In particular, Inject provides a simple way of wiring together modular
applications. Each module contains configuration and logic to create the
objects it provides. The main application installs all of these modules, then
calls its main entry point using the injector. The injector resolves any
dependencies of the main function and injects them.

<!-- MarkdownTOC -->

- [Example usage](#example-usage)
- [Value bindings](#value-bindings)
- [Singletons](#singletons)
- [Literals](#literals)
- [Mapping bindings](#mapping-bindings)
- [Sequence bindings](#sequence-bindings)
- [Named bindings](#named-bindings)
- [Interfaces](#interfaces)
- [Modules](#modules)
- [Validation](#validation)

<!-- /MarkdownTOC -->

## Example usage

The following example illustrates a simple modular application.

First, the main package installs configured modules and calls an entry point:

```go
package main

func run(db *mgo.Database, log *log.Logger) {
  log.Println("starting application")
  // ...
}

func main() {
  injector := New()
  injector.Install(
    &MongoModule{URI: "mongodb://db1.example.net,db2.example.net:2500/?replicaSet=test&connectTimeoutMS=300000"""},
    &LoggingModule{Flags: log.Ldate | log.Ltime | log.Llongfile},
  )
  injector.Call(run)
}
```

Next we have a simple Mongo module with a configurable URI:

```go
package db

type MongoModule struct {
  URI string
}

func (m *MongoModule) ProvideMongoDB() (*mgo.Database, error) {
  return mgo.Dial(m.URI)
}
```

The logging package shows idiomatic use of inject; it is just a thin wrapper
around normal Go constructors. This is the least invasive way of using
injection, and preferred.

```go
package logging

// LoggingModule provides a *log.Logger that writes log lines to a Mongo collection.
type LoggingModule struct {
  Flags int
}

func (l *LoggingModule) ProvideMongoLogger(db *mgo.Database) *log.Logger {
  return NewMongoLogger(db, l.Flags)
}

type logEntry struct {
  Text string `bson:"text"`
}

func NewMongoLogger(db *mgo.Database, flags int) *log.Logger {
  return log.New(&mongologWriter{c: db.C("logs")}, "", flags)
}

type mongoLogWriter struct {
  buf string
  c *mgo.Collection
}

func (m *mongoLogWriter) Write(b []byte) (int, error) {
  m.buf = m.buf + string(b)
  for {
    eol := strings.Index(m.buf, "\n")
    if eol == -1 {
      return len(b), nil
    }
    line := m.buf[:eol]
    err := m.c.Insert(&logEntry{line})
    if err != nil {
      return len(b), err
    }
    m.buf = m.buf[eol:]
  }
}
```

## Value bindings

The simplest form of binding simply binds a value directly:

```go
injector.Bind(http.DefaultServeMux)
```

## Singletons

Function bindings are not singleton by default. For example, the following
function binding will be called each time an int is requested:

```go
value := 0
injector.Bind(func() int {
  value++
  return value
})
```

Wrap the function in `Singleton()` to ensure it is called only once:

```go
injector.Bind(Singleton(func() int {
  return 10
}))
```

## Literals

To bind a function as a value, use Literal:

```go
injector.Bind(Literal(fmt.Sprintf))
```

## Mapping bindings

Mappings can be bound explicitly:

```go
injector.Bind(Mapping(map[string]int{"one": 1}))
injector.Bind(Mapping(map[string]int{"two": 2}))
injector.Bind(Mapping(func() map[string]int { return map[string]int{"three": 3} }))
injector.Call(func(m map[string]int) {
  // m == map[string]int{"one": 1, "two": 2, "three": 3}
})
```

Or provided via a Provider method that includes the term `Mapping` in its name:

```go
func (m *MyModule) ProvideStringIntMapping() map[string]int {
  return map[string]int{"one": 1, "two": 2}
}
```

## Sequence bindings

Sequences can be bound explicitly:

```go
injector.Bind(Sequence([]int{1, 2}))
injector.Bind(Sequence([]int{3, 4}))
injector.Bind(Sequence(func() []int { return  []int{5, 6} }))
injector.Call(func(s []int) {
  // s = []int{1, 2, 3, 4, 5, 6}
})
```

Or provided via a Provider method that includes the term `Sequence` in its name:

```go
func (m *MyModule) ProvideIntSequence() []int {
  return []int{1, 2}
}
```

## Named bindings

The equivalent of "named" values can be achieved with type aliases:

```go
type UserName string

injector.Bind(UserName("Bob"))
injector.Call(func (username UserName) {})
```

## Interfaces

Interfaces can be explicitly bound to implementations:

```go
type stringer string
func (s stringer) String() string { return string(s) }

injector.BindTo((*fmt.Stringer)(nil), stringer("hello"))
injector.Call(func(s fmt.Stringer) {
  fmt.Println(s.String())
})
```

However, if an explicit interface binding is not present, any bound object
implementing that interface will be used:

```go
injector.Bind(stringer("hello"))
injector.Call(func(s fmt.Stringer) {
  fmt.Println(s.String())
})
```

Similarly, if sequences/maps of interfaces are injected, explicit bindings
will be used first, then inject will fallback to sequences/maps of objects
implementing that interface.

## Modules

Similar to injection frameworks in other languages, inject includes the
concept of modules. A module is a struct whose methods are providers. This is
useful for grouping configuration data together with providers.

Any method starting with "Provide" will be bound as a Provider. If the method
name contains "Multi" it will not be a singleton provider. If the method name
contains "Sequence" it will contribute to a sequence of its return type.
Similarly, if the method name contains "Mapping" it will contribute to a
mapping of its return type.

```go
type MyModule struct {}

// Singleton provider.
func (m *MyModule) ProvideLog() *log.Logger { return log.New() }

type Randomness int

// "Multi" provider, called every time an "int" is injected.
func (m *MyModule) ProvideMultiRandomness() Randomness { return Randomness(rand.Int()) }
```

## Validation

Finally, after binding all of your types to the injector you can validate that
a function is constructible via the Injector by calling `Validate(f)`.

Or you can live on the edge and simply use `Call(f)` which will return an
error if injection is not possible.
