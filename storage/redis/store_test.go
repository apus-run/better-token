package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/apus-run/gala/components/rdb"

	"github.com/apus-run/better-token/core"
	redisstore "github.com/apus-run/better-token/storage/redis"
)

func newStore(t *testing.T) (*redisstore.Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	provider, err := rdb.NewRDB("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("NewRDB failed: %v", err)
	}
	return redisstore.NewStore(provider), mr
}

func tokenState(token, loginID, loginType string) *core.TokenState {
	return &core.TokenState{
		Token:     core.TokenValue(token),
		LoginID:   loginID,
		LoginType: loginType,
	}
}

func TestStoreTokenStateLifecycle(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	if err := store.SaveTokenState(ctx, tokenState("t1", "1001", "login"), time.Hour); err != nil {
		t.Fatalf("SaveTokenState failed: %v", err)
	}

	got, ok, err := store.GetTokenState(ctx, "t1")
	if err != nil || !ok {
		t.Fatalf("GetTokenState = %v, ok=%v, err=%v", got, ok, err)
	}
	if got.LoginID != "1001" || got.LoginType != "login" {
		t.Fatalf("unexpected state: %#v", got)
	}

	if err := store.DeleteTokenState(ctx, "t1"); err != nil {
		t.Fatalf("DeleteTokenState failed: %v", err)
	}
	if _, ok, _ := store.GetTokenState(ctx, "t1"); ok {
		t.Fatal("token should be gone after delete")
	}
	if err := store.DeleteTokenState(ctx, "t1"); err != nil {
		t.Fatalf("DeleteTokenState should be idempotent: %v", err)
	}
}

func TestStoreFindTokenStatesIsolatedBySubject(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	_ = store.SaveTokenState(ctx, tokenState("web1", "1001", "web"), time.Hour)
	_ = store.SaveTokenState(ctx, tokenState("web2", "1001", "web"), time.Hour)
	_ = store.SaveTokenState(ctx, tokenState("app1", "1001", "app"), time.Hour)
	_ = store.SaveTokenState(ctx, tokenState("other", "2002", "web"), time.Hour)

	web, err := store.FindTokenStates(ctx, core.LoginSubject{LoginID: "1001", LoginType: "web"})
	if err != nil {
		t.Fatalf("FindTokenStates failed: %v", err)
	}
	if len(web) != 2 {
		t.Fatalf("expected 2 web tokens for 1001, got %d", len(web))
	}

	app, _ := store.FindTokenStates(ctx, core.LoginSubject{LoginID: "1001", LoginType: "app"})
	if len(app) != 1 || app[0].Token != "app1" {
		t.Fatalf("expected only app1 for app subject, got %#v", app)
	}
}

func TestStoreSaveTokenStateRebindsIndexOnSubjectChange(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	_ = store.SaveTokenState(ctx, tokenState("t1", "1001", "web"), time.Hour)
	_ = store.SaveTokenState(ctx, tokenState("t1", "1001", "app"), time.Hour)

	web, _ := store.FindTokenStates(ctx, core.LoginSubject{LoginID: "1001", LoginType: "web"})
	if len(web) != 0 {
		t.Fatalf("old web index should be cleared, got %d", len(web))
	}
	app, _ := store.FindTokenStates(ctx, core.LoginSubject{LoginID: "1001", LoginType: "app"})
	if len(app) != 1 {
		t.Fatalf("token should now be under app subject, got %d", len(app))
	}
}

func TestStoreDeleteTokenStatesBySubject(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	_ = store.SaveTokenState(ctx, tokenState("a", "1001", "web"), time.Hour)
	_ = store.SaveTokenState(ctx, tokenState("b", "1001", "web"), time.Hour)
	_ = store.SaveTokenState(ctx, tokenState("c", "2002", "web"), time.Hour)

	if err := store.DeleteTokenStates(ctx, core.LoginSubject{LoginID: "1001", LoginType: "web"}); err != nil {
		t.Fatalf("DeleteTokenStates failed: %v", err)
	}
	if _, ok, _ := store.GetTokenState(ctx, "a"); ok {
		t.Fatal("token a should be deleted")
	}
	if _, ok, _ := store.GetTokenState(ctx, "b"); ok {
		t.Fatal("token b should be deleted")
	}
	if _, ok, _ := store.GetTokenState(ctx, "c"); !ok {
		t.Fatal("token c (other subject) must survive")
	}
}

func TestStoreTokenStatePhysicalExpiry(t *testing.T) {
	store, mr := newStore(t)
	ctx := context.Background()

	_ = store.SaveTokenState(ctx, tokenState("t1", "1001", "login"), time.Minute)
	mr.FastForward(2 * time.Minute) // 物理过期

	if _, ok, _ := store.GetTokenState(ctx, "t1"); ok {
		t.Fatal("expired token should not be returned")
	}
	// Find 应自愈索引中残留的过期成员。
	states, _ := store.FindTokenStates(ctx, core.LoginSubject{LoginID: "1001", LoginType: "login"})
	if len(states) != 0 {
		t.Fatalf("expired token should be evicted from index, got %d", len(states))
	}
}

func TestStoreReturnedStateIsCopy(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	_ = store.SaveTokenState(ctx, tokenState("t1", "1001", "login"), time.Hour)

	got, _, _ := store.GetTokenState(ctx, "t1")
	got.LoginID = "mutated"

	again, _, _ := store.GetTokenState(ctx, "t1")
	if again.LoginID != "1001" {
		t.Fatalf("store state mutated through returned copy: %q", again.LoginID)
	}
}

func TestStoreSessionPerSubjectIsolation(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	webSubject := core.LoginSubject{LoginID: "1001", LoginType: "web"}
	appSubject := core.LoginSubject{LoginID: "1001", LoginType: "app"}

	webSession := core.NewSessionForSubject(webSubject)
	webSession.Set("scope", "web")
	appSession := core.NewSessionForSubject(appSubject)
	appSession.Set("scope", "app")

	if err := store.SaveSession(ctx, webSession, time.Hour); err != nil {
		t.Fatalf("SaveSession(web) failed: %v", err)
	}
	if err := store.SaveSession(ctx, appSession, time.Hour); err != nil {
		t.Fatalf("SaveSession(app) failed: %v", err)
	}

	got, ok, err := store.GetSession(ctx, webSubject)
	if err != nil || !ok {
		t.Fatalf("GetSession(web) = ok %v, err %v", ok, err)
	}
	if v, _ := got.Get("scope"); v != "web" {
		t.Fatalf("web session scope = %v", v)
	}

	if err := store.DeleteSession(ctx, appSubject); err != nil {
		t.Fatalf("DeleteSession(app) failed: %v", err)
	}
	if _, ok, _ := store.GetSession(ctx, appSubject); ok {
		t.Fatal("app session should be deleted")
	}
	if _, ok, _ := store.GetSession(ctx, webSubject); !ok {
		t.Fatal("web session must survive app deletion")
	}
}

func TestStoreSessionEmptySubjectRejected(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	if err := store.SaveSession(ctx, core.NewSessionForSubject(core.LoginSubject{}), time.Hour); err == nil {
		t.Fatal("SaveSession with empty subject should error")
	}
}

func TestStoreSessionExpiry(t *testing.T) {
	store, mr := newStore(t)
	ctx := context.Background()

	subject := core.LoginSubject{LoginID: "1001", LoginType: "login"}
	_ = store.SaveSession(ctx, core.NewSessionForSubject(subject), time.Minute)

	mr.FastForward(2 * time.Minute)
	if _, ok, _ := store.GetSession(ctx, subject); ok {
		t.Fatal("expired session should not be returned")
	}
}

func TestStoreSubjectNormalizationOnLookup(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	_ = store.SaveTokenState(ctx, tokenState("t1", "1001", ""), time.Hour)

	states, _ := store.FindTokenStates(ctx, core.LoginSubject{LoginID: "1001"})
	if len(states) != 1 {
		t.Fatalf("normalization mismatch: expected 1, got %d", len(states))
	}
	states2, _ := store.FindTokenStates(ctx, core.LoginSubject{LoginID: "1001", LoginType: core.DefaultLoginType})
	if len(states2) != 1 {
		t.Fatalf("explicit default loginType mismatch: got %d", len(states2))
	}
}

func TestStoreKeyPrefixIsolation(t *testing.T) {
	mr := miniredis.RunT(t)
	provider, err := rdb.NewRDB("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("NewRDB failed: %v", err)
	}
	ctx := context.Background()

	a := redisstore.NewStore(provider, redisstore.WithKeyPrefix("appA"))
	b := redisstore.NewStore(provider, redisstore.WithKeyPrefix("appB"))

	_ = a.SaveTokenState(ctx, tokenState("t1", "1001", "login"), time.Hour)

	if _, ok, _ := a.GetTokenState(ctx, "t1"); !ok {
		t.Fatal("appA should see its own token")
	}
	if _, ok, _ := b.GetTokenState(ctx, "t1"); ok {
		t.Fatal("appB must not see appA's token")
	}
}

func TestStoreContextCancellation(t *testing.T) {
	store, _ := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.SaveTokenState(ctx, tokenState("t1", "1001", "login"), time.Hour); err == nil {
		t.Fatal("cancelled context should fail SaveTokenState")
	}
	if _, _, err := store.GetTokenState(ctx, "t1"); err == nil {
		t.Fatal("cancelled context should fail GetTokenState")
	}
	if _, err := store.FindTokenStates(ctx, core.LoginSubject{LoginID: "1001"}); err == nil {
		t.Fatal("cancelled context should fail FindTokenStates")
	}
}
