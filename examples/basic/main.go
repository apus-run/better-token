// Package main 演示 better-token 第一版新架构的最小使用流程：
// token 生成 -> core 登录态保存 -> 校验 -> 授权 -> 注销。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
	"github.com/apus-run/better-token/token"
)

func main() {
	ctx := context.Background()

	authorizer := core.NewMemoryAuthorizer()
	authorizer.SetRoles("1001", []string{"admin"})
	authorizer.SetPermissions("1001", []string{"user:read"})

	manager := core.NewManager(
		memory.NewStore(),
		core.WithAuthorizer(authorizer),
	)

	generator := token.NewTokenGenerator[any](
		token.WithTokenStyle(token.TokenStyleSimple),
	)
	tokenStr, err := generator.GenerateToken("1001", nil)
	if err != nil {
		log.Fatalf("generate token: %v", err)
	}

	state, err := manager.Login(ctx, "1001", core.TokenValue(tokenStr))
	if err != nil {
		log.Fatalf("login: %v", err)
	}
	fmt.Printf("login ok: loginID=%s token=%s\n", state.LoginID, state.Token)

	checked, err := manager.GetTokenState(ctx, core.TokenValue(tokenStr))
	if err != nil {
		log.Fatalf("check login: %v", err)
	}
	fmt.Printf("check ok: loginID=%s\n", checked.LoginID)

	authCtx := core.WithAuth(ctx, core.NewAuthContext(checked))
	if err := manager.CheckPermission(authCtx, "user:read"); err != nil {
		log.Fatalf("check permission user:read: %v", err)
	}
	fmt.Println("permission user:read: granted")

	if err := manager.CheckPermission(authCtx, "user:write"); err != nil {
		fmt.Printf("permission user:write: denied (expected): %v\n", err)
	}

	if err := manager.Logout(ctx, core.TokenValue(tokenStr)); err != nil {
		log.Fatalf("logout: %v", err)
	}
	if _, err := manager.GetTokenState(ctx, core.TokenValue(tokenStr)); !errors.Is(err, core.ErrTokenNotFound) {
		log.Fatalf("logout failed to invalidate token: %v", err)
	}
	fmt.Println("logout ok")
}
