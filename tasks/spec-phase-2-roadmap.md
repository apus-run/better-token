# SPEC: better-token 第二阶段功能路线图

> Technical specification derived from: `tasks/prd-phase-2-roadmap.md`
> Generated: 2026-06-20 | Target branch: `main` | Commit: `9e2246d`

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 将第二阶段 PRD 转换为可实施技术规格。第二阶段在保持第一版 public API 兼容的前提下，增量引入 RefreshToken、Nonce、OnlineManager、DistributedSession 语义、AsyncEventBus 与 RBAC helper。设计重点是：不破坏 `core.Store` 窄接口，不让 `core.Manager` 重新承担 token/JWT 生成职责，并让新增能力能按模块分阶段落地。

### 1.2 PRD Reference

- Source: `tasks/prd-phase-2-roadmap.md`
- User Stories covered:
  - US-001: RefreshToken 数据模型与端口
  - US-002: 签发 RefreshToken
  - US-003: 使用 RefreshToken 换新 Access Token
  - US-004: RefreshToken 撤销
  - US-005: Nonce 生成与消费
  - US-006: 登录流程接入 Nonce 校验
  - US-007: 在线 Token 查询
  - US-008: 按设备踢下线
  - US-009: DistributedSession 语义
  - US-010: 异步事件总线
  - US-011: RBAC 辅助模块
  - US-012: 第二阶段文档与示例
- Functional Requirements covered:
  - FR-1 through FR-24 from `tasks/prd-phase-2-roadmap.md`

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
| --- | --- | --- |
| API compatibility | 保持现有 `core.Manager`、`core.Store` 方法签名不变 | 第二阶段是增量增强，不能破坏第一版接入代码 |
| RefreshToken placement | 在 `core` 中新增 `RefreshManager` 与 `RefreshStore` capability | RefreshToken 属于服务端登录态扩展，但不应塞进 `TokenState` 或强改 `Store` |
| Token generation | RefreshManager 接收调用方传入的新 access/refresh token 字符串 | 延续第一版边界：`core` 不 import `token`，不生成 JWT 或随机 token |
| Refresh rotation | 支持轮换，默认启用；可通过配置关闭 | 更适合生产安全默认值；RefreshToken 功能本身默认不启用，故不破坏兼容 |
| Refresh binding | RefreshToken 绑定 `loginID + loginType + device`，并可记录当前 access token | 支持按设备撤销和刷新后替换 access token |
| Store extension | 新增 `RefreshStore`、`NonceStore` capability 接口，具体 store 可组合实现 | 避免修改 `core.Store`，通过可选能力扩展存储 |
| Nonce scope | 提供通用 NonceManager；登录流程通过 optional `NonceConsumer` 接入 | 既满足登录防重放，也能用于业务敏感操作 |
| OnlineManager | 在 `core.Manager` 上增量新增在线查询和按设备踢下线方法 | 现有 `FindTokenStates` 已具备基础能力，无需新包绕远路 |
| DistributedSession | 不新增独立 public type；强化现有 `Session` 的跨实例 contract 与测试 | 现有 Session 已按 `LoginSubject` 存储，Redis/database 天然可共享 |
| AsyncEventBus | 新增 `core.AsyncEventBus`，实现现有 `EventBus` 接口并暴露额外 `Close/Flush` | 不修改 `EventBus` 接口，保持 `WithEventBus` 兼容 |
| RBAC helper | 新增顶层 `rbac` 包，实现 `core.Authorizer` | 可选依赖业务，避免把 RBAC 数据库模型塞进 core |
| Documentation | README 增加索引，examples 提供 refresh/nonce/online 示例 | 成功标准明确要求 10 分钟内完成最小接入 |

---

## 2. Architecture

### 2.1 System Context

第二阶段沿用第一版架构边界：

```text
Application
  |
  | generates access/refresh token strings using token package or custom code
  v
core.Manager
  |-- Login / GetTokenState / Logout / Session / Authorization
  |-- ListTokenStates / LogoutByDevice                 [NEW]
  |
  | optional composition
  |-- core.RefreshManager -> core.RefreshStore          [NEW]
  |-- core.NonceManager   -> core.NonceStore            [NEW]
  |-- core.AsyncEventBus implements core.EventBus       [NEW]
  |
  v
core.Store + optional capabilities
  |
  | storage/memory, storage/redis, storage/database
  v
Persistent medium
```

Package boundaries:

- `core` may define domain models, optional capability interfaces, managers, errors, and event bus implementations.
- `core` must not import `token`, `storage/*`, `plugins/*`, `rbac`, or framework packages.
- `token` remains independent and may be used by application code to generate access/refresh token strings.
- `storage/*` may implement `core.Store`, `core.RefreshStore`, and `core.NonceStore`.
- `rbac` imports `core` and implements `core.Authorizer`.
- `plugins/http` and `plugins/gin` do not need refresh or nonce awareness for phase 2, because they authenticate access tokens through `core.Manager.GetTokenState`.

### 2.2 Component Design

#### core.RefreshManager

Responsibilities:

- Save refresh token state after access token login succeeds.
- Exchange valid refresh token for a new access token.
- Revoke one refresh token.
- Revoke all refresh tokens for a login subject.
- Coordinate old access token deletion during refresh when configured.
- Publish refresh/revoke events through the existing `EventBus`.

Non-responsibilities:

- Generate access token strings.
- Generate refresh token strings.
- Parse or verify JWT signatures.
- Expose HTTP endpoints.

#### core.RefreshStore

Capability interface implemented by stores that support refresh tokens:

```go
type RefreshStore interface {
    SaveRefreshTokenState(ctx context.Context, state *RefreshTokenState, ttl time.Duration) error
    GetRefreshTokenState(ctx context.Context, token TokenValue) (*RefreshTokenState, bool, error)
    DeleteRefreshTokenState(ctx context.Context, token TokenValue) error
    FindRefreshTokenStates(ctx context.Context, subject LoginSubject) ([]*RefreshTokenState, error)
    DeleteRefreshTokenStates(ctx context.Context, subject LoginSubject) error
}

type StoreWithRefresh interface {
    Store
    RefreshStore
}
```

`core.Store` remains unchanged.

#### core.NonceManager

Responsibilities:

- Generate nonce values.
- Save nonce values with TTL.
- Atomically consume nonce values.
- Return typed errors for missing, expired, or replayed nonce.

NonceManager should support general use and login integration. Login integration is done by adding non-breaking optional fields/options to `core.Manager` and `LoginOption`.

#### core.NonceStore

Capability interface implemented by stores that support nonce:

```go
type NonceStore interface {
    SaveNonceState(ctx context.Context, state *NonceState, ttl time.Duration) error
    ConsumeNonceState(ctx context.Context, nonce TokenValue) (*NonceState, bool, error)
}

type StoreWithNonce interface {
    Store
    NonceStore
}
```

`ConsumeNonceState` must be atomic for Redis and database stores.

#### core.OnlineManager Capability

Online management is implemented as new methods on `core.Manager`:

```go
func (m *Manager) ListTokenStates(ctx context.Context, loginID string, opts ...ListTokenOption) ([]*TokenState, error)
func (m *Manager) LogoutByDevice(ctx context.Context, loginID string, device string, opts ...LogoutOption) error
```

These methods reuse `Store.FindTokenStates` and `Store.DeleteTokenState`. They do not require a new store interface.

#### core.AsyncEventBus

`AsyncEventBus` implements the existing `EventBus` interface:

```go
type AsyncEventBus struct { ... }

func NewAsyncEventBus(opts ...AsyncEventBusOption) *AsyncEventBus
func (b *AsyncEventBus) Register(listener Listener)
func (b *AsyncEventBus) Publish(ctx context.Context, event Event)
func (b *AsyncEventBus) Clear()
func (b *AsyncEventBus) ListenerCount() int
func (b *AsyncEventBus) Flush(ctx context.Context) error
func (b *AsyncEventBus) Close(ctx context.Context) error
```

`Flush` and `Close` are extra concrete methods, not added to `EventBus`, so `WithEventBus` remains compatible.

#### rbac.Authorizer

The new `rbac` package provides an optional Authorizer:

```go
package rbac

type Authorizer struct { ... }

func NewAuthorizer(opts ...Option) *Authorizer
func (a *Authorizer) AssignRole(loginID, role string)
func (a *Authorizer) RevokeRole(loginID, role string)
func (a *Authorizer) GrantPermission(role, permission string)
func (a *Authorizer) RevokePermission(role, permission string)
func (a *Authorizer) SetDirectPermissions(loginID string, permissions []string)
```

It implements:

```go
var _ core.Authorizer = (*Authorizer)(nil)
```

### 2.3 Module Interactions

#### Login with RefreshToken

```text
Application
  -> generate access token using token.TokenGenerator or custom issuer
  -> generate refresh token using token.TokenGenerator or custom issuer
  -> RefreshManager.Login(ctx, loginID, accessToken, refreshToken, login options...)
      -> core.Manager.Login(ctx, loginID, accessToken, login options...)
      -> RefreshStore.SaveRefreshTokenState(refreshToken state)
      -> EventBus.Publish(EventRefreshIssued)
  -> returns LoginResult{TokenState, RefreshTokenState}
```

If saving refresh state fails after access login succeeds, RefreshManager must roll back the newly created access token by calling `Manager.Logout(ctx, accessToken)`.

#### Refresh Access Token

```text
Application
  -> generate next access token
  -> generate next refresh token if RotateRefreshToken is true
  -> RefreshManager.Refresh(ctx, refreshToken, nextAccessToken, opts...)
      -> RefreshStore.GetRefreshTokenState(refreshToken)
      -> validate not expired and not revoked
      -> if RevokeAccessTokenOnRefresh: Manager.Logout(old access token)
      -> Manager.Login(ctx, loginID, nextAccessToken, login options...)
      -> if RotateRefreshToken:
           delete old refresh token
           save next refresh token state
         else:
           touch existing refresh token state
      -> EventBus.Publish(EventRefresh)
  -> returns LoginResult{TokenState, RefreshTokenState}
```

#### Nonce Generate and Consume

```text
NonceManager.Generate(ctx, subject, metadata)
  -> generate random nonce
  -> NonceStore.SaveNonceState(nonce state, ttl)
  -> return nonce

NonceManager.Consume(ctx, nonce)
  -> NonceStore.ConsumeNonceState(nonce)
  -> validate found and not expired
  -> return nonce state
```

#### Login with Required Nonce

```text
Manager.Login(ctx, loginID, token, WithNonce(nonce))
  -> if Config.RequireNonce:
       validate nonce is present
       call configured NonceConsumer.ConsumeNonce(ctx, nonce)
  -> existing Login behavior
```

If `RequireNonce` is true but no `NonceConsumer` is configured, Manager must fail fast with `ErrNonceConsumerNotConfigured`.

#### Online Token Query and Logout by Device

```text
Manager.ListTokenStates(ctx, loginID, WithListLoginType(loginType))
  -> Store.FindTokenStates(subject)
  -> filter expired states via store contract
  -> return clones

Manager.LogoutByDevice(ctx, loginID, device, WithLogoutLoginType(loginType))
  -> Store.FindTokenStates(subject)
  -> for states where state.Device == device:
       Store.DeleteTokenState(state.Token)
  -> EventBus.Publish(EventKickOut)
```

### 2.4 File Structure

```text
better-token/
  core/
    refresh.go                 [NEW: RefreshManager, RefreshConfig, LoginResult]
    refresh_state.go           [NEW: RefreshTokenState]
    refresh_store.go           [NEW: RefreshStore capability]
    nonce.go                   [NEW: NonceManager, NonceConfig, NonceConsumer]
    nonce_state.go             [NEW: NonceState]
    nonce_store.go             [NEW: NonceStore capability]
    online.go                  [NEW: ListTokenStates, LogoutByDevice options/methods]
    async_event.go             [NEW: AsyncEventBus]
    errors.go                  [MODIFY: add refresh/nonce errors]
    event.go                   [MODIFY: add refresh/nonce event types]
    config.go                  [MODIFY: add optional nonce config only]
    option.go                  [MODIFY: add WithNonceConsumer, WithNonce, list options]
    refresh_test.go            [NEW]
    nonce_test.go              [NEW]
    online_test.go             [NEW]
    async_event_test.go        [NEW]

  rbac/
    authorizer.go              [NEW]
    authorizer_test.go         [NEW]

  storage/
    memory/
      store.go                 [MODIFY: implement RefreshStore and NonceStore]
      store_test.go            [MODIFY: add refresh/nonce contract coverage]
    redis/
      store.go                 [MODIFY: implement RefreshStore and NonceStore]
      store_test.go            [MODIFY: add miniredis refresh/nonce coverage]
    database/
      store.go                 [MODIFY: add refresh/nonce records and migration]
      store_test.go            [MODIFY: add sqlite refresh/nonce coverage]

  examples/
    refresh-token/
      main.go                  [NEW]
    nonce/
      main.go                  [NEW]
    online-manager/
      main.go                  [NEW]

  README.md                    [MODIFY: second-stage feature index and examples]
  tasks/
    spec-phase-2-roadmap.md    [NEW]
```

---

## 3. Data Model

### 3.1 Schema Changes

#### Memory Store

Extend `storage/memory.Store` with refresh and nonce maps:

```go
type Store struct {
    mu       sync.RWMutex
    tokens   map[core.TokenValue]*tokenItem
    indexes  map[core.LoginSubject]map[core.TokenValue]struct{}
    sessions map[core.LoginSubject]*sessionItem

    refreshTokens  map[core.TokenValue]*refreshTokenItem
    refreshIndexes map[core.LoginSubject]map[core.TokenValue]struct{}
    nonces         map[core.TokenValue]*nonceItem

    now core.NowFunc
}
```

#### Redis Store

Default prefix remains `bt`.

```text
bt:token:{access_token}                         -> TokenState JSON, TTL
bt:index:{login_type}:{login_id}                -> Set(access_token), GC TTL
bt:session:{login_type}:{login_id}              -> Session JSON, TTL

bt:refresh:{refresh_token}                      -> RefreshTokenState JSON, TTL
bt:refresh-index:{login_type}:{login_id}        -> Set(refresh_token), GC TTL
bt:nonce:{nonce}                                -> NonceState JSON, TTL
```

Redis nonce consume must be implemented atomically with Lua or an equivalent single-command strategy that returns and deletes only if the key exists. If the Redis command set supports `GETDEL`, `GETDEL` is acceptable, but Lua is preferred for portability and explicit expiry validation.

#### Database Store

`Migrate(ctx)` must include the new records:

```go
type refreshTokenRecord struct {
    Token        string     `gorm:"column:token;primaryKey;size:255"`
    LoginID      string     `gorm:"column:login_id;size:255;index:idx_refresh_login,priority:1"`
    LoginType    string     `gorm:"column:login_type;size:64;index:idx_refresh_login,priority:2"`
    AccessToken  string     `gorm:"column:access_token;size:255;index"`
    Device       string     `gorm:"column:device;size:255;index"`
    StateJSON    []byte     `gorm:"column:state_json;type:text;not null"`
    ExpiresAt    *time.Time `gorm:"column:expires_at;index"`
    RevokedAt    *time.Time `gorm:"column:revoked_at;index"`
    LastUsedAt   *time.Time `gorm:"column:last_used_at"`
    CreatedAt    time.Time  `gorm:"column:created_at"`
}

func (refreshTokenRecord) TableName() string { return "refresh_token_states" }

type nonceRecord struct {
    Nonce      string     `gorm:"column:nonce;primaryKey;size:255"`
    StateJSON  []byte     `gorm:"column:state_json;type:text;not null"`
    ExpiresAt  *time.Time `gorm:"column:expires_at;index"`
    ConsumedAt *time.Time `gorm:"column:consumed_at;index"`
    CreatedAt  time.Time  `gorm:"column:created_at"`
}

func (nonceRecord) TableName() string { return "nonce_states" }
```

Database nonce consume must run in a transaction:

```text
1. SELECT nonce row FOR UPDATE when supported by dialect.
2. If not found, return not found.
3. If consumed_at is not null, return replayed.
4. If expires_at <= now, delete or mark expired and return expired.
5. UPDATE consumed_at = now.
6. Return decoded NonceState.
```

SQLite tests may emulate row lock by transaction order; Redis and production SQL stores must document their atomicity guarantees.

### 3.2 Entity Definitions

#### RefreshTokenState

```go
type RefreshTokenState struct {
    Token        TokenValue      `json:"token"`
    AccessToken  TokenValue      `json:"access_token,omitempty"`
    LoginID      string          `json:"login_id"`
    LoginType    string          `json:"login_type"`
    Device       string          `json:"device,omitempty"`
    CreatedAt    time.Time       `json:"created_at"`
    LastUsedAt   *time.Time      `json:"last_used_at,omitempty"`
    ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
    RevokedAt    *time.Time      `json:"revoked_at,omitempty"`
    Metadata     json.RawMessage `json:"metadata,omitempty"`
}
```

Methods:

```go
func (s RefreshTokenState) Subject() LoginSubject
func (s RefreshTokenState) IsExpired(now time.Time) bool
func (s RefreshTokenState) IsRevoked() bool
func (s *RefreshTokenState) Touch(now time.Time)
func (s *RefreshTokenState) Revoke(now time.Time)
func (s *RefreshTokenState) Clone() *RefreshTokenState
```

#### NonceState

```go
type NonceState struct {
    Nonce      TokenValue      `json:"nonce"`
    LoginID    string          `json:"login_id,omitempty"`
    LoginType  string          `json:"login_type,omitempty"`
    Purpose    string          `json:"purpose,omitempty"`
    CreatedAt  time.Time       `json:"created_at"`
    ExpiresAt  *time.Time      `json:"expires_at,omitempty"`
    ConsumedAt *time.Time      `json:"consumed_at,omitempty"`
    Metadata   json.RawMessage `json:"metadata,omitempty"`
}
```

Methods:

```go
func (s NonceState) Subject() LoginSubject
func (s NonceState) IsExpired(now time.Time) bool
func (s NonceState) IsConsumed() bool
func (s *NonceState) Consume(now time.Time)
func (s *NonceState) Clone() *NonceState
```

#### LoginResult

```go
type LoginResult struct {
    TokenState        *TokenState        `json:"token_state"`
    RefreshTokenState *RefreshTokenState `json:"refresh_token_state,omitempty"`
}
```

### 3.3 Relationships

- `TokenState` is the server-side state for access tokens.
- `RefreshTokenState` is a separate server-side state for refresh tokens.
- `RefreshTokenState.AccessToken` points to the current access token produced by the refresh flow.
- `RefreshTokenState.Subject()` uses the same `LoginSubject` model as `TokenState` and `Session`.
- `Session` remains subject-scoped and is not deleted when a single access token is deleted.
- `NonceState` is not tied to `TokenState`; it may optionally carry `LoginSubject` and `Purpose`.

### 3.4 Migration Plan

1. Add new core types and capability interfaces without modifying existing `core.Store`.
2. Add memory store fields and tests.
3. Add Redis key helpers and tests using miniredis.
4. Add database records to `Migrate(ctx)` and tests using sqlite.
5. Add RefreshManager and NonceManager after store capabilities exist.
6. Add online methods and AsyncEventBus.
7. Add `rbac` helper package.
8. Update README and examples.

Rollback strategy:

- Removing second-stage code should not require changing first-stage token/session tables.
- Database rollback can drop `refresh_token_states` and `nonce_states` without affecting `token_states` and `sessions`.
- Redis rollback can delete `bt:refresh:*`, `bt:refresh-index:*`, and `bt:nonce:*`.

---

## 4. API Design

### 4.1 Public Go APIs

#### RefreshManager

```go
type RefreshConfig struct {
    Timeout                    time.Duration
    RotateRefreshToken         bool
    RevokeAccessTokenOnRefresh bool
    RevokeRefreshOnLogout      bool
}

func DefaultRefreshConfig() RefreshConfig

type RefreshManager struct { ... }

func NewRefreshManager(manager *Manager, store RefreshStore, opts ...RefreshOption) *RefreshManager

func (m *RefreshManager) Login(
    ctx context.Context,
    loginID string,
    accessToken TokenValue,
    refreshToken TokenValue,
    opts ...LoginOption,
) (*LoginResult, error)

func (m *RefreshManager) Refresh(
    ctx context.Context,
    refreshToken TokenValue,
    nextAccessToken TokenValue,
    opts ...RefreshFlowOption,
) (*LoginResult, error)

func (m *RefreshManager) Revoke(ctx context.Context, refreshToken TokenValue) error
func (m *RefreshManager) RevokeByLoginID(ctx context.Context, loginID string, opts ...LogoutOption) error
```

`RefreshFlowOption`:

```go
func WithNextRefreshToken(token TokenValue) RefreshFlowOption
func WithRefreshDevice(device string) RefreshFlowOption
func WithRefreshMetadata(metadata json.RawMessage) RefreshFlowOption
```

Rules:

- If `RotateRefreshToken` is true, `Refresh` requires `WithNextRefreshToken`.
- If `RotateRefreshToken` is false, `Refresh` keeps the old refresh token and updates `LastUsedAt`.
- If `RevokeAccessTokenOnRefresh` is true, the old access token is removed after the refresh token validates and before the new access token is saved.
- If new access token login fails, old refresh token must remain valid unless it was already invalid.

#### NonceManager

```go
type NonceConfig struct {
    Timeout time.Duration
    Length  int
}

func DefaultNonceConfig() NonceConfig

type NonceManager struct { ... }

func NewNonceManager(store NonceStore, opts ...NonceOption) *NonceManager
func (m *NonceManager) Generate(ctx context.Context, opts ...GenerateNonceOption) (TokenValue, error)
func (m *NonceManager) Consume(ctx context.Context, nonce TokenValue) (*NonceState, error)
```

`GenerateNonceOption`:

```go
func WithNonceSubject(subject LoginSubject) GenerateNonceOption
func WithNoncePurpose(purpose string) GenerateNonceOption
func WithNonceMetadata(metadata json.RawMessage) GenerateNonceOption
```

Login integration:

```go
type NonceConsumer interface {
    Consume(ctx context.Context, nonce TokenValue) (*NonceState, error)
}

func WithNonceConsumer(consumer NonceConsumer) Option
func WithNonce(nonce TokenValue) LoginOption
```

`core.Config` gains:

```go
RequireNonce bool
```

Default is `false`.

#### Online APIs

```go
type ListTokenOption = option.Option[listTokenOptions]

func WithListLoginType(loginType string) ListTokenOption

func (m *Manager) ListTokenStates(
    ctx context.Context,
    loginID string,
    opts ...ListTokenOption,
) ([]*TokenState, error)

func (m *Manager) LogoutByDevice(
    ctx context.Context,
    loginID string,
    device string,
    opts ...LogoutOption,
) error
```

#### AsyncEventBus

```go
type EventErrorHandler func(ctx context.Context, event Event, err error)

func NewAsyncEventBus(opts ...AsyncEventBusOption) *AsyncEventBus
func WithEventQueueSize(size int) AsyncEventBusOption
func WithEventWorkerCount(count int) AsyncEventBusOption
func WithEventErrorHandler(handler EventErrorHandler) AsyncEventBusOption
```

#### RBAC Helper

```go
package rbac

func NewAuthorizer(opts ...Option) *Authorizer

func (a *Authorizer) AssignRole(loginID, role string)
func (a *Authorizer) RevokeRole(loginID, role string)
func (a *Authorizer) GrantPermission(role, permission string)
func (a *Authorizer) RevokePermission(role, permission string)
func (a *Authorizer) SetDirectPermissions(loginID string, permissions []string)
func (a *Authorizer) HasAuthority(ctx context.Context, loginID string, authority core.Authority) (bool, error)
func (a *Authorizer) GetAuthorities(ctx context.Context, loginID string) ([]core.Authority, error)
```

### 4.2 Request/Response Schemas

This is a Go library and does not expose HTTP endpoints in phase 2. Application-level HTTP APIs are expected to wrap the public Go APIs.

Recommended HTTP response shapes for examples:

```json
{
  "access_token": "access-token-string",
  "refresh_token": "refresh-token-string",
  "expires_at": "2026-06-20T13:00:00Z"
}
```

Refresh error response in examples:

```json
{
  "error": "refresh_token_expired",
  "message": "refresh token expired"
}
```

### 4.3 Error Responses

Core errors:

```go
var (
    ErrEmptyRefreshToken          = errors.New("empty refresh token")
    ErrRefreshTokenNotFound       = errors.New("refresh token not found")
    ErrRefreshTokenExpired        = errors.New("refresh token expired")
    ErrRefreshTokenRevoked        = errors.New("refresh token revoked")
    ErrNextRefreshTokenRequired   = errors.New("next refresh token required")
    ErrEmptyNonce                 = errors.New("empty nonce")
    ErrNonceNotFound              = errors.New("nonce not found")
    ErrNonceExpired               = errors.New("nonce expired")
    ErrNonceReplayed              = errors.New("nonce replayed")
    ErrNonceConsumerNotConfigured = errors.New("nonce consumer not configured")
)
```

Recommended HTTP mapping for examples:

| Error | HTTP Status | Example Code |
| --- | --- | --- |
| `ErrEmptyRefreshToken` | 400 | `empty_refresh_token` |
| `ErrRefreshTokenNotFound` | 401 | `refresh_token_not_found` |
| `ErrRefreshTokenExpired` | 401 | `refresh_token_expired` |
| `ErrRefreshTokenRevoked` | 401 | `refresh_token_revoked` |
| `ErrNextRefreshTokenRequired` | 500 or 400 | `next_refresh_token_required` |
| `ErrEmptyNonce` | 400 | `empty_nonce` |
| `ErrNonceNotFound` | 400 | `nonce_not_found` |
| `ErrNonceExpired` | 400 | `nonce_expired` |
| `ErrNonceReplayed` | 409 | `nonce_replayed` |
| `ErrNonceConsumerNotConfigured` | 500 | `nonce_consumer_not_configured` |

### 4.4 Breaking Changes

No breaking changes are allowed in phase 2.

Allowed changes:

- Add fields to `core.Config`.
- Add fields to private option structs.
- Add new public types, constructors, options, methods, and packages.
- Make existing stores implement additional interfaces.
- Add new database tables in `storage/database.Migrate`.

Disallowed changes:

- Change existing method signatures.
- Rename existing packages, types, methods, constants, or errors.
- Make current `core.Store` require refresh/nonce methods.
- Make default `Manager.Login` require nonce.
- Make default `Manager.Login` issue refresh tokens.

---

## 5. Business Logic

### 5.1 Core Algorithms

#### RefreshManager.Login

```text
Input: loginID, accessToken, refreshToken, login options

1. Normalize context, loginID, accessToken, refreshToken.
2. Return ErrEmptyLoginID, ErrEmptyToken, or ErrEmptyRefreshToken for invalid input.
3. Call Manager.Login(ctx, loginID, accessToken, opts...).
4. Build RefreshTokenState from returned TokenState:
   - Token = refreshToken
   - AccessToken = accessToken
   - LoginID/LoginType/Device copied from TokenState
   - CreatedAt = Runtime.Now
   - ExpiresAt = now + RefreshConfig.Timeout when Timeout > 0
5. Save RefreshTokenState with RefreshStore.
6. If save fails, call Manager.Logout(ctx, accessToken) and return the save error.
7. Publish EventRefreshIssued.
8. Return LoginResult.
```

#### RefreshManager.Refresh

```text
Input: old refreshToken, nextAccessToken, options

1. Normalize input.
2. Return ErrEmptyRefreshToken or ErrEmptyToken for invalid input.
3. Load RefreshTokenState from RefreshStore.
4. If not found, return ErrRefreshTokenNotFound.
5. If revoked, return ErrRefreshTokenRevoked.
6. If expired, delete refresh token and return ErrRefreshTokenExpired.
7. If RotateRefreshToken is true and no next refresh token is provided, return ErrNextRefreshTokenRequired.
8. If RevokeAccessTokenOnRefresh and state.AccessToken is not empty, call Manager.Logout(ctx, state.AccessToken).
9. Call Manager.Login(ctx, state.LoginID, nextAccessToken, login options derived from refresh state).
10. If RotateRefreshToken is true:
    - Delete old refresh token.
    - Save new RefreshTokenState with next refresh token and next access token.
11. If RotateRefreshToken is false:
    - Update existing state AccessToken = nextAccessToken and LastUsedAt = now.
    - Save existing RefreshTokenState.
12. Publish EventRefresh.
13. Return LoginResult.
```

#### RefreshManager.Revoke

```text
1. Load refresh token.
2. If not found, return ErrRefreshTokenNotFound.
3. Mark RevokedAt = now and save with remaining TTL, or delete physically.
4. Publish EventRefreshRevoked.
```

Physical deletion is acceptable for memory/redis. Database may prefer soft revoke to preserve audit data. Public behavior must treat both as revoked/not usable.

#### NonceManager.Generate

```text
1. Generate random string with configured length.
2. Build NonceState with optional subject, purpose, metadata.
3. Set ExpiresAt when Timeout > 0.
4. Save nonce state with TTL.
5. Return nonce string.
```

#### NonceManager.Consume

```text
1. Normalize nonce.
2. Return ErrEmptyNonce for empty input.
3. Call NonceStore.ConsumeNonceState atomically.
4. If not found, return ErrNonceNotFound.
5. If consumed, return ErrNonceReplayed.
6. If expired, return ErrNonceExpired.
7. Return consumed NonceState.
```

#### Manager.Login with Required Nonce

```text
1. Apply login options.
2. If Config.RequireNonce is false, continue existing login behavior.
3. If Config.RequireNonce is true and login option nonce is empty, return ErrEmptyNonce.
4. If Config.RequireNonce is true and no NonceConsumer is configured, return ErrNonceConsumerNotConfigured.
5. Consume nonce.
6. Continue existing login behavior.
```

### 5.2 Validation Rules

- `loginID` is trimmed and must not be empty.
- `TokenValue` inputs are trimmed and must not be empty.
- `RefreshTokenState.LoginType` defaults to `core.DefaultLoginType`.
- `NonceConfig.Length <= 0` falls back to default length.
- `RefreshConfig.Timeout == 0` falls back to default refresh timeout.
- `RefreshConfig.Timeout < 0` means refresh token never expires.
- `NonceConfig.Timeout <= 0` is invalid for generated nonce and must fall back to default timeout; nonce should not be permanent by default.
- `device` is trimmed; empty device means no device filter.
- RBAC role and permission values are trimmed; empty values are ignored by mutators and denied by checks.

### 5.3 State Machine

#### RefreshTokenState

```text
issued
  -> used        on successful refresh without rotation
  -> rotated     on successful refresh with rotation
  -> revoked     on Revoke or RevokeByLoginID
  -> expired     when now >= ExpiresAt

rotated, revoked, expired are terminal for the old refresh token.
```

Valid transitions:

| From | Event | To | Side Effects |
| --- | --- | --- | --- |
| issued | refresh without rotation | used | Update LastUsedAt and AccessToken |
| issued | refresh with rotation | rotated | Delete old token, save next token |
| used | refresh without rotation | used | Update LastUsedAt and AccessToken |
| issued/used | revoke | revoked | Save RevokedAt or delete token |
| issued/used | expiry check | expired | Delete token or deny |

#### NonceState

```text
issued
  -> consumed    on successful Consume
  -> expired     when now >= ExpiresAt

consumed and expired are terminal.
```

### 5.4 Edge Cases

- Refresh state save fails after access login succeeds: roll back access login.
- Refresh rotates successfully but event listener fails: auth flow remains successful.
- Old access token is already missing during refresh: continue when refresh token is valid.
- Concurrent refresh with the same rotating refresh token: only one request may succeed.
- Concurrent nonce consume: only one request may succeed.
- Store returns expired token from an older implementation: managers must check `ExpiresAt` defensively.
- LogoutByDevice with no matching device: return nil and publish no event.
- `ListTokenStates` for empty loginID: return `ErrEmptyLoginID`.
- AsyncEventBus queue full: behavior depends on configured publish mode; default should block until enqueue or context cancellation.

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error Code | HTTP Status | Condition | User Message |
| --- | --- | --- | --- |
| `ErrEmptyLoginID` | 400 | loginID empty | empty login id |
| `ErrEmptyToken` | 400 | access token empty | empty token |
| `ErrTokenNotFound` | 401 | access token missing/expired | token not found |
| `ErrEmptyRefreshToken` | 400 | refresh token empty | empty refresh token |
| `ErrRefreshTokenNotFound` | 401 | refresh token not found | refresh token not found |
| `ErrRefreshTokenExpired` | 401 | refresh token expired | refresh token expired |
| `ErrRefreshTokenRevoked` | 401 | refresh token revoked | refresh token revoked |
| `ErrNextRefreshTokenRequired` | 400 | rotation enabled but next token missing | next refresh token required |
| `ErrEmptyNonce` | 400 | nonce missing | empty nonce |
| `ErrNonceNotFound` | 400 | nonce not found | nonce not found |
| `ErrNonceExpired` | 400 | nonce expired | nonce expired |
| `ErrNonceReplayed` | 409 | nonce already consumed | nonce replayed |
| `ErrNonceConsumerNotConfigured` | 500 | RequireNonce without consumer | nonce consumer not configured |
| `ErrAuthorityDenied` | 403 | RBAC denied | authority denied |

### 6.2 Retry Strategy

- Refresh retry with the same refresh token is not safe when rotation is enabled; clients must use the latest returned refresh token.
- Refresh retry with rotation disabled is idempotent only if the first attempt failed before saving a new access token.
- Nonce consume must not be retried with the same nonce after a timeout unless the application can verify the first attempt never reached the server.
- AsyncEventBus listener execution is not retried by default.
- Store transient errors are returned to caller; the library does not implement automatic retry.

### 6.3 Failure Modes

| Failure | Behavior |
| --- | --- |
| RefreshStore unavailable | Refresh APIs return store error; existing access token flow remains unaffected |
| NonceStore unavailable | Nonce APIs return store error; login requiring nonce fails closed |
| Event listener panic | Sync and async event bus recover; main auth flow remains unaffected |
| Async queue full | Default mode waits for enqueue or context cancellation |
| Database migration not run | Store returns database error; README must instruct running `Migrate(ctx)` |
| Redis script unavailable | Redis store returns command error; nonce consume fails closed |

---

## 7. Security

### 7.1 Authentication & Authorization

- Access token authentication remains based on `core.Manager.GetTokenState`.
- Refresh token must never authenticate normal protected routes.
- Refresh token may only be used with RefreshManager.
- Refresh token exchange must validate server-side `RefreshTokenState`, not only token string format.
- RBAC helper is optional and must be explicitly passed via `core.WithAuthorizer`.

### 7.2 Input Validation

- All token strings and IDs are trimmed.
- Empty access token, refresh token, nonce, and loginID return explicit errors.
- Metadata should be copied with existing `cloneRawMessage` semantics.
- JSON metadata should not be interpreted by core.

### 7.3 Data Protection

- Refresh tokens and nonce values are secrets and must not be logged by library code.
- Examples must avoid printing full refresh token values in logs.
- Database stores keep refresh token strings as primary keys in phase 2; applications that need hashed refresh token storage can wrap token generation or implement a custom store.
- Event metadata must not include raw refresh tokens by default.

---

## 8. Performance

### 8.1 Expected Load

Target assumptions for phase 2:

- Refresh requests are lower frequency than normal authenticated requests.
- Nonce generation/consume may be used on login and other sensitive operations.
- Online token listing is admin/user-management traffic, not hot-path middleware traffic.
- AsyncEventBus should avoid blocking the login/logout path except when its queue is full and default blocking mode is selected.

### 8.2 Optimization Strategy

- Reuse existing subject indexes for access token online listing.
- Add separate refresh token subject indexes to avoid scanning all refresh tokens.
- Redis uses sets for subject-to-token indexes and read-time cleanup for stale members.
- Database uses `(login_id, login_type)` indexes for refresh lookup and `expires_at` indexes for cleanup.
- Nonce consume is single-key and should be O(1).
- AsyncEventBus worker count and queue size are configurable.

### 8.3 Database Considerations

- Add index `idx_refresh_login(login_id, login_type)`.
- Add index on `refresh_token_states.access_token` for old access token cleanup or audit.
- Add index on `refresh_token_states.expires_at`.
- Add index on `refresh_token_states.revoked_at`.
- Add index on `nonce_states.expires_at`.
- Add index on `nonce_states.consumed_at`.
- Cleanup of expired refresh tokens and nonce rows may be lazy in phase 2; background cleanup is out of scope unless implemented by application code.

---

## 9. Testing Strategy

### 9.1 Unit Tests

- `core.RefreshTokenState` clone, expiry, revoke, subject normalization.
- `core.NonceState` clone, expiry, consume.
- `core.RefreshManager.Login` success and rollback on refresh save failure.
- `core.RefreshManager.Refresh` success with rotation enabled.
- `core.RefreshManager.Refresh` success with rotation disabled.
- `core.RefreshManager.Refresh` rejects expired, revoked, missing, and empty tokens.
- `core.NonceManager.Generate` uses configured TTL and length.
- `core.NonceManager.Consume` rejects empty, missing, expired, and replayed nonce.
- `core.Manager.Login` with `RequireNonce` validates nonce behavior.
- `core.Manager.ListTokenStates` returns only valid states.
- `core.Manager.LogoutByDevice` removes only matching device token states.
- `core.AsyncEventBus` dispatches events asynchronously and reports listener errors.
- `rbac.Authorizer` role/permission matching and wildcard permission matching.

### 9.2 Integration Tests

- `storage/memory` implements `StoreWithRefresh` and `StoreWithNonce`.
- `storage/redis` refresh token save/get/delete/list with miniredis.
- `storage/redis` nonce consume is single-use under concurrent goroutines.
- `storage/database` migration creates refresh and nonce tables.
- `storage/database` refresh token save/get/delete/list with sqlite.
- `storage/database` nonce consume is single-use under concurrent goroutines.
- RefreshManager works with memory, redis, and database stores.
- Existing `plugins/http` and `plugins/gin` continue to authenticate refreshed access tokens.

### 9.3 Edge Case Tests

- Refresh token save failure rolls back access token.
- Refresh rotation rejects missing next refresh token.
- Two concurrent refresh calls with the same rotating refresh token: exactly one succeeds.
- Two concurrent nonce consumes for the same nonce: exactly one succeeds.
- LogoutByDevice with empty device returns a validation error or no-op according to final implementation; behavior must be tested and documented.
- AsyncEventBus listener panic does not panic caller.
- AsyncEventBus `Close` drains or stops according to documented behavior.
- Existing first-stage tests continue passing unchanged.

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type | Description |
| --- | --- | --- | --- |
| US-001, FR-2, FR-3 | `TestRefreshStoreCapabilityDoesNotChangeCoreStore` | unit | Verify `core.Store` signature unchanged and optional interface exists |
| US-002, FR-4 | `TestRefreshManagerLoginIssuesRefreshState` | unit | Login with refresh creates access and refresh states |
| US-003, FR-7, FR-8, FR-9 | `TestRefreshManagerRefreshValidatesState` | unit | Refresh accepts valid token and rejects expired/revoked token |
| US-004, FR-5, FR-6 | `TestRefreshManagerRevoke` | unit | Revoke one and revoke by loginID deny future refresh |
| US-005, FR-10, FR-11, FR-12 | `TestNonceManagerGenerateConsume` | unit | Nonce can be generated and consumed once |
| US-006 | `TestManagerLoginRequiresNonce` | unit | Login fails closed when nonce missing or replayed |
| US-007, FR-13, FR-14 | `TestManagerListTokenStates` | unit | Online listing filters expired tokens |
| US-008, FR-15 | `TestManagerLogoutByDevice` | unit | Device logout removes matching device only |
| US-009, FR-16, FR-17 | `TestSessionSharedAcrossManagers` | integration | Multiple managers share Session through same store |
| US-010, FR-18, FR-19 | `TestAsyncEventBus` | unit | Async dispatch, error handling, panic recovery |
| US-011, FR-20, FR-21 | `TestRBACAuthorizer` | unit | RBAC helper satisfies Authorizer |
| US-012, FR-22, FR-23 | `TestExamplesCompile` | integration | Examples compile or run with documented command |
| FR-24 | `go test ./...` | integration | All stores covered by refresh/nonce tests |

---

## 10. Implementation Plan

### 10.1 Phases

#### Phase 1: Core Extension Contracts

- Add refresh token model, errors, events, and store capability interfaces.
- Add nonce model, errors, and store capability interfaces.
- Add tests for pure core models.

#### Phase 2: Store Capability Implementations

- Extend memory store.
- Extend Redis store with refresh keys and atomic nonce consume.
- Extend database store with migration records and transaction-based nonce consume.
- Add shared contract-style tests for refresh and nonce stores where practical.

#### Phase 3: Refresh and Nonce Managers

- Implement `RefreshManager`.
- Implement `NonceManager`.
- Add optional Manager nonce integration.
- Add refresh/nonce examples.

#### Phase 4: Online and Distributed Session

- Add `Manager.ListTokenStates`.
- Add `Manager.LogoutByDevice`.
- Add cross-manager Session tests for memory, Redis, and database.
- Add online-manager example.

#### Phase 5: Async Events and RBAC

- Implement `AsyncEventBus`.
- Add refresh/nonce event types.
- Add `rbac` package.
- Add docs for event and RBAC usage.

#### Phase 6: Documentation Closeout

- Update README second-stage feature index.
- Add examples to README.
- Run `go test ./...`.
- Review public API for compatibility.

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
| --- | --- | --- | --- |
| #1 Add refresh core contracts | 2.2, 3.2, 4.1, 6.1 | high | — |
| #2 Add nonce core contracts | 2.2, 3.2, 4.1, 6.1 | high | — |
| #3 Implement memory refresh/nonce store | 3.1, 9.2 | high | #1, #2 |
| #4 Implement Redis refresh/nonce store | 3.1, 8.2, 9.2 | high | #1, #2 |
| #5 Implement database refresh/nonce store | 3.1, 3.4, 8.3, 9.2 | high | #1, #2 |
| #6 Implement RefreshManager | 2.3, 4.1, 5.1 | high | #1, #3 |
| #7 Implement NonceManager and login nonce integration | 2.3, 4.1, 5.1 | high | #2, #3 |
| #8 Add OnlineManager methods and session sharing tests | 2.2, 4.1, 5.1, 9.2 | medium | — |
| #9 Add AsyncEventBus | 2.2, 4.1, 6.3, 9.1 | medium | — |
| #10 Add RBAC helper package | 2.2, 4.1, 7.1, 9.1 | medium | — |
| #11 Add README and examples | 4.2, 9.4, 10.3 | high | #6, #7, #8 |

### 10.3 Incremental Delivery

- First merge contracts and memory implementation to unblock manager tests.
- Redis/database implementations can follow behind memory as long as capability tests define the same behavior.
- README should mark second-stage APIs as available only after their implementation lands.
- No feature flag is needed for default behavior because all second-stage features are opt-in.
- For release notes, call out that `core.Config.RequireNonce` defaults to false and RefreshToken is not enabled unless `RefreshManager` is used.

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- Should database refresh token storage keep revoked tokens for audit by default, or physically delete them to match memory/Redis?
- Should `LogoutByDevice` return an error for empty device, or treat it as a no-op to avoid accidental full logout?
- Should RBAC helper support direct user permissions and role permissions in phase 2, or role permissions only?

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Refresh rotation races | Same refresh token might produce multiple access tokens | Store-level delete/save sequence must make old refresh token unusable; add concurrent tests |
| Redis nonce atomicity differs from database | Replay protection behavior inconsistent across stores | Define `ConsumeNonceState` atomic contract and test concurrency per store |
| Expanding Manager options makes core feel too broad | Core public surface becomes harder to understand | Keep advanced flows in `RefreshManager` and `NonceManager`; add only minimal Manager hooks |
| AsyncEventBus shutdown semantics are ambiguous | Tests and services may lose events during shutdown | Document `Flush` and `Close` behavior; test both |
| Database migration changes surprise existing users | Users may not run `Migrate(ctx)` after upgrade | README and examples must explicitly mention migration requirement |
| RBAC helper overlaps with existing `MemoryAuthorizer` | Duplicate concepts confuse users | Document `MemoryAuthorizer` as direct authority mapping and `rbac.Authorizer` as role-permission graph |

### 11.3 Assumptions

- Existing first-stage public API must remain source-compatible.
- `core.Manager` must continue to avoid token generation and JWT parsing.
- Application code is responsible for generating access and refresh token strings.
- RefreshToken is optional and only active when users instantiate `RefreshManager`.
- Nonce login enforcement is optional and disabled by default.
- `storage/memory`, `storage/redis`, and `storage/database` are all expected to support phase 2 refresh/nonce capabilities.
- OAuth2, SSO, and pure stateless JWT remain out of scope for phase 2.
