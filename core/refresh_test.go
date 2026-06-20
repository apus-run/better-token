package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
)

func TestRefreshLoginAndRefreshRotation(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	manager := core.NewManager(store, core.WithConfig(core.Config{
		TokenName:  "token",
		Timeout:    time.Hour,
		Concurrent: true,
	}))

	result, err := manager.LoginWithRefresh(ctx, "1001", "access-1", "refresh-1", core.WithDevice("ios"))
	if err != nil {
		t.Fatalf("LoginWithRefresh failed: %v", err)
	}
	if result.TokenState.Token != "access-1" || result.RefreshState.Token != "refresh-1" {
		t.Fatalf("unexpected login result: %#v", result)
	}
	if result.RefreshState.Kind != core.TokenKindRefresh {
		t.Fatalf("refresh state kind = %s", result.RefreshState.Kind)
	}

	refreshed, err := manager.Refresh(ctx, "refresh-1", "access-2", core.WithNextRefreshToken("refresh-2"))
	if err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}
	if refreshed.TokenState.Token != "access-2" || refreshed.RefreshState.Token != "refresh-2" {
		t.Fatalf("unexpected refresh result: %#v", refreshed)
	}
	if _, err := manager.GetTokenState(ctx, "access-1"); !errors.Is(err, core.ErrTokenNotFound) {
		t.Fatalf("old access token should be revoked, got %v", err)
	}
	if _, err := manager.GetTokenState(ctx, "access-2"); err != nil {
		t.Fatalf("new access token should be valid: %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-1", "access-x", core.WithNextRefreshToken("refresh-x")); !errors.Is(err, core.ErrRefreshTokenNotFound) {
		t.Fatalf("old refresh token should be revoked after rotation, got %v", err)
	}
}

func TestRefreshRejectsExpiredAndRevokedRefreshToken(t *testing.T) {
	now := time.Date(2026, 6, 20, 8, 0, 0, 0, time.UTC)
	runtime := core.Runtime{Now: func() time.Time { return now }}
	store := memory.NewStore(memory.WithRuntime(runtime))
	manager := core.NewManager(store,
		core.WithRuntime(runtime),
		core.WithConfig(core.Config{TokenName: "token", Timeout: time.Hour, Concurrent: true}),
		core.WithRefreshConfig(core.RefreshConfig{
			Timeout:                    time.Minute,
			RotateRefreshToken:         true,
			RevokeAccessTokenOnRefresh: true,
			RevokeRefreshOnLogout:      true,
		}),
	)

	if _, err := manager.LoginWithRefresh(context.Background(), "1001", "access-1", "refresh-1"); err != nil {
		t.Fatalf("LoginWithRefresh failed: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := manager.Refresh(context.Background(), "refresh-1", "access-2", core.WithNextRefreshToken("refresh-2")); !errors.Is(err, core.ErrRefreshTokenNotFound) {
		t.Fatalf("expired refresh token should be evicted by store TTL, got %v", err)
	}

	now = time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	if _, err := manager.LoginWithRefresh(context.Background(), "1001", "access-3", "refresh-3"); err != nil {
		t.Fatalf("LoginWithRefresh refresh-3 failed: %v", err)
	}
	if err := manager.RevokeRefreshToken(context.Background(), "refresh-3"); err != nil {
		t.Fatalf("RevokeRefreshToken failed: %v", err)
	}
	if _, err := manager.Refresh(context.Background(), "refresh-3", "access-4", core.WithNextRefreshToken("refresh-4")); !errors.Is(err, core.ErrRefreshTokenRevoked) {
		t.Fatalf("revoked refresh token should fail with ErrRefreshTokenRevoked, got %v", err)
	}
}

func TestRefreshRequiresNextRefreshTokenWhenRotationEnabled(t *testing.T) {
	store := memory.NewStore()
	manager := core.NewManager(store)
	if _, err := manager.LoginWithRefresh(context.Background(), "1001", "access-1", "refresh-1"); err != nil {
		t.Fatalf("LoginWithRefresh failed: %v", err)
	}
	if _, err := manager.Refresh(context.Background(), "refresh-1", "access-2"); !errors.Is(err, core.ErrNextRefreshTokenRequired) {
		t.Fatalf("Refresh without next refresh token should fail, got %v", err)
	}
}

func TestRefreshRejectsRefreshTokenSelfRotation(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	manager := core.NewManager(store)
	if _, err := manager.LoginWithRefresh(ctx, "1001", "access-1", "refresh-1"); err != nil {
		t.Fatalf("LoginWithRefresh failed: %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-1", "access-2", core.WithNextRefreshToken("refresh-1")); !errors.Is(err, core.ErrNextRefreshTokenReuse) {
		t.Fatalf("self rotation should fail with ErrNextRefreshTokenReuse, got %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-1", "access-2", core.WithNextRefreshToken("refresh-2")); err != nil {
		t.Fatalf("refresh token should remain usable after rejected self rotation: %v", err)
	}
}

func TestRefreshRejectsReusingOldAccessToken(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	manager := core.NewManager(store)
	if _, err := manager.LoginWithRefresh(ctx, "1001", "access-1", "refresh-1"); err != nil {
		t.Fatalf("LoginWithRefresh failed: %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-1", "access-1", core.WithNextRefreshToken("refresh-2")); !errors.Is(err, core.ErrNextAccessTokenReuse) {
		t.Fatalf("reusing old access token should fail with ErrNextAccessTokenReuse, got %v", err)
	}
	if _, err := manager.GetTokenState(ctx, "access-1"); err != nil {
		t.Fatalf("old access token should remain valid after rejected refresh: %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-1", "access-2", core.WithNextRefreshToken("refresh-2")); err != nil {
		t.Fatalf("refresh token should remain usable after rejected access reuse: %v", err)
	}
}

func TestRefreshSingleSessionReplacementRevokesOldRefreshToken(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	manager := core.NewManager(store, core.WithConfig(core.Config{
		TokenName:  "token",
		Timeout:    time.Hour,
		Concurrent: false,
	}))
	if _, err := manager.LoginWithRefresh(ctx, "1001", "access-1", "refresh-1"); err != nil {
		t.Fatalf("first LoginWithRefresh failed: %v", err)
	}
	if _, err := manager.LoginWithRefresh(ctx, "1001", "access-2", "refresh-2"); err != nil {
		t.Fatalf("second LoginWithRefresh failed: %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-1", "access-3", core.WithNextRefreshToken("refresh-3")); !errors.Is(err, core.ErrRefreshTokenNotFound) {
		t.Fatalf("old refresh token should be revoked by replacement, got %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-2", "access-4", core.WithNextRefreshToken("refresh-4")); err != nil {
		t.Fatalf("new refresh token should remain usable: %v", err)
	}
}

func TestRefreshConfigPartialTimeoutPreservesSecurityDefaults(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	manager := core.NewManager(store,
		core.WithConfig(core.Config{
			TokenName:  "token",
			Timeout:    time.Hour,
			Concurrent: true,
		}),
		core.WithRefreshConfig(core.RefreshConfig{Timeout: time.Hour}),
	)
	if _, err := manager.LoginWithRefresh(ctx, "1001", "access-1", "refresh-1"); err != nil {
		t.Fatalf("LoginWithRefresh failed: %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-1", "access-2"); !errors.Is(err, core.ErrNextRefreshTokenRequired) {
		t.Fatalf("partial config should preserve refresh rotation, got %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-1", "access-2", core.WithNextRefreshToken("refresh-2")); err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}
	if _, err := manager.GetTokenState(ctx, "access-1"); !errors.Is(err, core.ErrTokenNotFound) {
		t.Fatalf("partial config should preserve access-token revocation, got %v", err)
	}
	if err := manager.Logout(ctx, "access-2"); err != nil {
		t.Fatalf("Logout failed: %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-2", "access-3", core.WithNextRefreshToken("refresh-3")); !errors.Is(err, core.ErrRefreshTokenNotFound) {
		t.Fatalf("partial config should preserve logout refresh revocation, got %v", err)
	}
}

func TestRefreshIssuedEventDoesNotPublishRefreshToken(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	eventBus := core.NewEventBus()
	var refreshIssued core.Event
	eventBus.Register(core.ListenerFunc(func(_ context.Context, event core.Event) error {
		if event.Type == core.EventRefreshIssued {
			refreshIssued = event
		}
		return nil
	}))
	manager := core.NewManager(store, core.WithEventBus(eventBus))
	if _, err := manager.LoginWithRefresh(ctx, "1001", "access-1", "refresh-1"); err != nil {
		t.Fatalf("LoginWithRefresh failed: %v", err)
	}
	if refreshIssued.Type != core.EventRefreshIssued {
		t.Fatal("refresh issued event was not published")
	}
	if refreshIssued.Token == "refresh-1" {
		t.Fatal("refresh issued event leaked refresh token")
	}
}

func TestRefreshLogoutRevokesRefreshToken(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	manager := core.NewManager(store)
	if _, err := manager.LoginWithRefresh(ctx, "1001", "access-1", "refresh-1"); err != nil {
		t.Fatalf("LoginWithRefresh failed: %v", err)
	}
	if err := manager.Logout(ctx, "access-1"); err != nil {
		t.Fatalf("Logout failed: %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-1", "access-2", core.WithNextRefreshToken("refresh-2")); !errors.Is(err, core.ErrRefreshTokenNotFound) {
		t.Fatalf("refresh after logout should fail, got %v", err)
	}
}

func TestRefreshRotationConsumesOldTokenOnce(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	manager := core.NewManager(store)
	if _, err := manager.LoginWithRefresh(ctx, "1001", "access-1", "refresh-1"); err != nil {
		t.Fatalf("LoginWithRefresh failed: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	successes := make(chan struct{}, workers)
	failures := make(chan error, workers)
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := manager.Refresh(
				ctx,
				"refresh-1",
				core.TokenValue("access-next-"+string(rune('a'+i))),
				core.WithNextRefreshToken(core.TokenValue("refresh-next-"+string(rune('a'+i)))),
			)
			if err != nil {
				failures <- err
				return
			}
			successes <- struct{}{}
		}()
	}
	wg.Wait()
	close(successes)
	close(failures)
	if len(successes) != 1 {
		t.Fatalf("expected exactly one refresh success, got %d", len(successes))
	}
	for err := range failures {
		if !errors.Is(err, core.ErrRefreshTokenRevoked) && !errors.Is(err, core.ErrRefreshTokenNotFound) {
			t.Fatalf("concurrent refresh should fail as revoked/not-found, got %v", err)
		}
	}
}

func TestRefreshBypassesLoginNonceRequirement(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	manager := core.NewManager(store,
		core.WithConfig(core.Config{
			TokenName:    "token",
			Timeout:      time.Hour,
			Concurrent:   true,
			RequireNonce: true,
		}),
	)

	nonce, err := manager.GenerateNonce(ctx)
	if err != nil {
		t.Fatalf("GenerateNonce failed: %v", err)
	}
	if _, err := manager.LoginWithRefresh(ctx, "1001", "access-1", "refresh-1", core.WithNonce(nonce)); err != nil {
		t.Fatalf("LoginWithRefresh with nonce failed: %v", err)
	}
	if _, err := manager.Refresh(ctx, "refresh-1", "access-2", core.WithNextRefreshToken("refresh-2")); err != nil {
		t.Fatalf("Refresh should not require login nonce: %v", err)
	}
}
