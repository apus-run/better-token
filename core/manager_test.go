package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
)

func TestDefaultConfigAndRuntime(t *testing.T) {
	config := core.DefaultConfig()
	if config.TokenName != "token" {
		t.Fatalf("TokenName = %q", config.TokenName)
	}
	if config.Timeout != 30*24*time.Hour {
		t.Fatalf("Timeout = %v", config.Timeout)
	}
	if !config.Concurrent {
		t.Fatal("Concurrent should default to true")
	}

	runtime := core.Runtime{}
	manager := core.NewManager(memory.NewStore(), core.WithRuntime(runtime))
	state, err := manager.Login(context.Background(), "1001", "token-1")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if state.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt should use UTC, got %v", state.CreatedAt.Location())
	}
}

func TestManagerLoginGetRenewLogoutLifecycle(t *testing.T) {
	now := time.Date(2026, 6, 20, 8, 0, 0, 0, time.UTC)
	store := memory.NewStore(memory.WithRuntime(core.Runtime{Now: func() time.Time { return now }}))
	manager := core.NewManager(store,
		core.WithRuntime(core.Runtime{Now: func() time.Time { return now }}),
		core.WithConfig(core.Config{
			TokenName:     "token",
			Timeout:       time.Hour,
			ActiveTimeout: 10 * time.Minute,
			AutoRenew:     true,
			Concurrent:    true,
		}),
	)

	state, err := manager.Login(context.Background(), "1001", "token-1", core.WithDevice("ios"))
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if state.Token != "token-1" || state.LoginID != "1001" || state.Device != "ios" {
		t.Fatalf("Unexpected state: %#v", state)
	}
	if state.ExpiresAt == nil || !state.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("Unexpected ExpiresAt: %v", state.ExpiresAt)
	}
	if _, err := manager.GetSession(context.Background(), "1001"); err != nil {
		t.Fatalf("Login should create session: %v", err)
	}

	now = now.Add(5 * time.Minute)
	renewed, err := manager.GetTokenState(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("GetTokenState failed: %v", err)
	}
	if !renewed.LastActiveAt.Equal(now) {
		t.Fatalf("LastActiveAt = %v, want %v", renewed.LastActiveAt, now)
	}
	if renewed.ExpiresAt == nil || !renewed.ExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("AutoRenew ExpiresAt = %v", renewed.ExpiresAt)
	}

	if !manager.IsValid(context.Background(), "token-1") {
		t.Fatal("token should be valid")
	}
	if err := manager.Renew(context.Background(), "token-1", 30*time.Minute); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}
	afterRenew, err := manager.GetTokenState(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("GetTokenState after Renew failed: %v", err)
	}
	if afterRenew.ExpiresAt == nil || !afterRenew.ExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("AutoRenew should apply active timeout on read, got %v", afterRenew.ExpiresAt)
	}

	if err := manager.Logout(context.Background(), "token-1"); err != nil {
		t.Fatalf("Logout failed: %v", err)
	}
	if _, err := manager.GetTokenState(context.Background(), "token-1"); !errors.Is(err, core.ErrTokenNotFound) {
		t.Fatalf("Logged out token should be missing, got %v", err)
	}
	if _, err := manager.GetSession(context.Background(), "1001"); err != nil {
		t.Fatalf("Logout should not delete session: %v", err)
	}
}

func TestManagerConcurrentAndShareToken(t *testing.T) {
	ctx := context.Background()

	single := core.NewManager(memory.NewStore(), core.WithConfig(core.Config{
		TokenName:  "token",
		Timeout:    time.Hour,
		Concurrent: false,
	}))
	if _, err := single.Login(ctx, "1001", "token-1"); err != nil {
		t.Fatalf("First login failed: %v", err)
	}
	if _, err := single.Login(ctx, "1001", "token-2"); err != nil {
		t.Fatalf("Second login failed: %v", err)
	}
	if _, err := single.GetTokenState(ctx, "token-1"); !errors.Is(err, core.ErrTokenNotFound) {
		t.Fatalf("token-1 should be replaced, got %v", err)
	}
	if _, err := single.GetTokenState(ctx, "token-2"); err != nil {
		t.Fatalf("token-2 should be valid: %v", err)
	}

	shared := core.NewManager(memory.NewStore(), core.WithConfig(core.Config{
		TokenName:  "token",
		Timeout:    time.Hour,
		Concurrent: true,
		ShareToken: true,
		AutoRenew:  false,
	}))
	first, err := shared.Login(ctx, "2001", "token-a")
	if err != nil {
		t.Fatalf("First shared login failed: %v", err)
	}
	second, err := shared.Login(ctx, "2001", "token-b")
	if err != nil {
		t.Fatalf("Second shared login failed: %v", err)
	}
	if second.Token != first.Token {
		t.Fatalf("ShareToken should reuse first token, got %q want %q", second.Token, first.Token)
	}
	if _, err := shared.GetTokenState(ctx, "token-b"); !errors.Is(err, core.ErrTokenNotFound) {
		t.Fatalf("token-b should not be saved, got %v", err)
	}
}

func TestManagerGetTokenStateTreatsExpiredAsNotFound(t *testing.T) {
	now := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	store := memory.NewStore(memory.WithRuntime(core.Runtime{Now: func() time.Time { return now }}))
	manager := core.NewManager(store,
		core.WithRuntime(core.Runtime{Now: func() time.Time { return now }}),
		core.WithConfig(core.Config{
			TokenName:  "token",
			Timeout:    time.Minute,
			Concurrent: true,
		}),
	)

	if _, err := manager.Login(context.Background(), "1001", "token-1"); err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	now = now.Add(2 * time.Minute)
	if _, err := manager.GetTokenState(context.Background(), "token-1"); !errors.Is(err, core.ErrTokenNotFound) {
		t.Fatalf("Expired token should return ErrTokenNotFound, got %v", err)
	}
}

func TestManagerRenewKeepsSessionAlive(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	store := memory.NewStore(memory.WithRuntime(core.Runtime{Now: func() time.Time { return now }}))
	manager := core.NewManager(store,
		core.WithRuntime(core.Runtime{Now: func() time.Time { return now }}),
		core.WithConfig(core.Config{
			TokenName:  "token",
			Timeout:    time.Minute,
			Concurrent: true,
		}),
	)

	if _, err := manager.Login(context.Background(), "1001", "token-1"); err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	now = now.Add(30 * time.Second)
	if err := manager.Renew(context.Background(), "token-1", 5*time.Minute); err != nil {
		t.Fatalf("Renew failed: %v", err)
	}

	now = now.Add(90 * time.Second)
	if _, err := manager.GetSession(context.Background(), "1001"); err != nil {
		t.Fatalf("Renew should keep session alive past original timeout: %v", err)
	}
}

func TestManagerAutoRenewKeepsSessionAlive(t *testing.T) {
	now := time.Date(2026, 6, 20, 11, 0, 0, 0, time.UTC)
	store := memory.NewStore(memory.WithRuntime(core.Runtime{Now: func() time.Time { return now }}))
	manager := core.NewManager(store,
		core.WithRuntime(core.Runtime{Now: func() time.Time { return now }}),
		core.WithConfig(core.Config{
			TokenName:     "token",
			Timeout:       time.Minute,
			ActiveTimeout: 5 * time.Minute,
			AutoRenew:     true,
			Concurrent:    true,
			ShareToken:    false,
		}),
	)

	if _, err := manager.Login(context.Background(), "1001", "token-1"); err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	now = now.Add(30 * time.Second)
	if _, err := manager.GetTokenState(context.Background(), "token-1"); err != nil {
		t.Fatalf("GetTokenState should auto renew: %v", err)
	}

	now = now.Add(90 * time.Second)
	if _, err := manager.GetSession(context.Background(), "1001"); err != nil {
		t.Fatalf("AutoRenew should keep session alive past original timeout: %v", err)
	}
}

func TestManagerLoginRefreshesExistingSessionTTL(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	store := memory.NewStore(memory.WithRuntime(core.Runtime{Now: func() time.Time { return now }}))
	manager := core.NewManager(store,
		core.WithRuntime(core.Runtime{Now: func() time.Time { return now }}),
		core.WithConfig(core.Config{
			TokenName:  "token",
			Timeout:    time.Hour,
			Concurrent: true,
		}),
	)

	if _, err := manager.Login(context.Background(), "1001", "token-1"); err != nil {
		t.Fatalf("First login failed: %v", err)
	}
	session := core.NewSession("1001")
	session.Set("theme", "dark")
	if err := manager.SaveSession(context.Background(), session); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	now = now.Add(50 * time.Minute)
	if _, err := manager.Login(context.Background(), "1001", "token-2"); err != nil {
		t.Fatalf("Second login failed: %v", err)
	}

	now = now.Add(20 * time.Minute)
	if _, err := manager.GetTokenState(context.Background(), "token-2"); err != nil {
		t.Fatalf("token-2 should still be valid: %v", err)
	}
	got, err := manager.GetSession(context.Background(), "1001")
	if err != nil {
		t.Fatalf("Second login should refresh existing session TTL: %v", err)
	}
	if value, _ := got.Get("theme"); value != "dark" {
		t.Fatalf("Session data should be preserved, got %v", value)
	}
}

func TestManagerLogoutByLoginIDAndSessionDeletion(t *testing.T) {
	ctx := context.Background()
	manager := core.NewManager(memory.NewStore(), core.WithConfig(core.DefaultConfig()))
	if _, err := manager.Login(ctx, "1001", "token-1"); err != nil {
		t.Fatalf("Login token-1 failed: %v", err)
	}
	if _, err := manager.Login(ctx, "1001", "token-2"); err != nil {
		t.Fatalf("Login token-2 failed: %v", err)
	}
	if err := manager.LogoutByLoginID(ctx, "1001", core.WithDeleteSession(true)); err != nil {
		t.Fatalf("LogoutByLoginID failed: %v", err)
	}
	if _, err := manager.GetTokenState(ctx, "token-1"); !errors.Is(err, core.ErrTokenNotFound) {
		t.Fatalf("token-1 should be missing, got %v", err)
	}
	if _, err := manager.GetTokenState(ctx, "token-2"); !errors.Is(err, core.ErrTokenNotFound) {
		t.Fatalf("token-2 should be missing, got %v", err)
	}
	if _, err := manager.GetSession(ctx, "1001"); !errors.Is(err, core.ErrSessionNotFound) {
		t.Fatalf("session should be deleted, got %v", err)
	}
}

func TestManagerAuthorization(t *testing.T) {
	ctx := context.Background()
	authorizer := core.NewMemoryAuthorizer()
	authorizer.SetRoles("1001", []string{"admin"})
	authorizer.SetPermissions("1001", []string{"user:*"})

	manager := core.NewManager(memory.NewStore(), core.WithAuthorizer(authorizer))
	authCtx := core.WithAuth(ctx, &core.AuthContext{LoginID: "1001", Token: "token-1"})

	if err := manager.CheckRole(authCtx, "admin"); err != nil {
		t.Fatalf("CheckRole failed: %v", err)
	}
	if err := manager.CheckPermission(authCtx, "user:create"); err != nil {
		t.Fatalf("CheckPermission wildcard failed: %v", err)
	}
	if err := manager.CheckAll(authCtx, core.Role("admin"), core.Permission("user:update")); err != nil {
		t.Fatalf("CheckAll failed: %v", err)
	}
	if err := manager.CheckAny(authCtx, core.Role("missing"), core.Permission("user:delete")); err != nil {
		t.Fatalf("CheckAny failed: %v", err)
	}
	if err := manager.CheckPermission(authCtx, "order:create"); !errors.Is(err, core.ErrAuthorityDenied) {
		t.Fatalf("Expected authority denied, got %v", err)
	}

	noop := core.NewManager(memory.NewStore())
	if err := noop.CheckRole(authCtx, "admin"); !errors.Is(err, core.ErrAuthorityDenied) {
		t.Fatalf("NoopAuthorizer should deny, got %v", err)
	}
}

func TestEventBusListenerErrorsAndPanicsDoNotBreakFlow(t *testing.T) {
	ctx := context.Background()
	bus := core.NewEventBus()
	var called int
	bus.Register(core.ListenerFunc(func(context.Context, core.Event) error {
		called++
		return errors.New("listener failed")
	}))
	bus.Register(core.ListenerFunc(func(context.Context, core.Event) error {
		called++
		panic("listener panic")
	}))
	bus.Register(core.ListenerFunc(func(context.Context, core.Event) error {
		called++
		return nil
	}))

	manager := core.NewManager(memory.NewStore(), core.WithEventBus(bus))
	if _, err := manager.Login(ctx, "1001", "token-1"); err != nil {
		t.Fatalf("Login should ignore listener errors: %v", err)
	}
	if called != 3 {
		t.Fatalf("Expected all listeners to be called, got %d", called)
	}
}

func TestManagerSessionScopedByLoginSubject(t *testing.T) {
	ctx := context.Background()
	manager := core.NewManager(memory.NewStore(), core.WithConfig(core.Config{
		TokenName:  "token",
		Timeout:    time.Hour,
		Concurrent: true,
	}))

	if _, err := manager.Login(ctx, "1001", "token-web"); err != nil {
		t.Fatalf("web login failed: %v", err)
	}
	if _, err := manager.Login(ctx, "1001", "token-app", core.WithLoginType("app")); err != nil {
		t.Fatalf("app login failed: %v", err)
	}

	webSession := core.NewSession("1001")
	webSession.Set("scope", "web")
	if err := manager.SaveSession(ctx, webSession); err != nil {
		t.Fatalf("save web session failed: %v", err)
	}
	appSession := core.NewSessionForSubject(core.LoginSubject{LoginID: "1001", LoginType: "app"})
	appSession.Set("scope", "app")
	if err := manager.SaveSession(ctx, appSession); err != nil {
		t.Fatalf("save app session failed: %v", err)
	}

	got, err := manager.GetSession(ctx, "1001")
	if err != nil {
		t.Fatalf("get default session failed: %v", err)
	}
	if v, _ := got.Get("scope"); v != "web" {
		t.Fatalf("default session scope = %v, want web", v)
	}
	gotApp, err := manager.GetSession(ctx, "1001", core.WithSessionLoginType("app"))
	if err != nil {
		t.Fatalf("get app session failed: %v", err)
	}
	if v, _ := gotApp.Get("scope"); v != "app" {
		t.Fatalf("app session scope = %v, want app", v)
	}

	// Logging out of the app subject must not touch the web subject.
	if err := manager.LogoutByLoginID(ctx, "1001",
		core.WithLogoutLoginType("app"),
		core.WithDeleteSession(true),
	); err != nil {
		t.Fatalf("LogoutByLoginID(app) failed: %v", err)
	}
	if _, err := manager.GetSession(ctx, "1001", core.WithSessionLoginType("app")); !errors.Is(err, core.ErrSessionNotFound) {
		t.Fatalf("app session should be deleted, got %v", err)
	}
	if _, err := manager.GetTokenState(ctx, "token-app"); !errors.Is(err, core.ErrTokenNotFound) {
		t.Fatalf("token-app should be gone, got %v", err)
	}
	if _, err := manager.GetSession(ctx, "1001"); err != nil {
		t.Fatalf("web session should survive app logout: %v", err)
	}
	if _, err := manager.GetTokenState(ctx, "token-web"); err != nil {
		t.Fatalf("token-web should survive app logout: %v", err)
	}
}

func TestAuthContextHelpers(t *testing.T) {
	ctx := context.Background()
	if core.IsAuthenticated(ctx) {
		t.Fatal("empty context should not be authenticated")
	}
	if _, err := core.RequireAuth(ctx); !errors.Is(err, core.ErrNotLogin) {
		t.Fatalf("RequireAuth should fail, got %v", err)
	}

	auth := &core.AuthContext{LoginID: "1001", Token: "token-1"}
	ctx = core.WithAuth(ctx, auth)
	if loginID, err := core.RequireLoginID(ctx); err != nil || loginID != "1001" {
		t.Fatalf("RequireLoginID = %q, %v", loginID, err)
	}
	if token, ok := core.TokenFromContext(ctx); !ok || token != "token-1" {
		t.Fatalf("TokenFromContext = %q, %v", token, ok)
	}
}
