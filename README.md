# better-token

`better-token` 是一个与具体 Web 框架解耦的 Go 认证授权核心库。

核心边界：

```text
token    生成 token、签发 JWT、解析 JWT、校验 JWT
core     管理服务端登录态 TokenState、RefreshTokenState、Nonce、Session、授权判断、事件发布、认证上下文
storage  保存 TokenState / RefreshTokenState / Nonce / Session
plugins  接入 net/http、gin 等 Web 框架
```

## 基本用法

```go
package main

import (
	"context"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
	"github.com/apus-run/better-token/token"
)

func main() {
	ctx := context.Background()

	store := memory.NewStore()
	manager := core.NewManager(store)

	generator := token.NewTokenGenerator[any](
		token.WithTokenStyle(token.TokenStyleSimple),
	)

	tokenStr, err := generator.GenerateToken("1001", nil)
	if err != nil {
		panic(err)
	}

	state, err := manager.Login(ctx, "1001", core.TokenValue(tokenStr))
	if err != nil {
		panic(err)
	}

	_ = state
}
```

`core.Manager` 不生成 token，也不解析 JWT。调用方先用 `token.TokenGenerator` 或 `token.JwtManager` 生成 token 字符串，再调用 `core.Manager.Login` 保存服务端登录态。

## JWT 作为 TokenValue

```go
jwtManager := token.NewJwtManager[map[string]string](
	token.WithSecretKey("secret"),
)

jwtToken, err := jwtManager.GenerateToken("1001", map[string]string{
	"tenant": "acme",
})
if err != nil {
	panic(err)
}

state, err := manager.Login(ctx, "1001", core.TokenValue(jwtToken))
if err != nil {
	panic(err)
}

_ = state
```

JWT 是 token 的一种表达形式。服务端是否承认该 token 登录，由 `core.Store` 中的 `TokenState` 决定。

## 第二阶段能力

第二阶段能力都是增量启用，默认不改变第一版登录态行为：

- RefreshToken：通过 `core.RefreshManager` 显式启用。`core.Manager.Login` 默认不会签发 refresh token。
- Nonce：通过 `core.NonceManager` 生成和消费。`core.Config.RequireNonce` 默认是 `false`。
- OnlineManager：通过 `Manager.ListTokenStates` 查询在线 token，通过 `Manager.LogoutByDevice` 按设备踢下线。
- DistributedSession：复用现有 `Session`，在 Redis/database store 下由同一 `LoginSubject` 实现跨实例共享。
- AsyncEventBus：`core.NewAsyncEventBus` 实现现有 `core.EventBus` 接口，可通过 `core.WithEventBus` 注入。
- RBAC helper：`rbac.Authorizer` 是可选包，实现 `core.Authorizer`，适合小项目快速绑定 role 和 permission。

access token、refresh token 和 server-side `TokenState` 的关系：

- access token 是客户端访问受保护接口时携带的凭证。
- refresh token 只用于换新 access token，不应该用于普通接口鉴权。
- `TokenState` 是服务端是否承认 access token 登录的最终依据。
- `RefreshTokenState` 是服务端是否允许 refresh token 换新的最终依据。

## RefreshToken

```go
store := memory.NewStore()
manager := core.NewManager(store)
refreshManager := core.NewRefreshManager(manager, store)

result, err := refreshManager.Login(ctx, "1001", "access-token", "refresh-token")
if err != nil {
	panic(err)
}

refreshed, err := refreshManager.Refresh(
	ctx,
	result.RefreshTokenState.Token,
	"next-access-token",
	core.WithNextRefreshToken("next-refresh-token"),
)
if err != nil {
	panic(err)
}

_ = refreshed
```

`core` 仍然不生成 token。调用方可以继续使用 `token.TokenGenerator`、`token.JwtManager` 或业务自己的签发器生成 access token 和 refresh token 字符串。

## Nonce 防重放

```go
store := memory.NewStore()
nonceManager := core.NewNonceManager(store)
manager := core.NewManager(
	store,
	core.WithConfig(core.Config{
		TokenName:    "token",
		Timeout:      core.DefaultConfig().Timeout,
		Concurrent:   true,
		RequireNonce: true,
	}),
	core.WithNonceConsumer(nonceManager),
)

nonce, err := nonceManager.Generate(ctx, core.WithNoncePurpose("login"))
if err != nil {
	panic(err)
}

state, err := manager.Login(ctx, "1001", "access-token", core.WithNonce(nonce))
if err != nil {
	panic(err)
}

_ = state
```

同一个 nonce 只能被成功消费一次。重复消费会返回 `core.ErrNonceReplayed`。

## 在线 token 与按设备踢下线

```go
_, _ = manager.Login(ctx, "1001", "ios-token", core.WithDevice("ios"))
_, _ = manager.Login(ctx, "1001", "web-token", core.WithDevice("web"))

states, err := manager.ListTokenStates(ctx, "1001")
if err != nil {
	panic(err)
}

err = manager.LogoutByDevice(ctx, "1001", "ios")
if err != nil {
	panic(err)
}

_ = states
```

## 数据库存储迁移

`storage/database` 使用 GORM `AutoMigrate` 建表。使用数据库 store 时，在创建 `core.Manager` 前调用一次：

```go
store := database.NewStore(provider)
if err := store.Migrate(ctx); err != nil {
	panic(err)
}
```

第二阶段会额外创建 `refresh_token_states` 和 `nonce_states` 表；已有 `token_states` 和 `sessions` 表保持兼容。

## net/http 中间件

```go
package main

import (
	"net/http"

	"github.com/apus-run/better-token/core"
	btHTTP "github.com/apus-run/better-token/plugins/http"
	"github.com/apus-run/better-token/storage/memory"
)

func main() {
	manager := core.NewManager(memory.NewStore(), core.WithConfig(core.Config{
		TokenName:   "Authorization",
		TokenPrefix: "Bearer",
		Timeout:     core.DefaultConfig().Timeout,
		Concurrent:  true,
	}))

	mux := http.NewServeMux()
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		loginID, err := core.RequireLoginID(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(loginID))
	})

	http.ListenAndServe(":8080", btHTTP.Middleware(manager)(mux))
}
```

## gin 中间件

```go
router := gin.New()
router.Use(btGin.Middleware(manager))
router.GET("/me", func(c *gin.Context) {
	loginID, err := core.RequireLoginID(c.Request.Context())
	if err != nil {
		c.AbortWithStatus(401)
		return
	}
	c.String(200, loginID)
})
```

## 示例

更多完整示例：

- `examples/basic`
- `examples/refresh-token`
- `examples/nonce`
- `examples/online-manager`

## 暂不做

- OAuth2
- SSO
- RBAC 数据库表
- 纯无状态 JWT 模式
