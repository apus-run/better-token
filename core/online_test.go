package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
)

func TestManagerListTokenStatesAndLogoutByDevice(t *testing.T) {
	store := memory.NewStore()
	manager := core.NewManager(store, core.WithConfig(core.Config{
		TokenName:  "token",
		Timeout:    time.Hour,
		Concurrent: true,
	}))
	ctx := context.Background()
	_, _ = manager.Login(ctx, "1001", "ios-token", core.WithDevice("ios"))
	_, _ = manager.Login(ctx, "1001", "web-token", core.WithDevice("web"))

	states, err := manager.ListTokenStates(ctx, "1001")
	if err != nil {
		t.Fatalf("ListTokenStates failed: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected 2 online tokens, got %d", len(states))
	}

	if err := manager.LogoutByDevice(ctx, "1001", "ios"); err != nil {
		t.Fatalf("LogoutByDevice failed: %v", err)
	}
	if _, err := manager.GetTokenState(ctx, "ios-token"); !errors.Is(err, core.ErrTokenNotFound) {
		t.Fatalf("ios token should be gone, got %v", err)
	}
	if _, err := manager.GetTokenState(ctx, "web-token"); err != nil {
		t.Fatalf("web token should survive: %v", err)
	}
}

func TestManagerLogoutByDeviceRejectsEmptyDevice(t *testing.T) {
	manager := core.NewManager(memory.NewStore())
	if err := manager.LogoutByDevice(context.Background(), "1001", " "); !errors.Is(err, core.ErrEmptyDevice) {
		t.Fatalf("empty device should fail with ErrEmptyDevice, got %v", err)
	}
}
