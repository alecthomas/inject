// Package inject provides dependency injection for Go. For small Go applications, manually
// constructing all required objects is more than sufficient. But for large, modular code bases,
// dependency injection can alleviate a lot of boilerplate.
//
// The following example illustrates a simple modular application.
//
// First, the main package installs configured modules and calls an entry point:
//
// 		package main
//
// 		func run(db *mgo.Database, log *log.Logger) {
// 		  log.Println("starting application")
// 		  // ...
// 		}
//
// 		func main() {
// 		  injector := New()
// 		  injector.Install(
// 		    &MongoModule{URI: "mongodb://db1.example.net,db2.example.net:2500/?replicaSet=test&connectTimeoutMS=300000"""},
// 		    &LoggingModule{Flags: log.Ldate | log.Ltime | log.Llongfile},
// 		  )
// 		  injector.Call(run)
// 		}
//
// Next we have a simple Mongo module with a configurable URI:
//
// 		package db
//
// 		type MongoModule struct {
// 		  URI string
// 		}
//
// 		func (m *MongoModule) ProvideMongoDB() (*mgo.Database, error) {
// 		  return mgo.Dial(m.URI)
// 		}
//
// The logging package shows idiomatic use of inject; it is just a thin wrapper
// around normal Go constructors. This is the least invasive way of using
// injection, and preferred.
//
// 		package logging
//
// 		// LoggingModule provides a *log.Logger that writes log lines to a Mongo collection.
// 		type LoggingModule struct {
// 		  Flags int
// 		}
//
// 		func (l *LoggingModule) ProvideMongoLogger(db *mgo.Database) *log.Logger {
// 		  return NewMongoLogger(db, l.Flags)
// 		}
//
// 		type logEntry struct {
// 		  Text string `bson:"text"`
// 		}
//
// 		func NewMongoLogger(db *mgo.Database, flags int) *log.Logger {
// 		  return log.New(&mongologWriter{c: db.C("logs")}, "", flags)
// 		}
//
// 		type mongoLogWriter struct {
// 		  buf string
// 		  c *mgo.Collection
// 		}
//
// 		func (m *mongoLogWriter) Write(b []byte) (int, error) {
// 		  m.buf = m.buf + string(b)
// 		  for {
// 		    eol := strings.Index(m.buf, "\n")
// 		    if eol == -1 {
// 		      return len(b), nil
// 		    }
// 		    line := m.buf[:eol]
// 		    err := m.c.Insert(&logEntry{line})
// 		    if err != nil {
// 		      return len(b), err
// 		    }
// 		    m.buf = m.buf[eol:]
// 		  }
// 		}
//
// Two interfaces to the injector are provided: SafeInjector and Injector. The former will return error values
// and the latter will panic on any error. The latter is commonly used because DI failures are typically not
// user-recoverable.
//
// See the [README](https://github.com/alecthomas/inject/blob/master/README.md) for more details.
package inject
