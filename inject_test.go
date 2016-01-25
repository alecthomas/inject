package inject

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInjectorBind(t *testing.T) {
	i := New()
	i.MustBind("hello")
	require.Equal(t, "hello", i.MustGet(reflect.TypeOf("")))
}

type stringer string

func (s stringer) String() string {
	return string(s)
}

func TestInjectorBindTo(t *testing.T) {
	i := New()
	s := stringer("hello")
	i.BindTo((*fmt.Stringer)(nil), s)
	ss := i.MustGet(reflect.TypeOf((*fmt.Stringer)(nil)).Elem()).(fmt.Stringer)
	require.Equal(t, "hello", ss.String())
}

func TestInjectorBindToInvalidImplementation(t *testing.T) {
	i := New()
	s := "hello"
	err := i.BindTo((*fmt.Stringer)(nil), s)
	require.Error(t, err)
}

func TestGetUnboundType(t *testing.T) {
	i := New()
	_, err := i.Get(reflect.TypeOf(""))
	require.Error(t, err)
}

func TestProvider(t *testing.T) {
	i := New()
	i.MustBind(func() string { return "hello" })
	i.MustBind(func() int { return 123 })
	sv := i.MustGet(reflect.TypeOf(""))
	require.Equal(t, "hello", sv)
	iv := i.MustGet(reflect.TypeOf(1))
	require.Equal(t, 123, iv)
}

func TestProviderGraph(t *testing.T) {
	i := New()
	i.MustBind(func() int { return 123 })
	i.MustBind(func(n int) string { return fmt.Sprintf("hello:%d", n) })
	sv := i.MustGet(reflect.TypeOf(""))
	require.Equal(t, "hello:123", sv)
}

func TestChildInjector(t *testing.T) {
	i := New()
	i.MustBind(func() string { return "hello" })
	c := i.Child()
	c.MustBind(func() int { return 123 })
	sv := c.MustGet(reflect.TypeOf(""))
	require.Equal(t, "hello", sv)
	iv := c.MustGet(reflect.TypeOf(1))
	require.Equal(t, 123, iv)
}

func TestInjectorCall(t *testing.T) {
	i := New()
	i.MustBind("hello")
	i.MustBind(123)
	as := ""
	ai := 0
	i.Call(func(s string, i int) {
		as = s
		ai = i
	})
	require.Equal(t, "hello", as)
	require.Equal(t, 123, ai)
}

func TestSingletonAnnotation(t *testing.T) {
	i := New()
	calls := 0
	i.MustBind(Singleton(func() string {
		calls++
		return "hello"
	}))
	i.MustGet(reflect.TypeOf(""))
	i.MustGet(reflect.TypeOf(""))
	require.Equal(t, 1, calls)
}

func TestSingletonToNonProviderPanics(t *testing.T) {
	i := New()
	require.Panics(t, func() {
		i.MustBind(Singleton(1))
	})
}

func TestDynamicInjection(t *testing.T) {
	i := New()
	called := 0
	i.MustBind(func() *string {
		called++
		s := new(string)
		*s = fmt.Sprintf("hello:%d", called)
		return s
	})
	p := new(string)
	a := i.MustGet(reflect.TypeOf(p))
	b := i.MustGet(reflect.TypeOf(p))
	require.NotEqual(t, a, b)
	require.Equal(t, 2, called)
}

func TestSequenceAnnotation(t *testing.T) {
	i := New()
	i.MustBind(Sequence(1))
	i.MustBind(Sequence(2))
	i.MustBind(Sequence(func() int { return 3 }))
	v := i.MustGet(reflect.TypeOf([]int{}))
	require.Equal(t, []int{1, 2, 3}, v)
}

func TestMappingAnnotation(t *testing.T) {
	i := New()
	i.MustBind(Mapping("one", 1))
	i.MustBind(Mapping("two", 2))
	i.MustBind(Mapping("three", func() int { return 3 }))
	v := i.MustGet(reflect.TypeOf(map[string]int{}))
	require.Equal(t, map[string]int{"one": 1, "two": 2, "three": 3}, v)
	called := false
	i.Call(func(m map[string]int) {
		called = true
		require.Equal(t, map[string]int{"one": 1, "two": 2, "three": 3}, m)
	})
	require.True(t, called)
}

func TestLiteral(t *testing.T) {
	i := New()
	buf := bytes.Buffer{}
	i.MustBind(Literal(buf.WriteString))
	i.Call(func(write func(string) (int, error)) {
		write("hello world")
	})
	require.Equal(t, "hello world", buf.String())
}

type Username string

func TestPseudoBoundValues(t *testing.T) {
	i := New()
	i.MustBind(Username("bob"))
	name := ""
	i.Call(func(user Username) {
		name = string(user)
	})
	require.Equal(t, "bob", name)
}
