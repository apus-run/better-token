# better-token 第一版最终架构 Issues

> Source SPEC: `docs/better-token-final-spec.md`
> Creation mode: Local
> Generated from: `.autoresearch/issues`

## Issue #1: 建立 core 基础模型、配置与错误

### Description

从全新架构出发，新增 `core` 包的基础领域模型、配置、运行时和错误定义。该 Issue 不考虑旧根包实现或迁移兼容，只为后续 `core.Manager`、`core.Store`、插件适配提供稳定契约。

Source: `docs/better-token-final-spec.md`

### Acceptance Criteria

- [ ] 新增 `core.TokenValue`
- [ ] 新增 `core.TokenState`
- [ ] 新增 `core.Session`
- [ ] 新增 `core.Config`
- [ ] 新增 `core.Runtime` 和 `NowFunc`
- [ ] 定义 `ErrEmptyLoginID`
- [ ] 定义 `ErrEmptyToken`
- [ ] 定义 `ErrTokenNotFound`
- [ ] 不定义 `ErrTokenExpired`，过期 token 统一返回 `ErrTokenNotFound`
- [ ] 定义 `ErrNotLogin`
- [ ] 定义 `ErrAuthorityDenied`
- [ ] `DefaultConfig` 默认值符合 SPEC
- [ ] `Runtime.Now` 支持 nil fallback 与 UTC 语义

### Dependencies

None

### Type

backend

### Priority

high

### SPEC Reference

Sections 3.2, 4.2, 6.1

---

## Issue #2: 建立 core 端口与上下文契约

### Description

定义全新 `core` 架构中的存储、授权、事件和认证上下文契约。该 Issue 只建立接口、基础实现和 helper，不实现 Manager 状态机。

Source: `docs/better-token-final-spec.md`

### Acceptance Criteria

- [ ] 定义 `core.Store` 领域存储接口
- [ ] 定义 `core.Authorizer`
- [ ] 定义 `core.Authority`
- [ ] 实现 `core.Permission(value string) Authority`
- [ ] 实现 `core.Role(value string) Authority`
- [ ] 提供 `NoopAuthorizer`
- [ ] 提供内存 Authorizer
- [ ] 定义 `core.Event`
- [ ] 定义 `core.EventBus`
- [ ] 定义 `core.Listener` 和 `ListenerFunc`
- [ ] 提供同步 EventBus
- [ ] 提供 Noop EventBus
- [ ] 定义 `core.AuthContext`
- [ ] 实现 `WithAuth`
- [ ] 实现 `AuthFromContext`
- [ ] 实现 `RequireAuth`
- [ ] 实现 `LoginIDFromContext`
- [ ] 实现 `RequireLoginID`
- [ ] 实现 `TokenFromContext`
- [ ] 实现 `IsAuthenticated`

### Dependencies

Issue #1

### Type

backend

### Priority

high

### SPEC Reference

Sections 2.2, 4.3, 5.1, 7.1

---

## Issue #3: 实现 storage/memory 的 core.Store

### Description

为第一版全新架构实现进程内 `core.Store`。该 Store 负责保存服务端承认的 `TokenState`、维护 `loginID + loginType` token 索引，并保存用户级 `Session`。

Source: `docs/better-token-final-spec.md`

### Acceptance Criteria

- [ ] 实现 `SaveTokenState`
- [ ] 实现 `GetTokenState`
- [ ] 实现 `DeleteTokenState`
- [ ] 实现 `ListTokenStates`
- [ ] 实现 `DeleteTokenStates`
- [ ] 实现 `SaveSession`
- [ ] 实现 `GetSession`
- [ ] 实现 `DeleteSession`
- [ ] 维护 `loginID + loginType -> token set` 索引
- [ ] `GetTokenState` 不返回过期数据
- [ ] `ListTokenStates` 过滤过期数据
- [ ] 删除 token 时同步维护索引
- [ ] 返回对象具备 copy 语义
- [ ] 并发读写无 data race

### Dependencies

Issue #2

### Type

backend

### Priority

high

### SPEC Reference

Sections 3.1, 4.3, 8.2

---

## Issue #4: 实现 core.Manager 登录态状态机

### Description

实现全新 `core.Manager` 的登录态生命周期。Manager 不生成 token、不解析 JWT，只保存和校验服务端登录态。

Source: `docs/better-token-final-spec.md`

### Acceptance Criteria

- [ ] 实现 `NewManager(store Store, opts ...Option) *Manager`
- [ ] 实现 `Login(ctx, loginID, token, opts...)`
- [ ] `Login` 接收外部 token，不生成 token
- [ ] `Login` 保存 `TokenState`
- [ ] `Login` 支持 `Concurrent=false`
- [ ] `Login` 支持 `ShareToken=true`
- [ ] 实现 `GetTokenState`
- [ ] `GetTokenState` 能识别不存在 token
- [ ] `GetTokenState` 能识别过期 token 并删除状态
- [ ] 实现 `IsValid`
- [ ] 实现 `Renew`
- [ ] 实现 `Logout`
- [ ] 实现 `LogoutByLoginID`
- [ ] `AutoRenew` 更新 `LastActiveAt`
- [ ] `AutoRenew` 在 `ActiveTimeout > 0` 时更新 `ExpiresAt`
- [ ] 状态变更成功后发布对应事件
- [ ] EventBus 发布不发生在 Manager 锁内

### Dependencies

Issue #1, Issue #2, Issue #3

### Type

backend

### Priority

high

### SPEC Reference

Sections 4.1, 5.1, 5.3, 6.3

---

## Issue #5: 实现 Session 与授权门面

### Description

在 `core.Manager` 上补齐 Session KV 与统一授权检查 API。Session 是用户级 KV 容器，授权统一通过 `Authorizer` 判断。

Source: `docs/better-token-final-spec.md`

### Acceptance Criteria

- [ ] 实现 `GetSession(ctx, loginID)`
- [ ] 实现 `SaveSession(ctx, session)`
- [ ] 实现 `DeleteSession(ctx, loginID)`
- [ ] Session 与 TokenState 生命周期分离
- [ ] 删除单个 token 不默认删除 Session
- [ ] 实现 `CheckAuthority`
- [ ] 实现 `CheckPermission`
- [ ] 实现 `CheckRole`
- [ ] 实现 `CheckAll`
- [ ] 实现 `CheckAny`
- [ ] 授权失败返回 `AuthorityDeniedError`
- [ ] `NoopAuthorizer` 默认拒绝授权检查

### Dependencies

Issue #4

### Type

backend

### Priority

high

### SPEC Reference

Sections 4.1, 5.1, 5.2, 6.1

---

## Issue #6: 实现独立 token 包

### Description

实现独立 `token` 包，专注 token 生成、JWT 签发、解析与校验。`token` 包不依赖 `core`，`core` 也不关心 token 是 JWT、UUID、Hash 还是其他字符串形式。

Source: `docs/better-token-final-spec.md`

### Acceptance Criteria

- [ ] 实现 `Claims[T]`
- [ ] 实现 `JwtConfig`
- [ ] 实现 `JwtManager[T]`
- [ ] `JwtManager[T]` 支持签发 JWT
- [ ] `JwtManager[T]` 支持解析 JWT
- [ ] `JwtManager[T]` 支持校验 JWT
- [ ] 实现 `TokenConfig`
- [ ] 实现 `TokenGenerator[T]`
- [ ] 支持 simple token style
- [ ] 支持 timestamp token style
- [ ] 支持 uuid token style
- [ ] 支持 hash token style
- [ ] 支持 jwt token style
- [ ] 支持 tiktok token style
- [ ] `token` 包不依赖 `core`
- [ ] JWT 签发、解析、校验可独立测试

### Dependencies

None

### Type

backend

### Priority

high

### SPEC Reference

Sections 2.4, 4.6, 9.4

---

## Issue #7: 实现 plugins/http 中间件

### Description

实现标准库 `net/http` 适配层。插件从请求中提取 token，调用 `core.Manager.GetTokenState`，并将 `core.AuthContext` 注入 `request.Context()`。

Source: `docs/better-token-final-spec.md`

### Acceptance Criteria

- [ ] 新增 `plugins/http`
- [ ] 实现 `Middleware(manager *core.Manager, opts ...Option) func(http.Handler) http.Handler`
- [ ] 支持从 `Header[TokenName]` 提取 token
- [ ] 支持从 `Authorization` 提取 token
- [ ] 支持从 Cookie 提取 token
- [ ] 支持从 Query 提取 token
- [ ] token 提取顺序符合 SPEC
- [ ] 支持 `TokenPrefix = "Bearer"`
- [ ] 前缀解析会 trim 空白
- [ ] 未认证返回 401
- [ ] 未认证不调用下游 handler
- [ ] 已认证后可通过 `core.RequireLoginID(r.Context())` 读取身份

### Dependencies

Issue #4, Issue #5

### Type

backend

### Priority

medium

### SPEC Reference

Sections 4.4, 7.2, 9.2

---

## Issue #8: 实现 plugins/gin 中间件

### Description

实现 gin 适配层。插件提取 token 并校验登录态，认证成功后同时写入 `c.Request.Context()` 与 gin context。

Source: `docs/better-token-final-spec.md`

### Acceptance Criteria

- [ ] 新增 `plugins/gin`
- [ ] 实现 `Middleware(manager *core.Manager, opts ...Option) gin.HandlerFunc`
- [ ] token 提取规则与 `plugins/http` 保持一致
- [ ] 支持 `TokenPrefix = "Bearer"`
- [ ] 未认证时 `AbortWithStatus(401)`
- [ ] 未认证时不执行后续 handler
- [ ] 已认证后 `c.Request.Context()` 可读取 `core.AuthContext`
- [ ] 已认证后 `c.Get("auth")` 可读取同一个 `*core.AuthContext`

### Dependencies

Issue #4, Issue #5

### Type

backend

### Priority

medium

### SPEC Reference

Sections 4.5, 7.2, 9.2

---

## Issue #9: 补齐核心测试与 Store contract tests

### Description

为全新架构补齐核心单元测试、边界测试与 `core.Store` contract tests，保证模型、状态机、授权、事件和 memory store 行为符合 SPEC。

Source: `docs/better-token-final-spec.md`

### Acceptance Criteria

- [ ] 覆盖 `DefaultConfig` 默认值
- [ ] 覆盖 `Runtime.Now` nil fallback 与 UTC 语义
- [ ] 覆盖 `Login`
- [ ] 覆盖 `GetTokenState`
- [ ] 覆盖 `Renew`
- [ ] 覆盖 `Logout`
- [ ] 覆盖 `LogoutByLoginID`
- [ ] 覆盖 `Concurrent=false`
- [ ] 覆盖 `ShareToken=true`
- [ ] 覆盖 `AutoRenew`
- [ ] 覆盖 Session 生命周期
- [ ] 覆盖 Authorizer 检查
- [ ] 覆盖 EventBus 监听器错误不破坏主流程
- [ ] 覆盖 memory Store 索引一致性
- [ ] 覆盖过期过滤
- [ ] 覆盖 copy 语义
- [ ] 覆盖并发安全
- [ ] `go test ./...` 通过

### Dependencies

Issue #3, Issue #4, Issue #5, Issue #6

### Type

backend

### Priority

high

### SPEC Reference

Sections 9.1, 9.3, 9.4

---

## Issue #10: 补齐插件集成测试与文档示例

### Description

验证全新架构的端到端使用路径，并补齐 README 示例。示例应体现 token 包独立生成 token，core 包只负责承认服务端登录态，plugins 包负责 Web 接入。

Source: `docs/better-token-final-spec.md`

### Acceptance Criteria

- [ ] `token.TokenGenerator` 生成 token 后可登录 core
- [ ] 通过 HTTP middleware 可认证已登录 token
- [ ] `token.JwtManager` 生成 JWT 后可作为 `TokenValue` 保存并认证
- [ ] Gin middleware 同时验证 request context 与 `c.Get("auth")`
- [ ] README 展示全新架构用法
- [ ] README 示例包含外部生成 token 后调用 `core.Manager.Login`
- [ ] README 明确第一版不做 RefreshToken
- [ ] README 明确第一版不做 Nonce
- [ ] README 明确第一版不做 OAuth2
- [ ] README 明确第一版不做 SSO
- [ ] README 明确第一版不做纯无状态 JWT 模式
- [ ] `go test ./...` 通过
- [ ] `go test -race ./...` 通过，或记录不能运行的原因

### Dependencies

Issue #7, Issue #8, Issue #9

### Type

backend

### Priority

medium

### SPEC Reference

Sections 9.2, 10.1, 10.3
