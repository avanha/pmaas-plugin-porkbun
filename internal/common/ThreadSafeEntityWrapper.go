package common

import "pmaas.io/spi"

type ThreadSafeEntityWrapper[T any] struct {
	Container spi.IPMAASContainer
	Entity    T
}

func (w *ThreadSafeEntityWrapper[T]) Invoke(f func(T)) error {
	return w.Container.EnqueueOnPluginGoRoutine(func() {
		f(w.Entity)
	})
}

func ThreadSafeEntityWrapperExecValueFunc[T any, V any](
	entityWrapper *ThreadSafeEntityWrapper[T],
	fn func(target T) V) V {
	if entityWrapper == nil {
		panic("current target is nil")
	}

	resultCh := make(chan V)
	err := entityWrapper.Invoke(func(target T) {
		defer close(resultCh)
		resultCh <- fn(target)
	})

	if err != nil {
		close(resultCh)
		panic(err)
	}

	return <-resultCh
}
