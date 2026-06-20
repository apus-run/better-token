package core

import (
	"context"
	"sync"

	"github.com/apus-run/better-token/pkg/option"
)

type EventErrorHandler func(ctx context.Context, event Event, err error)

type AsyncEventBus struct {
	mu           sync.RWMutex
	listeners    []Listener
	queue        chan asyncEventJob
	workerCount  int
	errorHandler EventErrorHandler

	wg        sync.WaitGroup
	closeOnce sync.Once
	closed    bool
}

type asyncEventJob struct {
	ctx          context.Context
	event        Event
	listeners    []Listener
	errorHandler EventErrorHandler
}

type AsyncEventBusOption = option.Option[AsyncEventBus]

func WithEventQueueSize(size int) AsyncEventBusOption {
	return func(b *AsyncEventBus) {
		if size > 0 {
			b.queue = make(chan asyncEventJob, size)
		}
	}
}

func WithEventWorkerCount(count int) AsyncEventBusOption {
	return func(b *AsyncEventBus) {
		if count > 0 {
			b.workerCount = count
		}
	}
}

func WithEventErrorHandler(handler EventErrorHandler) AsyncEventBusOption {
	return func(b *AsyncEventBus) {
		b.errorHandler = handler
	}
}

func NewAsyncEventBus(opts ...AsyncEventBusOption) *AsyncEventBus {
	b := &AsyncEventBus{
		queue:       make(chan asyncEventJob, 64),
		workerCount: 1,
	}
	option.Apply(b, opts...)
	if b.queue == nil {
		b.queue = make(chan asyncEventJob, 64)
	}
	if b.workerCount <= 0 {
		b.workerCount = 1
	}
	for i := 0; i < b.workerCount; i++ {
		go b.worker()
	}
	return b
}

func (b *AsyncEventBus) Register(listener Listener) {
	if listener == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners = append(b.listeners, listener)
}

func (b *AsyncEventBus) Publish(ctx context.Context, event Event) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	defer b.mu.RUnlock()

	b.wg.Add(1)
	job := asyncEventJob{
		ctx:          ctx,
		event:        event,
		listeners:    append([]Listener(nil), b.listeners...),
		errorHandler: b.errorHandler,
	}
	select {
	case b.queue <- job:
	case <-ctx.Done():
		b.wg.Done()
	}
}

func (b *AsyncEventBus) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners = nil
}

func (b *AsyncEventBus) ListenerCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.listeners)
}

func (b *AsyncEventBus) Flush(ctx context.Context) error {
	ctx = normalizeContext(ctx)
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *AsyncEventBus) Close(ctx context.Context) error {
	ctx = normalizeContext(ctx)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		if err := b.Flush(ctx); err != nil {
			return err
		}
		b.closeOnce.Do(func() {
			close(b.queue)
		})
		return nil
	}
	b.closed = true
	b.mu.Unlock()
	if err := b.Flush(ctx); err != nil {
		return err
	}
	b.closeOnce.Do(func() {
		close(b.queue)
	})
	return nil
}

func (b *AsyncEventBus) worker() {
	for job := range b.queue {
		b.dispatch(job.ctx, job.event, job.listeners, job.errorHandler)
		b.wg.Done()
	}
}

func (b *AsyncEventBus) dispatch(ctx context.Context, event Event, listeners []Listener, handler EventErrorHandler) {
	for _, listener := range listeners {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil && handler != nil {
					handler(ctx, event, ErrEventListenerPanic)
				}
			}()
			if err := listener.Handle(ctx, event); err != nil && handler != nil {
				handler(ctx, event, err)
			}
		}()
	}
}
