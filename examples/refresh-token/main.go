package main

import (
	"context"
	"fmt"
	"log"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
	"github.com/apus-run/better-token/token"
)

func main() {
	ctx := context.Background()
	store := memory.NewStore()
	manager := core.NewManager(store)

	generator := token.NewTokenGenerator[any](token.WithTokenStyle(token.TokenStyleSimple))
	accessToken, err := generator.GenerateToken("1001", nil)
	if err != nil {
		log.Fatalf("generate access token: %v", err)
	}
	refreshToken, err := generator.GenerateToken("1001", nil)
	if err != nil {
		log.Fatalf("generate refresh token: %v", err)
	}

	result, err := manager.LoginWithRefresh(ctx, "1001", core.TokenValue(accessToken), core.TokenValue(refreshToken), core.WithDevice("web"))
	if err != nil {
		log.Fatalf("login with refresh token: %v", err)
	}
	fmt.Printf("login ok: access=%s refresh=%s\n", result.TokenState.Token, result.RefreshState.Token)

	nextAccessToken, err := generator.GenerateToken("1001", nil)
	if err != nil {
		log.Fatalf("generate next access token: %v", err)
	}
	nextRefreshToken, err := generator.GenerateToken("1001", nil)
	if err != nil {
		log.Fatalf("generate next refresh token: %v", err)
	}

	refreshed, err := manager.Refresh(
		ctx,
		core.TokenValue(refreshToken),
		core.TokenValue(nextAccessToken),
		core.WithNextRefreshToken(core.TokenValue(nextRefreshToken)),
	)
	if err != nil {
		log.Fatalf("refresh access token: %v", err)
	}
	fmt.Printf("refresh ok: access=%s refresh=%s\n", refreshed.TokenState.Token, refreshed.RefreshState.Token)
}
