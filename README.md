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

- [`examples/admin`](examples/admin)：Gin + HTMX + SQLite 后台，完整串联 nonce、access/refresh、Gin middleware、RBAC、Session、在线设备、踢下线和异步审计；token store 可切换 memory/SQLite。
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

## Session 数据操作

Session 保持显式保存：`Set`、`SetMulti`、`Remove`、`Clear` 修改快照，调用 `Manager.SaveSession` 持久化并处理错误。新增 `GetString`、`GetBool`、`GetInt`、`GetInt64`、`Keys`、`Size`、`IsEmpty`。

```go
session.SetMulti(map[string]any{"theme": "dark", "visits": int64(42)})
if err := manager.SaveSession(ctx, session); err != nil {
    return err
}
visits := session.GetInt64("visits")
_ = visits
```

数据方法与序列化可并发使用。不要复制已使用的 Session 值，也不要并发写公开字段或修改 `Get` 返回的可变值；用 `Clone` 获取独立容器快照。Clone 递归复制 map、slice、array，保留类型和容器循环引用；自定义 struct/pointer 仍由调用方管理。

从 JSON 恢复的数字使用 `json.Number` 保留精度，类型化 getter 可跨 memory、Redis、database 使用。直接对 `Get` 的结果断言 `float64` 的调用方应改用 getter，或显式处理 `json.Number`。整数 getter 支持精确的十进制和科学计数表示（如 `1000.0`、`1e3`），对非整数、越界与非数值返回 0。

## Token Cookie

Cookie 的读取、写入和清理复用 `plugins/http.CookieConfig`；Gin 提供同名类型与默认配置。默认 Path=/、HttpOnly=true、SameSite=Lax。使用 HTTPS 时配置 Secure=true。

```go
cookies := btHTTP.DefaultCookieConfig()
cookies.Name = "access"
cookies.Secure = true

handler := btHTTP.Middleware(manager, btHTTP.WithCookieConfig(cookies))(next)
_ = handler

// Login / Refresh 成功后，将调用方持有的明文 token 写入 Cookie。
if err := cookies.SetTokenCookie(w, state.Token, manager.Config().Timeout); err != nil {
    return err
}

// Logout 成功后，以相同 Name / Path / Domain 清理浏览器凭证。
if err := cookies.ClearTokenCookie(w); err != nil {
    return err
}
```

Cookie 写入原始 token，不拼 Header 前缀。正 TTL 设置浏览器期限，TTL≤0 创建浏览器会话 Cookie；服务端有效性仍由 Manager 判断。`WithCookieRead(false)` 可关闭 Cookie 来源，不影响 Header；Header 仍优先于 Cookie，URL query 仍须显式启用。

显式 `WithCookieConfig` 的空 Name 在读写两侧均使用默认名 `token`；未指定 CookieConfig 时，读取仍沿用 TokenName。Path 必须为绝对路径。写入前验证 Cookie 内容和配置；SameSite=None 必须同时启用 Secure，`__Secure-` / `__Host-` 名称必须满足其属性约束。后台示例的登录、刷新、注销和撤销 refresh 已使用这些工具。

不轮换 refresh 时，同一 refresh token 的并发刷新也会先原子占用，未取得执行权的请求不修改 access。完成后重新激活该 refresh；失败时使用条件更新释放占用，不覆盖并发撤销。若进程中断或存储故障导致无法释放，占用保持不可用，需要重新登录。

Session 序列化会拒绝可见对象图中的循环引用并返回 JSON 错误，包括 Session 自引用和相互引用；自定义 JSON/Text marshaler 仍负责自身的编码逻辑。
