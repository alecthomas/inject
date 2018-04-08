package inject

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInjectorBind(t *testing.T) {
	i := SafeNew()
	i.Bind("hello")
	v, err := i.Get("")
	require.NoError(t, err)
	require.Equal(t, "hello", v)
}

type stringer string

func (s stringer) String() string {
	return string(s)
}

type stringerStruct struct {
	s string
}

func (s *stringerStruct) String() string {
	return s.s
}

func TestInjectorBindTo(t *testing.T) {
	i := SafeNew()
	s := stringer("hello")
	i.BindTo((*fmt.Stringer)(nil), s)
	v, err := i.Get((*fmt.Stringer)(nil))
	require.NoError(t, err)
	ss := v.(fmt.Stringer)
	require.Equal(t, "hello", ss.String())
}

func TestInjectorBindToStruct(t *testing.T) {
	i := SafeNew()
	s := &stringerStruct{"hello"}
	i.BindTo((*fmt.Stringer)(nil), s)
	ss, err := i.Get((*fmt.Stringer)(nil))
	require.NoError(t, err)
	require.Equal(t, "hello", ss.(fmt.Stringer).String())
}

func TestInjectorBindToTypeAlias(t *testing.T) {
	i := SafeNew()
	i.BindTo(stringer(""), "hello")
	iv, err := i.Get(stringer(""))
	require.NoError(t, err)
	v := iv.(stringer)
	require.Equal(t, stringer("hello"), v)
	i.BindTo(int64(0), 10)
	w, err := i.Get(int64(0))
	require.NoError(t, err)
	require.Equal(t, int64(10), w)
}

func TestInjectorBindToInvalidImplementation(t *testing.T) {
	i := SafeNew()
	s := "hello"
	err := i.BindTo((*fmt.Stringer)(nil), s)
	require.Error(t, err)
}

func TestGetUnboundType(t *testing.T) {
	i := SafeNew()
	_, err := i.Get("")
	require.Error(t, err)
}

func TestProvider(t *testing.T) {
	i := SafeNew()
	i.Bind(func() string { return "hello" })
	i.Bind(func() int { return 123 })
	sv, err := i.Get("")
	require.NoError(t, err)
	require.Equal(t, "hello", sv)
	iv, err := i.Get(1)
	require.NoError(t, err)
	require.Equal(t, 123, iv)
}

func TestProviderGraph(t *testing.T) {
	i := SafeNew()
	i.Bind(func() int { return 123 })
	i.Bind(func(n int) string { return fmt.Sprintf("hello:%d", n) })
	sv, err := i.Get("")
	require.NoError(t, err)
	require.Equal(t, "hello:123", sv)
}

func TestChildInjector(t *testing.T) {
	i := SafeNew()
	i.Bind(func() string { return "hello" })
	c := i.Child()
	c.Bind(func() int { return 123 })
	sv, err := c.Get("")
	require.NoError(t, err)
	require.Equal(t, "hello", sv)
	iv, err := c.Get(1)
	require.NoError(t, err)
	require.Equal(t, 123, iv)
}

func TestInjectorCall(t *testing.T) {
	i := SafeNew()
	i.Bind("hello")
	i.Bind(123)
	as := ""
	ai := 0
	_, err := i.Call(func(s string, i int) {
		as = s
		ai = i
	})
	require.NoError(t, err)
	require.Equal(t, "hello", as)
	require.Equal(t, 123, ai)
}

func TestSingletonAnnotation(t *testing.T) {
	i := SafeNew()
	calls := 0
	i.Bind(Singleton(func() string {
		calls++
		return "hello"
	}))
	i.Get("")
	i.Get("")
	require.Equal(t, 1, calls)
}

func TestSingletonToNonProviderPanics(t *testing.T) {
	i := New()
	require.Panics(t, func() {
		i.Bind(Singleton(1))
	})
}

func TestDynamicInjection(t *testing.T) {
	i := SafeNew()
	called := 0
	i.Bind(func() *string {
		called++
		s := new(string)
		*s = fmt.Sprintf("hello:%d", called)
		return s
	})
	p := new(string)
	a, err := i.Get(p)
	require.NoError(t, err)
	b, err := i.Get(p)
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	require.Equal(t, 2, called)
}

func TestSequenceAnnotation(t *testing.T) {
	i := SafeNew()
	i.Bind(Sequence([]int{1}))
	i.Bind(Sequence([]int{2}))
	i.Bind(Sequence(Singleton(func() []int { return []int{3} })))
	v, err := i.Get([]int{})
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3}, v)
}

func TestMappingAnnotation(t *testing.T) {
	i := SafeNew()
	i.Bind(Mapping(map[string]int{"one": 1}))
	i.Bind(Mapping(map[string]int{"two": 2}))
	i.Bind(Mapping(func() map[string]int { return map[string]int{"three": 3} }))
	v, err := i.Get(map[string]int{})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"one": 1, "two": 2, "three": 3}, v)
	called := false
	i.Call(func(m map[string]int) {
		called = true
		require.Equal(t, map[string]int{"one": 1, "two": 2, "three": 3}, m)
	})
	require.True(t, called)
}

func TestLiteral(t *testing.T) {
	i := SafeNew()
	buf := bytes.Buffer{}
	i.Bind(Literal(buf.WriteString))
	i.Call(func(write func(string) (int, error)) {
		write("hello world")
	})
	require.Equal(t, "hello world", buf.String())
}

type UserName string

func TestPseudoBoundValues(t *testing.T) {
	i := SafeNew()
	i.Bind(UserName("bob"))
	name := ""
	i.Call(func(user UserName) {
		name = string(user)
	})
	require.Equal(t, "bob", name)
}

type myModule struct{}

func (m *myModule) ProvideString(i int) string { return fmt.Sprintf("hello:%d", i) }

func TestModule(t *testing.T) {
	i := SafeNew()
	err := i.Bind(123)
	require.NoError(t, err)
	err = i.Install(&myModule{})
	require.NoError(t, err)
	actual, err := i.Get("")
	require.NoError(t, err)
	require.Equal(t, "hello:123", actual)
}

func TestCallError(t *testing.T) {
	f := func() error {
		return fmt.Errorf("failed")
	}
	i := SafeNew()
	_, err := i.Call(f)
	require.Error(t, err)
}

type notQuiteStringer int

func (n notQuiteStringer) String() string { return fmt.Sprintf("%d", n) }

type notQuiteAnotherStringer float32

func (n notQuiteAnotherStringer) String() string { return fmt.Sprintf("%f", n) }

func TestInterfaceConversion(t *testing.T) {
	f := func(s fmt.Stringer) error {
		return nil
	}
	i := SafeNew()
	i.Bind(notQuiteStringer(10))
	_, err := i.Call(f)
	require.NoError(t, err)
}

func TestSliceInterfaceConversion(t *testing.T) {
	expected := []fmt.Stringer{notQuiteStringer(10), notQuiteAnotherStringer(20)}
	actual := []fmt.Stringer{}
	f := func(s []fmt.Stringer) error {
		actual = s
		return nil
	}
	i := SafeNew()
	i.Bind(Sequence([]notQuiteStringer{10}))
	i.Bind(Sequence([]notQuiteAnotherStringer{20}))
	_, err := i.Call(f)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func TestMapValueInterfaceConversion(t *testing.T) {
	expected := map[string]fmt.Stringer{"a": notQuiteStringer(10), "b": notQuiteAnotherStringer(20)}
	actual := map[string]fmt.Stringer{}
	f := func(s map[string]fmt.Stringer) error {
		actual = s
		return nil
	}
	i := SafeNew()
	i.Bind(Mapping(map[string]notQuiteStringer{"a": notQuiteStringer(10)}))
	i.Bind(Mapping(map[string]notQuiteAnotherStringer{"b": notQuiteAnotherStringer(20)}))
	_, err := i.Call(f)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func TestSliceIsNotImplicitlyProvided(t *testing.T) {
	f := func(s []string) {}
	i := SafeNew()
	_, err := i.Call(f)
	require.Error(t, err)
}

func TestMappingIsNotImplicitlyProvided(t *testing.T) {
	f := func(s map[string]string) {}
	i := SafeNew()
	_, err := i.Call(f)
	require.Error(t, err)
}

func TestIs(t *testing.T) {
	require.True(t, Sequence([]int{1, 2}).Is(&sequenceType{}))
}

func TestDuplicateNamedBindErrors(t *testing.T) {
	type Named string

	i := SafeNew()
	err := i.Bind(Named("alec"))
	require.NoError(t, err)
	err = i.Bind(Named("bob"))
	require.Error(t, err)
}

func TestValidate(t *testing.T) {
	i := SafeNew()

	err := i.Validate(func(string) {})
	require.Error(t, err)

	i.Bind(func(int) string { return "hello" })
	err = i.Validate(func(string) {})
	require.Error(t, err)
	i.Bind(10)

	// Verify that Validate doesn't call anything.
	var actual string
	err = i.Validate(func(s string) { actual = s })
	require.NoError(t, err)
	require.Equal(t, "", actual)
}

type testModuleA struct {
	param int
}

func (t *testModuleA) ProvideInt(string) int { return 0 }

type testModuleB struct{}

func (t *testModuleB) ProvideString(int) string { return "" }

func TestProviderCycle(t *testing.T) {
	i := SafeNew()
	i.Install(&testModuleA{})
	i.Install(&testModuleB{})
	_, err := i.Get(int(0))
	require.Error(t, err)
}

func TestInstallIdenticalDuplicateModule(t *testing.T) {
	i := SafeNew()
	err := i.Install(&testModuleA{})
	require.NoError(t, err)
	err = i.Install(&testModuleA{})
	require.NoError(t, err)
}

func TestInstallDifferingDuplicateModule(t *testing.T) {
	i := SafeNew()
	err := i.Install(&testModuleA{param: 1})
	require.NoError(t, err)
	err = i.Install(&testModuleA{param: 2})
	require.Error(t, err)
}

type testConfigurableModuleA struct{}

func (t *testConfigurableModuleA) Configure(binder Binder) error {
	binder.Bind(10)
	return nil
}

type testConfigurableModuleB struct{}

func (t *testConfigurableModuleB) Configure(binder Binder) error {
	binder.Install(&testConfigurableModuleA{})
	return nil
}

func TestInstallConfigurableModule(t *testing.T) {
	i := SafeNew()
	i.Install(&testConfigurableModuleB{})
	v, err := i.Get(0)
	require.NoError(t, err)
	require.Equal(t, 10, v.(int))
}

type testModuleParam struct{ param int }

func (t *testModuleParam) ProvideInt() int { return t.param }

func TestInstallNewZeroModuleKeepsExisting(t *testing.T) {
	i := SafeNew()
	err := i.Install(&testModuleParam{param: 123})
	require.NoError(t, err)
	err = i.Install(&testModuleParam{})
	require.NoError(t, err)
	v, err := i.Get(0)
	require.NoError(t, err)
	require.Equal(t, 123, v)
}

func TestInstallNewNonZeroModuleOverwritesExisting(t *testing.T) {
	i := SafeNew()
	err := i.Install(&testModuleParam{})
	require.NoError(t, err)
	err = i.Install(&testModuleParam{param: 123})
	require.NoError(t, err)
	v, err := i.Get(0)
	require.NoError(t, err)
	require.Equal(t, 123, v)
}
