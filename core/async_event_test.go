package core_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
)

func TestAsyncEventBusDispatchErrorAndPanicHandling(t *testing.T) {
	var handled int32
	var observed int32
	bus := core.NewAsyncEventBus(
		core.WithEventQueueSize(4),
		core.WithEventWorkerCount(2),
		core.WithEventErrorHandler(func(context.Context, core.Event, error) {
			atomic.AddInt32(&observed, 1)
		}),
	)
	bus.Register(core.ListenerFunc(func(context.Context, core.Event) error {
		atomic.AddInt32(&handled, 1)
		return errors.New("listener failed")
	}))
	bus.Register(core.ListenerFunc(func(context.Context, core.Event) error {
		panic("boom")
	}))

	bus.Publish(context.Background(), core.Event{Type: core.EventLogin, LoginID: "1001"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bus.Flush(ctx); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if atomic.LoadInt32(&handled) != 1 {
		t.Fatalf("listener was not called")
	}
	if atomic.LoadInt32(&observed) != 2 {
		t.Fatalf("expected error handler to observe error and panic, got %d", observed)
	}
	if err := bus.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestAsyncEventBusSnapshotsListenersAtPublish(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var lateHandled int32
	bus := core.NewAsyncEventBus(
		core.WithEventQueueSize(2),
		core.WithEventWorkerCount(1),
	)
	bus.Register(core.ListenerFunc(func(_ context.Context, event core.Event) error {
		if event.LoginID == "block" {
			close(started)
			<-release
		}
		return nil
	}))

	bus.Publish(context.Background(), core.Event{Type: core.EventLogin, LoginID: "block"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocking listener was not started")
	}
	bus.Publish(context.Background(), core.Event{Type: core.EventLogin, LoginID: "queued"})
	bus.Register(core.ListenerFunc(func(_ context.Context, event core.Event) error {
		if event.LoginID == "queued" {
			atomic.AddInt32(&lateHandled, 1)
		}
		return nil
	}))
	close(release)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bus.Flush(ctx); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if atomic.LoadInt32(&lateHandled) != 0 {
		t.Fatal("listener registered after Publish should not receive queued event")
	}
	if err := bus.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}
