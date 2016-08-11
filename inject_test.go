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

func TestInjectorBindToTypeAlias(t *testing.T) {
	i := New()
	i.MustBindTo(stringer(""), "hello")
	v := i.MustGet(reflect.TypeOf(stringer(""))).(stringer)
	require.Equal(t, stringer("hello"), v)
	i.MustBindTo(int64(0), 10)
	w := i.MustGet(reflect.TypeOf(int64(0)))
	require.Equal(t, int64(10), w)
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
	i.MustBind(Sequence([]int{1}))
	i.MustBind(Sequence([]int{2}))
	i.MustBind(Sequence(Singleton(func() []int { return []int{3} })))
	v := i.MustGet(reflect.TypeOf([]int{}))
	require.Equal(t, []int{1, 2, 3}, v)
}

func TestMappingAnnotation(t *testing.T) {
	i := New()
	i.MustBind(Mapping(map[string]int{"one": 1}))
	i.MustBind(Mapping(map[string]int{"two": 2}))
	i.MustBind(Mapping(func() map[string]int { return map[string]int{"three": 3} }))
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

type UserName string

func TestPseudoBoundValues(t *testing.T) {
	i := New()
	i.MustBind(UserName("bob"))
	name := ""
	i.Call(func(user UserName) {
		name = string(user)
	})
	require.Equal(t, "bob", name)
}

type myModule struct{}

func (m *myModule) ProvideString(i int) string { return fmt.Sprintf("hello:%d", i) }

func TestModule(t *testing.T) {
	i := New()
	i.MustBind(123)
	i.MustInstall(&myModule{})
	actual := i.MustGet(reflect.TypeOf("")).(string)
	require.Equal(t, "hello:123", actual)
}

func TestCallError(t *testing.T) {
	f := func() error {
		return fmt.Errorf("failed")
	}
	i := New()
	_, err := i.Call(f)
	require.Error(t, err)
}

type notQuiteStringer int

func (n notQuiteStringer) String() string { return fmt.Sprintf("%d", n) }

func TestInterfaceConversion(t *testing.T) {
	f := func(s fmt.Stringer) error {
		return nil
	}
	i := New()
	i.MustBind(notQuiteStringer(10))
	_, err := i.Call(f)
	require.NoError(t, err)
}

func TestSliceInterfaceConversion(t *testing.T) {
	expected := []fmt.Stringer{notQuiteStringer(10), notQuiteStringer(20)}
	actual := []fmt.Stringer{}
	f := func(s []fmt.Stringer) error {
		actual = s
		return nil
	}
	i := New()
	i.MustBind(Sequence([]notQuiteStringer{10}))
	i.MustBind(Sequence([]notQuiteStringer{20}))
	_, err := i.Call(f)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func TestMapValueInterfaceConversion(t *testing.T) {
	expected := map[string]fmt.Stringer{"a": notQuiteStringer(10), "b": notQuiteStringer(20)}
	actual := map[string]fmt.Stringer{}
	f := func(s map[string]fmt.Stringer) error {
		actual = s
		return nil
	}
	i := New()
	i.MustBind(Mapping(map[string]notQuiteStringer{"a": notQuiteStringer(10)}))
	i.MustBind(Mapping(map[string]notQuiteStringer{"b": notQuiteStringer(20)}))
	_, err := i.Call(f)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func TestSliceIsNotImplicitlyProvided(t *testing.T) {
	f := func(s []string) {}
	i := New()
	_, err := i.Call(f)
	require.Error(t, err)
}

func TestMappingIsNotImplicitlyProvided(t *testing.T) {
	f := func(s map[string]string) {}
	i := New()
	_, err := i.Call(f)
	require.Error(t, err)
}

func TestSliceIsImplicitlyProvidedWhenEnabled(t *testing.T) {
	f := func(s []string) {}
	i := New().Configure(Config{ImplicitSequences: true})
	_, err := i.Call(f)
	require.NoError(t, err)
}

func TestMappingIsImplicitlyProvidedWhenEnabled(t *testing.T) {
	f := func(s map[string]string) {}
	i := New().Configure(Config{ImplicitMappings: true})
	_, err := i.Call(f)
	require.NoError(t, err)
}

func TestIs(t *testing.T) {
	require.True(t, Sequence([]int{1, 2}).Is(&sequenceType{}))
}
