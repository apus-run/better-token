package main

import (
	"context"
	"fmt"
	"log"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
	"github.com/apus-run/better-token/token"
)

// UserClaims 定义 JWT 中存储的自定义业务数据
type UserClaims struct {
	UserID  string `json:"user_id"`  // 用户ID，核心标识
	TrackID string `json:"track_id"` // 追踪ID，用于日志链路追踪
	Role    string `json:"role"`     // 用户角色，用于权限控制
	Device  string `json:"device"`   // 设备标识，用于多设备管理
	Version int    `json:"ver"`      // Token 版本，用于版本管理
}

func main() {
	ctx := context.Background()

	// 1. 初始化 JWT Manager 并指定自定义数据类型
	jwtManager := token.NewJwtManager[UserClaims](
		token.WithSecretKey("your-jwt-secret-key-change-in-production"),
		token.WithIssuer("better-token-demo"),
		token.WithExpiry(2*60*60*1000000000), // 2小时
	)

	// 2. 创建业务数据（模拟登录场景）
	userData := UserClaims{
		UserID:  "10086",
		TrackID: "track_abc_12345",
		Role:    "admin",
		Device:  "web-chrome",
		Version: 1,
	}

	// 3. 生成包含自定义数据的 JWT Token
	jwtToken, err := jwtManager.GenerateToken(userData.UserID, userData)
	if err != nil {
		log.Fatalf("生成 JWT 失败: %v", err)
	}
	fmt.Printf("✅ 生成 JWT Token:\n%s\n\n", jwtToken)

	// 4. 解析 JWT Token 并提取业务数据
	claims, err := jwtManager.ParseToken(jwtToken)
	if err != nil {
		log.Fatalf("解析 JWT 失败: %v", err)
	}

	fmt.Println("📋 解析出的 Token 内容:")
	fmt.Printf("  Subject (UserID): %s\n", claims.Subject)
	fmt.Printf("  UserID:     %s\n", claims.Data.UserID)
	fmt.Printf("  TrackID:    %s\n", claims.Data.TrackID)
	fmt.Printf("  Role:       %s\n", claims.Data.Role)
	fmt.Printf("  Device:     %s\n", claims.Data.Device)
	fmt.Printf("  Version:    %d\n", claims.Data.Version)
	fmt.Printf("  ExpiresAt:  %s\n", claims.ExpiresAt.Time.Format("2006-01-02 15:04:05"))
	fmt.Printf("  IssuedAt:   %s\n", claims.IssuedAt.Time.Format("2006-01-02 15:04:05"))
	fmt.Println()

	// 5. 验证 Token
	valid, err := jwtManager.VerifyToken(jwtToken)
	if err != nil {
		log.Fatalf("验证 Token 失败: %v", err)
	}
	fmt.Printf("🔐 Token 验证结果: %v\n\n", valid)

	// 6. 结合 better-token 的 TokenState 管理
	fmt.Println("===== 结合 better-token 会话管理 =====")
	store := memory.NewStore()
	manager := core.NewManager(store)

	// 使用 JWT 作为 access token，配合 refresh token
	refreshToken, _ := token.NewTokenGenerator[any](token.WithTokenStyle(token.TokenStyleUUID)).
		GenerateToken(userData.UserID, nil)

	// 登录并存储会话状态
	result, err := manager.LoginWithRefresh(
		ctx,
		userData.UserID,
		core.TokenValue(jwtToken),
		core.TokenValue(refreshToken),
		core.WithDevice(userData.Device),
	)
	if err != nil {
		log.Fatalf("登录失败: %v", err)
	}
	fmt.Printf("✅ 用户 %s 登录成功\n", userData.UserID)
	fmt.Printf("  Access Token:  %s\n", result.TokenState.Token)
	fmt.Printf("  Refresh Token: %s\n", result.RefreshState.Token)
	fmt.Printf("  设备:          %s\n", result.TokenState.Device)
	fmt.Println()

	// 7. 业务场景：从请求中提取 Token 后解析出数据使用
	fmt.Println("===== 业务场景：使用 Token 中的数据 =====")
	// 首先验证 token 是否在服务端有效（是否被踢下线、是否过期）
	tokenState, err := manager.GetTokenState(ctx, core.TokenValue(jwtToken))
	if err != nil {
		log.Fatalf("服务端认证失败: %v", err)
	}
	fmt.Printf("  Token 状态: %s\n", tokenState.Status)
	fmt.Printf("  登录ID:     %s\n", tokenState.LoginID)

	// 然后解析 JWT 中的业务数据
	jwtClaims, err := jwtManager.ParseToken(jwtToken)
	if err != nil {
		log.Fatalf("业务数据解析失败: %v", err)
	}

	// 使用解析出的数据执行业务逻辑
	businessLogic(jwtClaims)

	// 8. Token 续期（通常在中间件或刷新接口中执行）
	fmt.Println("\n===== Token 续期 =====")
	newUserData := UserClaims{
		UserID:  jwtClaims.Data.UserID,
		TrackID: "track_new_67890", // 续期时可以更新追踪ID
		Role:    jwtClaims.Data.Role,
		Device:  jwtClaims.Data.Device,
		Version: jwtClaims.Data.Version + 1, // 版本号+1
	}
	newJwtToken, err := jwtManager.GenerateToken(newUserData.UserID, newUserData)
	if err != nil {
		log.Fatalf("续期 Token 失败: %v", err)
	}
	fmt.Printf("🔄 续期后的 Token (版本 %d):\n%s\n", newUserData.Version, newJwtToken)

	// 验证续期后的 Token
	newClaims, _ := jwtManager.ParseToken(newJwtToken)
	fmt.Printf("  新 TrackID: %s\n", newClaims.Data.TrackID)
	fmt.Printf("  新版本:    %d\n", newClaims.Data.Version)
}

// businessLogic 模拟业务逻辑，使用 JWT 中的自定义数据
func businessLogic(claims *token.Claims[UserClaims]) {
	fmt.Printf("\n📊 执行业务逻辑:\n")
	fmt.Printf("  用户ID:   %s - 用于查询用户信息、数据隔离\n", claims.Data.UserID)
	fmt.Printf("  追踪ID:   %s - 用于日志链路追踪、问题定位\n", claims.Data.TrackID)
	fmt.Printf("  角色:     %s - 用于权限判断、控制访问范围\n", claims.Data.Role)
	fmt.Printf("  设备:     %s - 用于多设备管理、风险控制\n", claims.Data.Device)
	fmt.Printf("  Token版本: %d - 用于版本兼容、旧版本失效\n", claims.Data.Version)

	// 实际业务场景示例:
	// 1. 查询用户数据: db.Query("SELECT * FROM users WHERE id = ?", claims.Data.UserID)
	// 2. 日志记录: log.WithField("track_id", claims.Data.TrackID).Info("用户操作")
	// 3. 权限校验: if claims.Data.Role != "admin" { return error }
	// 4. 设备检查: if claims.Data.Device != request.Device { 异地登录预警 }
}
