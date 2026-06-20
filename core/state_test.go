package core_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
)

func TestStateExpirationBoundary(t *testing.T) {
	expiresAt := time.Date(2026, 6, 20, 8, 0, 0, 0, time.UTC)
	state := core.TokenState{ExpiresAt: &expiresAt}

	if state.IsExpired(expiresAt.Add(-time.Nanosecond)) {
		t.Fatal("state should be valid before expires_at")
	}
	if !state.IsExpired(expiresAt) {
		t.Fatal("state should expire at expires_at")
	}
}

func TestStateEffectiveExpiresAtUsesEarliestDeadline(t *testing.T) {
	now := time.Date(2026, 6, 20, 8, 0, 0, 0, time.UTC)
	expiresAt := now.Add(30 * time.Minute)
	state := core.TokenState{ExpiresAt: &expiresAt}

	got := state.EffectiveExpiresAt(now, time.Hour)
	if got == nil || !got.Equal(expiresAt) {
		t.Fatalf("EffectiveExpiresAt should use explicit earlier expires_at, got %v", got)
	}

	got = state.EffectiveExpiresAt(now, 10*time.Minute)
	want := now.Add(10 * time.Minute)
	if got == nil || !got.Equal(want) {
		t.Fatalf("EffectiveExpiresAt should use earlier ttl deadline, got %v want %v", got, want)
	}

	withoutDeadline := core.TokenState{}
	if got := withoutDeadline.EffectiveExpiresAt(now, 0); got != nil {
		t.Fatalf("EffectiveExpiresAt without ttl or expires_at = %v, want nil", got)
	}
}

func TestStateCloneCopiesMutableFields(t *testing.T) {
	expiresAt := time.Date(2026, 6, 20, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	metadata := json.RawMessage(`{"role":"admin"}`)
	state := &core.TokenState{
		Token:     "refresh-1",
		Kind:      core.TokenKindRefresh,
		LoginID:   "1001",
		LoginType: "login",
		Status:    core.TokenStatusActive,
		ExpiresAt: &expiresAt,
		Metadata:  metadata,
		Refresh:   &core.RefreshInfo{AccessToken: "access-1"},
		Online:    &core.OnlineInfo{},
	}
	state.MarkRevoked(expiresAt)

	clone := state.Clone()
	if clone == state {
		t.Fatal("Clone should return a distinct pointer")
	}
	if &clone.Metadata[0] == &state.Metadata[0] {
		t.Fatal("Clone should deep-copy metadata")
	}
	if clone.ExpiresAt == state.ExpiresAt {
		t.Fatal("Clone should deep-copy time pointers")
	}
	if clone.Refresh == state.Refresh || clone.Online == state.Online {
		t.Fatal("Clone should deep-copy info pointers")
	}
	if clone.Refresh.RevokedAt == state.Refresh.RevokedAt {
		t.Fatal("Clone should deep-copy nested time pointers")
	}
	if clone.ExpiresAt.Location() != time.UTC {
		t.Fatal("Clone should normalize time pointers to UTC")
	}
}

func TestStateDomainActionsSetUTCTimestamps(t *testing.T) {
	now := time.Date(2026, 6, 20, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))

	refresh := &core.TokenState{Kind: core.TokenKindRefresh, Status: core.TokenStatusActive, Refresh: &core.RefreshInfo{}}
	refresh.MarkRevoked(now)
	if !refresh.IsRevoked() {
		t.Fatal("MarkRevoked should set status to revoked")
	}
	if refresh.Refresh.RevokedAt == nil || refresh.Refresh.RevokedAt.Location() != time.UTC {
		t.Fatalf("RevokedAt should be UTC, got %v", refresh.Refresh.RevokedAt)
	}

	nonce := &core.TokenState{Kind: core.TokenKindNonce, Status: core.TokenStatusActive, Nonce: &core.NonceInfo{}}
	nonce.MarkConsumed(now)
	if !nonce.IsConsumed() {
		t.Fatal("MarkConsumed should set status to consumed")
	}
	if nonce.Nonce.ConsumedAt == nil || nonce.Nonce.ConsumedAt.Location() != time.UTC {
		t.Fatalf("ConsumedAt should be UTC, got %v", nonce.Nonce.ConsumedAt)
	}

	access := &core.TokenState{Kind: core.TokenKindAccess, Status: core.TokenStatusActive}
	access.MarkOnline(now, core.OnlineInfo{IP: "127.0.0.1"})
	if access.Online == nil || access.Online.OnlineAt == nil || access.Online.OnlineAt.Location() != time.UTC {
		t.Fatalf("OnlineAt should be UTC, got %v", access.Online)
	}
	access.MarkOffline(now)
	if access.Online.OfflineAt == nil || access.Online.OfflineAt.Location() != time.UTC {
		t.Fatalf("OfflineAt should be UTC, got %v", access.Online.OfflineAt)
	}
}
