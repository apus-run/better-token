package core

import (
	"context"
	"sync"
	"time"
)

type EventType string

const (
	EventLogin          EventType = "login"
	EventLogout         EventType = "logout"
	EventKickOut        EventType = "kick_out"
	EventRenewTimeout   EventType = "renew_timeout"
	EventReplaced       EventType = "replaced"
	EventRefreshIssued  EventType = "refresh_issued"
	EventRefresh        EventType = "refresh"
	EventRefreshRevoked EventType = "refresh_revoked"
	EventNonceConsumed  EventType = "nonce_consumed"
	EventOnline         EventType = "online"
	EventOffline        EventType = "offline"
)

type Event struct {
	Type      EventType      `json:"type"`
	LoginID   string         `json:"login_id,omitempty"`
	LoginType string         `json:"login_type,omitempty"`
	Token     TokenValue     `json:"token,omitempty"`
	Time      time.Time      `json:"time"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type Listener interface {
	Handle(ctx context.Context, event Event) error
}

type ListenerFunc func(ctx context.Context, event Event) error

func (f ListenerFunc) Handle(ctx context.Context, event Event) error {
	return f(ctx, event)
}

type EventBus interface {
	Register(listener Listener)
	Publish(ctx context.Context, event Event)
	Clear()
	ListenerCount() int
}

type NoopEventBus struct{}

func (NoopEventBus) Register(Listener)              {}
func (NoopEventBus) Publish(context.Context, Event) {}
func (NoopEventBus) Clear()                         {}
func (NoopEventBus) ListenerCount() int             { return 0 }

type SyncEventBus struct {
	mu        sync.RWMutex
	listeners []Listener
}

func NewEventBus() *SyncEventBus {
	return &SyncEventBus{}
}

func (b *SyncEventBus) Register(listener Listener) {
	if listener == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners = append(b.listeners, listener)
}

func (b *SyncEventBus) Publish(ctx context.Context, event Event) {
	b.mu.RLock()
	listeners := append([]Listener(nil), b.listeners...)
	b.mu.RUnlock()

	for _, listener := range listeners {
		func() {
			defer func() {
				_ = recover()
			}()
			_ = listener.Handle(ctx, event)
		}()
	}
}

func (b *SyncEventBus) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners = nil
}

func (b *SyncEventBus) ListenerCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.listeners)
}
