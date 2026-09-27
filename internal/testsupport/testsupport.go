// Package testsupport holds helpers shared by the unit tests: fake client interceptors that make
// one kind of call fail, and a counter delta for asserting on Prometheus metrics.
package testsupport

import (
	"context"
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// ErrBoom is the error the failing interceptors return by default.
var ErrBoom = errors.New("boom")

// FailGet makes every Get of an object of type T (for example *kyvernov1.ClusterPolicy) fail with
// err. Other Gets reach the fake client.
func FailGet[T client.Object](err error) interceptor.Funcs {
	return interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(T); ok {
			return err
		}
		return c.Get(ctx, key, obj, opts...)
	}}
}

// FailGetNamed makes the Get of the object called name fail with err, whatever its type.
func FailGetNamed(name string, err error) interceptor.Funcs {
	return interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if key.Name == name {
			return err
		}
		return c.Get(ctx, key, obj, opts...)
	}}
}

// FailAllGets makes every Get fail with err, like an unreachable API server.
func FailAllGets(err error) interceptor.Funcs {
	return interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
		return err
	}}
}

// FailList makes every List fail with err.
func FailList(err error) interceptor.Funcs {
	return interceptor.Funcs{List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
		return err
	}}
}

// FailCreate makes every Create fail with err.
func FailCreate(err error) interceptor.Funcs {
	return interceptor.Funcs{Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
		return err
	}}
}

// FailDelete makes every Delete fail with err.
func FailDelete(err error) interceptor.Funcs {
	return interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
		return err
	}}
}

// CounterDelta records the value of c now and returns a function that reports how much it has
// grown since. Metrics are global, so tests assert on growth, never on absolute values.
func CounterDelta(c prometheus.Collector) func() float64 {
	before := testutil.ToFloat64(c)
	return func() float64 { return testutil.ToFloat64(c) - before }
}
