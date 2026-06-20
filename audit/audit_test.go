package audit_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/apus-run/better-token/audit"
	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
)

type captureSink struct {
	mu     sync.Mutex
	events []audit.AuditEvent
	fail   bool
	panics bool
}

func (s *captureSink) Write(_ context.Context, event audit.AuditEvent) error {
	if s.panics {
		panic("sink panic")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	if s.fail {
		return errors.New("sink failure")
	}
	return nil
}

func (s *captureSink) all() []audit.AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.AuditEvent(nil), s.events...)
}

func TestMapEventTypeCoversKnownAndUnknown(t *testing.T) {
	if got := audit.MapEventType(core.EventLogin); got != audit.AuditLogin {
		t.Fatalf("login mapped to %s", got)
	}
	if got := audit.MapEventType(core.EventNonceConsumed); got != audit.AuditNonceConsumed {
		t.Fatalf("nonce mapped to %s", got)
	}
	if got := audit.MapEventType(core.EventType("weird")); got != audit.AuditUnknown {
		t.Fatalf("unknown mapped to %s", got)
	}
}

func TestDefaultMapperExtractsDeviceAndIP(t *testing.T) {
	ev := core.Event{
		Type:      core.EventKickOut,
		LoginID:   "1001",
		LoginType: "web",
		Time:      time.Now(),
		Metadata:  map[string]any{"device": "ios", "ip": "127.0.0.1"},
	}
	ae := audit.DefaultMapper(ev)
	if ae.Type != audit.AuditKickOut || ae.Device != "ios" || ae.IP != "127.0.0.1" {
		t.Fatalf("unexpected audit event: %#v", ae)
	}
}

func TestListenerCapturesLoginAndNonceEvents(t *testing.T) {
	sink := &captureSink{}
	bus := core.NewEventBus()
	bus.Register(audit.New(sink))
	manager := core.NewManager(memory.NewStore(), core.WithEventBus(bus))

	if _, err := manager.Login(context.Background(), "1001", "access-1"); err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	nonce, err := manager.GenerateNonce(context.Background())
	if err != nil {
		t.Fatalf("GenerateNonce failed: %v", err)
	}
	if _, err := manager.ConsumeNonce(context.Background(), nonce); err != nil {
		t.Fatalf("ConsumeNonce failed: %v", err)
	}

	var sawLogin, sawNonce bool
	for _, ev := range sink.all() {
		switch ev.Type {
		case audit.AuditLogin:
			sawLogin = true
		case audit.AuditNonceConsumed:
			sawNonce = true
		}
	}
	if !sawLogin || !sawNonce {
		t.Fatalf("expected login and nonce audit events, got %#v", sink.all())
	}
}

func TestListenerErrorAndPanicDoNotBreakMainFlow(t *testing.T) {
	bus := core.NewEventBus()
	bus.Register(audit.New(&captureSink{fail: true}))
	bus.Register(audit.New(&captureSink{panics: true}))
	manager := core.NewManager(memory.NewStore(), core.WithEventBus(bus))

	if _, err := manager.Login(context.Background(), "1001", "access-1"); err != nil {
		t.Fatalf("Login must not fail because of audit sink error/panic: %v", err)
	}
}
