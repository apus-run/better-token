package main

import (
	"context"
	"fmt"
	"log"

	"github.com/apus-run/better-token/audit"
	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
)

func main() {
	ctx := context.Background()

	// 审计监听器（默认 slog 输出）注册到事件总线；监听器异常不影响主认证流程。
	bus := core.NewEventBus()
	bus.Register(audit.New(audit.NewSlogSink(nil)))

	manager := core.NewManager(memory.NewStore(), core.WithEventBus(bus))

	if _, err := manager.Login(ctx, "1001", "access-1", core.WithDevice("web")); err != nil {
		log.Fatalf("login: %v", err)
	}
	if err := manager.Logout(ctx, "access-1"); err != nil {
		log.Fatalf("logout: %v", err)
	}

	nonce, err := manager.GenerateNonce(ctx, core.WithNoncePurpose("login"))
	if err != nil {
		log.Fatalf("generate nonce: %v", err)
	}
	if _, err := manager.ConsumeNonce(ctx, nonce); err != nil {
		log.Fatalf("consume nonce: %v", err)
	}

	fmt.Println("audit events emitted for login / logout / nonce_consumed")
}
