package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
	"github.com/apus-run/better-token/token"
)

func main() {
	ctx := context.Background()
	store := memory.NewStore()
	manager := core.NewManager(
		store,
		core.WithConfig(core.Config{
			TokenName:    "token",
			Timeout:      core.DefaultConfig().Timeout,
			Concurrent:   true,
			RequireNonce: true,
		}),
		core.WithNonceConfig(core.NonceConfig{
			Timeout: 2 * time.Minute,
			Length:  32,
		}),
	)

	nonce, err := manager.GenerateNonce(ctx, core.WithNoncePurpose("login"))
	if err != nil {
		log.Fatalf("generate nonce: %v", err)
	}

	generator := token.NewTokenGenerator[any](token.WithTokenStyle(token.TokenStyleSimple))
	tokenStr, err := generator.GenerateToken("1001", nil)
	if err != nil {
		log.Fatalf("generate token: %v", err)
	}

	state, err := manager.Login(ctx, "1001", core.TokenValue(tokenStr), core.WithNonce(nonce))
	if err != nil {
		log.Fatalf("login with nonce: %v", err)
	}
	fmt.Printf("login ok: loginID=%s token=%s\n", state.LoginID, state.Token)
}
