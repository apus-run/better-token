package redis_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
)

func TestLegacySessionMigrationDoesNotResurrectExpiredData(t *testing.T) {
	s, mr := newStore(t)
	ctx := context.Background()
	session := core.NewSession("alice")
	session.Set("value", "legacy")
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := "bt:session:login:alice"
	mr.Set(oldKey, string(raw))
	mr.SetTTL(oldKey, time.Hour)
	got, found, err := s.GetSession(ctx, session.Subject)
	if err != nil || !found || got.Data["value"] != "legacy" {
		t.Fatalf("legacy read=%+v,%v,%v", got, found, err)
	}
	if err = s.EnsureSession(ctx, session.Subject, time.Minute); err != nil {
		t.Fatal(err)
	}
	if mr.Exists(oldKey) {
		t.Fatal("migration left old key available for fallback")
	}
	got, found, err = s.GetSession(ctx, session.Subject)
	if err != nil || !found || got.Data["value"] != "legacy" {
		t.Fatal("migration lost existing Data")
	}
	mr.FastForward(2 * time.Minute)
	if _, found, err = s.GetSession(ctx, session.Subject); err != nil || found {
		t.Fatal("expired session reappeared from legacy key")
	}
	if err = s.EnsureSession(ctx, session.Subject, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.GetSession(ctx, session.Subject)
	if err != nil || len(got.Data) != 0 {
		t.Fatal("expired data was retained")
	}
}
func TestLegacyCollidingSubjectsRemainIsolated(t *testing.T) {
	s, mr := newStore(t)
	ctx := context.Background()
	a := core.NewSessionForSubject(core.LoginSubject{LoginType: "a:b", LoginID: "c"})
	b := core.NewSessionForSubject(core.LoginSubject{LoginType: "a", LoginID: "b:c"})
	b.Set("owner", "b")
	raw, _ := json.Marshal(b)
	oldKey := "bt:session:a:b:c"
	mr.Set(oldKey, string(raw))
	if _, found, err := s.GetSession(ctx, a.Subject); err != nil || found {
		t.Fatal("legacy key granted another subject's session")
	}
	if err := s.SaveSession(ctx, a, time.Minute); err != nil {
		t.Fatal(err)
	}
	if !mr.Exists(oldKey) {
		t.Fatal("saving subject a deleted subject b's legacy session")
	}
	if err := s.DeleteSession(ctx, a.Subject); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.GetSession(ctx, b.Subject)
	if err != nil || !found || got.Data["owner"] != "b" {
		t.Fatal("subject b's legacy data was changed")
	}
}
func TestLegacyConsumedMarkerIsReadAndCannotResetByRenew(t *testing.T) {
	s, mr := newStore(t)
	ctx := context.Background()
	n := nonceState("key")
	n.LoginID = "alice"
	if err := s.SaveTokenState(ctx, n, time.Hour); err != nil {
		t.Fatal(err)
	}
	mr.Set("bt:token:key:consumed", "1")
	mr.SetTTL("bt:token:key:consumed", time.Hour)
	snapshot, found, err := s.GetTokenState(ctx, "key")
	if err != nil || !found || !snapshot.IsConsumed() {
		t.Fatal("marker ignored by Get")
	}
	next := snapshot.Clone()
	next.Touch(time.Now())
	updated, err := s.CompareAndSwapTokenState(ctx, snapshot, next, time.Hour)
	if err != nil || !updated {
		t.Fatalf("CAS=%v,%v", updated, err)
	}
	snapshot, found, err = s.ConsumeTokenState(ctx, "key")
	if err != nil || !found || !snapshot.IsConsumed() {
		t.Fatal("legacy consumption was lost during write")
	}
}
