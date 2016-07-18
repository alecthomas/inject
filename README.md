# Inject - Reflection-based dependency injection for Go [![](https://godoc.org/github.com/alecthomas/inject?status.svg)](http://godoc.org/github.com/alecthomas/inject) [![Build Status](https://travis-ci.org/alecthomas/inject.png)](https://travis-ci.org/alecthomas/inject)

Example usage:

```go
injector := New()
injector.Bind(http.DefaultServeMux)
injector.Call(func(mux *http.ServeMux) {
})
```

It supports static bindings:

```go
injector.Bind(http.DefaultServeMux)
```

As well as recursive provider functions:

```go
type MongoURI string

injector.Bind(MongoURI("mongodb://db1.example.net,db2.example.net:2500/?replicaSet=test&connectTimeoutMS=300000"))

injector.Bind(func(uri MongoURI) (*mgo.Database, error) {
	s, err := mgo.Dial(string(uri))
	if err != nil {
		return nil, err
	}
	return s.DB("my_db"), nil
})

injector.Bind(func(db *mgo.Database) *mgo.Collection {
	return db.C("my_collection")
})

injector.Call(func(c *mgo.Collection) {
	// ...
})
```

To bind a function as a value, use Literal:

```go
injector.Bind(Literal(fmt.Sprintf))
```

Mapping bindings are supported:

```go
injector.Bind(Mapping("one", 1))
injector.Bind(Mapping("two", 2))
injector.Bind(Mapping("three", func() int { return 3 }))
injector.Call(func(m map[string]int) {
	// ...
})
```

As are sequences:

```go
injector.Bind(Sequence(1))
injector.Bind(Sequence(2))
injector.Bind(Sequence(func() int { return 3 }))
injector.Call(func(s []int) {
	// ...
})
```

The equivalent of "named" values can be achieved with type aliases:

```go
type UserName string

injector.Bind(UserName("Bob"))
injector.Call(func (username UserName) {
})
```

