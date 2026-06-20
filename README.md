# better-token

`better-token` 是一个与具体 Web 框架解耦的 Go 认证授权核心库。

第一版核心边界：

```text
token    生成 token、签发 JWT、解析 JWT、校验 JWT
core     管理服务端登录态 TokenState、Session、授权判断、事件发布、认证上下文
storage  保存 TokenState / Session
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

## 第一版不做

- RefreshToken
- Nonce
- OAuth2
- SSO
- OnlineManager
- DistributedSession
- 异步 EventBus
- RBAC 数据库表
- 纯无状态 JWT 模式

