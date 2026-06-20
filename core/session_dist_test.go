package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
)

// TestDistributedSessionSharedAcrossManagers 验证多个 Manager 实例共享同一 Store 时，
// 一端写入的 Session 可被另一端读取——这是 DistributedSession 的核心契约：
// 分布式语义由共享 Store 承载，而非单个 Manager 实例的内存状态。
func TestDistributedSessionSharedAcrossManagers(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	writer := core.NewManager(store, core.WithConfig(core.Config{TokenName: "token", Timeout: time.Hour, Concurrent: true}))
	reader := core.NewManager(store, core.WithConfig(core.Config{TokenName: "token", Timeout: time.Hour, Concurrent: true}))

	session := core.NewSessionForSubject(core.LoginSubject{LoginID: "1001"}.Normalize())
	session.Set("scope", "admin")
	if err := writer.SaveSession(ctx, session); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	got, err := reader.GetSession(ctx, "1001")
	if err != nil {
		t.Fatalf("GetSession from second manager failed: %v", err)
	}
	if v, _ := got.Get("scope"); v != "admin" {
		t.Fatalf("cross-instance session value = %v, want admin", v)
	}

	if err := writer.DeleteSession(ctx, "1001"); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}
	if _, err := reader.GetSession(ctx, "1001"); err == nil {
		t.Fatal("session deleted by one manager should be gone for the other")
	}
}
