package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
)

func TestNonceGenerateConsumeAndReplay(t *testing.T) {
	store := memory.NewStore()
	manager := core.NewManager(store, core.WithNonceConfig(core.NonceConfig{
		Timeout: time.Minute,
		Length:  24,
	}))

	nonce, err := manager.GenerateNonce(context.Background(), core.WithNoncePurpose("login"))
	if err != nil {
		t.Fatalf("GenerateNonce failed: %v", err)
	}
	if len(nonce) != 24 {
		t.Fatalf("nonce length = %d", len(nonce))
	}
	state, err := manager.ConsumeNonce(context.Background(), nonce)
	if err != nil {
		t.Fatalf("first ConsumeNonce failed: %v", err)
	}
	if state.Kind != core.TokenKindNonce {
		t.Fatalf("consumed state kind = %s", state.Kind)
	}
	if !state.IsConsumed() {
		t.Fatal("returned nonce state should be marked consumed")
	}
	if _, err := manager.ConsumeNonce(context.Background(), nonce); !errors.Is(err, core.ErrNonceReplayed) {
		t.Fatalf("second ConsumeNonce should fail with ErrNonceReplayed, got %v", err)
	}
}

func TestManagerLoginRequiresNonceWhenConfigured(t *testing.T) {
	store := memory.NewStore()
	manager := core.NewManager(store,
		core.WithConfig(core.Config{
			TokenName:    "token",
			Timeout:      time.Hour,
			Concurrent:   true,
			RequireNonce: true,
		}),
	)

	if _, err := manager.Login(context.Background(), "1001", "token-1"); !errors.Is(err, core.ErrEmptyNonce) {
		t.Fatalf("missing nonce should fail with ErrEmptyNonce, got %v", err)
	}
	nonce, err := manager.GenerateNonce(context.Background())
	if err != nil {
		t.Fatalf("GenerateNonce failed: %v", err)
	}
	if _, err := manager.Login(context.Background(), "1001", "token-1", core.WithNonce(nonce)); err != nil {
		t.Fatalf("Login with nonce failed: %v", err)
	}
	if _, err := manager.Login(context.Background(), "1002", "token-2", core.WithNonce(nonce)); !errors.Is(err, core.ErrNonceReplayed) {
		t.Fatalf("reused nonce should fail with ErrNonceReplayed, got %v", err)
	}
}

func TestConsumeNonceRejectsNonNonceKind(t *testing.T) {
	store := memory.NewStore()
	manager := core.NewManager(store)
	if _, err := manager.Login(context.Background(), "1001", "access-1"); err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if _, err := manager.ConsumeNonce(context.Background(), "access-1"); !errors.Is(err, core.ErrUnsupportedKind) {
		t.Fatalf("consuming an access token as nonce should fail with ErrUnsupportedKind, got %v", err)
	}
}
