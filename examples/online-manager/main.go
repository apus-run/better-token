package main

import (
	"context"
	"fmt"
	"log"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
)

func main() {
	ctx := context.Background()
	manager := core.NewManager(memory.NewStore())

	if _, err := manager.Login(ctx, "1001", "ios-token", core.WithDevice("ios")); err != nil {
		log.Fatalf("login ios: %v", err)
	}
	if _, err := manager.Login(ctx, "1001", "web-token", core.WithDevice("web")); err != nil {
		log.Fatalf("login web: %v", err)
	}

	// 在 access token 上记录在线投影（IP / UA / 连接 ID）。
	if err := manager.MarkOnline(ctx, "web-token", core.OnlineInfo{IP: "10.0.0.1", UserAgent: "chrome"}); err != nil {
		log.Fatalf("mark online: %v", err)
	}

	states, err := manager.ListTokenStates(ctx, "1001")
	if err != nil {
		log.Fatalf("list online tokens: %v", err)
	}
	fmt.Printf("online tokens: %d\n", len(states))

	if err := manager.LogoutByDevice(ctx, "1001", "ios"); err != nil {
		log.Fatalf("logout ios: %v", err)
	}

	states, err = manager.ListTokenStates(ctx, "1001")
	if err != nil {
		log.Fatalf("list online tokens after logout: %v", err)
	}
	fmt.Printf("online tokens after ios logout: %d\n", len(states))
}
