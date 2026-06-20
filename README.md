# better-token

`better-token` 是一个与具体 Web 框架解耦的 Go 认证授权核心库。

核心边界：

```text
token    生成 token、签发 JWT、解析 JWT、校验 JWT
core     管理统一 TokenState（access/refresh/nonce）、Session、授权判断、事件发布、认证上下文
storage  保存统一 TokenState 与 Session
plugins  接入 net/http、gin、gRPC 等框架
```

## 能力总览（三阶段）

- **v1 核心**：`Login` / `Logout` / `GetTokenState` / `Renew` / Session / 授权 `Check*`，以统一 `TokenState` 为登录态模型。
- **第二阶段扩展**：RefreshToken、Nonce 防重放、在线 token 查询与按设备踢下线、DistributedSession、`AsyncEventBus`、`rbac` helper——全部通过 `core.Manager` 的方法直接调用。
- **第三阶段适配与审计**：`plugins/grpc`（server + client 拦截器）、`audit`（结构化审计监听器，默认 slog 输出）。

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

第二阶段能力都是增量启用，默认不改变第一版登录态行为，且全部通过 `core.Manager` 直接调用（不再有独立的 RefreshManager / NonceManager）：

- RefreshToken：`Manager.LoginWithRefresh` 签发，`Manager.Refresh` 换新。`core.Manager.Login` 默认不会签发 refresh token。
- Nonce：`Manager.GenerateNonce` 生成、`Manager.ConsumeNonce` 消费。`core.Config.RequireNonce` 默认是 `false`。
- OnlineManager：`Manager.ListTokenStates` 查询在线 token，`Manager.LogoutByDevice` 按设备踢下线，`Manager.MarkOnline` / `MarkOffline` 维护在线投影。
- DistributedSession：复用现有 `Session`，在 Redis/database store 下由同一 `LoginSubject` 实现跨实例共享。
- AsyncEventBus：`core.NewAsyncEventBus` 实现现有 `core.EventBus` 接口，可通过 `core.WithEventBus` 注入。
- RBAC helper：`rbac.Authorizer` 是可选包，实现 `core.Authorizer`。

access token、refresh token 和 server-side `TokenState` 的关系：

- access token 是客户端访问受保护接口时携带的凭证（`Kind=access` 的 `TokenState`）。
- refresh token 只用于换新 access token（`Kind=refresh` 的 `TokenState`），不应该用于普通接口鉴权。
- 统一 `TokenState` 是服务端是否承认该 token 的最终依据，通过 `Kind` 区分用途、`Status` 表达 active/revoked/consumed。

## RefreshToken

```go
store := memory.NewStore()
manager := core.NewManager(store)

result, err := manager.LoginWithRefresh(ctx, "1001", "access-token", "refresh-token")
if err != nil {
	panic(err)
}

refreshed, err := manager.Refresh(
	ctx,
	result.RefreshState.Token,
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
manager := core.NewManager(
	store,
	core.WithConfig(core.Config{
		TokenName:    "token",
		Timeout:      core.DefaultConfig().Timeout,
		Concurrent:   true,
		RequireNonce: true,
	}),
)

nonce, err := manager.GenerateNonce(ctx, core.WithNoncePurpose("login"))
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

统一模型下 access / refresh / nonce 均存于同一 `token_states` 表（通过 `kind` / `status` 列区分），`sessions` 表存 Session；对已有旧表，`Migrate` 会自动补充 `kind` / `status` 列。

## gRPC 拦截器

`plugins/grpc` 是独立 go module（避免给核心库引入强制的 gRPC 依赖），提供 server 端校验与 client 端注入：

```go
import btgrpc "github.com/apus-run/better-token/plugins/grpc"

// server：校验 metadata 中的 token（默认键 "authorization"），要求 Kind==access
srv := grpc.NewServer(grpc.UnaryInterceptor(btgrpc.UnaryServerInterceptor(manager)))

// client：把 token 注入 outgoing metadata，默认从 core.TokenFromContext 取
conn, _ := grpc.NewClient(target,
	grpc.WithUnaryInterceptor(btgrpc.UnaryClientInterceptor(
		btgrpc.WithTokenSource(func(ctx context.Context) (core.TokenValue, bool) {
			return "access-token", true
		}),
	)),
)
```

认证通过后，handler 内可用 `core.RequireSubject` / `core.RequireLoginID` 读取主体；失败返回 `codes.Unauthenticated`。

## 审计事件

`audit` 包把 `core.Event` 映射为结构化 `AuditEvent`（独立的 `AuditEventType`），通过实现 `core.Listener` 注册到事件总线，默认 slog 输出：

```go
import "github.com/apus-run/better-token/audit"

bus := core.NewEventBus() // 或 core.NewAsyncEventBus()，监听器异常不影响主流程
bus.Register(audit.New(audit.NewSlogSink(nil)))
manager := core.NewManager(store, core.WithEventBus(bus))
```

登录、登出、刷新、踢下线、nonce 消费、上线/下线事件都会被审计监听器捕获。可注入自定义 `audit.Sink` 做脱敏或对接外部审计系统。

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
- `examples/audit`
- `plugins/grpc`（拦截器用法见该模块的 `interceptor_test.go`）

## 暂不做

- OAuth2
- SSO
- RBAC 数据库表
- 纯无状态 JWT 模式
