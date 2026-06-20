# SPEC: better-token 最终架构

> Technical specification derived from: `tasks/better-token-final-architecture.md`
> Generated: 2026-06-20 | Target branch: `main` | Commit: `f19e0d1`

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 将 `better-token-final-architecture.md` 转换为最终可实施技术规格。目标是以全新架构实现一个边界清晰的服务端登录态库：`token` 只负责 token/JWT 能力，`core` 负责服务端登录态状态机，`storage/*` 负责具体持久化适配，`plugins/http` 与 `plugins/gin` 负责 Web 框架集成。

本规格不考虑旧架构迁移与兼容层，不保留 legacy API 作为设计约束。旧实现中与最终架构不一致的包、入口和目录应移除。

### 1.2 PRD Reference

- Source: `tasks/better-token-final-architecture.md`
- 输入文档类型：架构定稿，不是标准 PRD；本文按架构章节推导 User Stories、Functional Requirements 与验收项。
- User Stories covered:
  - US-001: 作为库使用者，我可以用外部生成的 token 建立服务端登录态。
  - US-002: 作为库使用者，我可以校验 token 是否仍被服务端承认。
  - US-003: 作为库使用者，我可以按 token 或 loginID 注销登录态。
  - US-004: 作为库使用者，我可以读写用户级 Session KV 数据。
  - US-005: 作为库使用者，我可以通过统一 Authorizer 校验角色、权限或扩展授权项。
  - US-006: 作为库使用者，我可以订阅登录、登出、续期、替换等事件。
  - US-007: 作为 Web 应用开发者，我可以通过 net/http 或 gin 中间件把认证上下文注入请求。
  - US-008: 作为 token/JWT 使用者，我可以继续独立使用 `token` 包，不被 core 状态机耦合。
- Functional Requirements covered:
  - FR-001: `core.Manager` 不生成 token，只接收 `TokenValue` 并保存 `TokenState`。
  - FR-002: `core.Config` 只包含登录态策略，不包含 JWT 或 token 生成配置。
  - FR-003: `core.Store` 是领域存储端口，隐藏 token 索引实现细节。
  - FR-004: `TokenState` 与 `Session` 分离。
  - FR-005: `AuthContext` 与 `TokenState` 分离。
  - FR-006: `PermissionChecker` 与 `RoleChecker` 合并为 `Authorizer`。
  - FR-007: 实现 `core`、`storage/memory`、`storage/redis`、`storage/database`、`plugins/http`、`plugins/gin`，并整理 `token` 包。
  - FR-008: 当前版本不实现 RefreshToken、Nonce、OAuth2、SSO、OnlineManager、异步 EventBus、RBAC 数据库模型、纯无状态 JWT 模式。

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
| --- | --- | --- |
| 核心包位置 | `core/` 包是唯一核心入口 | 让登录态状态机、授权、事件、上下文形成清晰边界；不再保留根包旧 Manager |
| token 生成 | 保留在 `token/` 包 | JWT 与 token style 属于客户端凭证表达，不属于服务端登录态 |
| 登录态模型 | 新增 `core.TokenState` | 替代旧 `token.TokenInfo` 混合 access/refresh/session/权限字段的模型 |
| 存储端口 | `core.Store` 保持窄接口 | Manager 不直接依赖底层 KV，不暴露 token index；过期态查询是可选能力 |
| 存储实现 | 实现 `storage/memory`、`storage/redis`、`storage/database` | 三种 Store 都是最终架构的一等适配器，行为通过同一 Store contract 约束 |
| 授权模型 | 新增 `core.Authorizer` + `Authority` | 角色、权限、scope、policy 统一为授权项，便于扩展 |
| 事件总线 | 同步 `EventBus`，错误不影响主流程 | 当前版本保持简单可靠，避免引入异步队列、持久化、重试等复杂度 |
| 时间源 | `Runtime.Now` | 统一 Login/Renew/LastActiveAt/Event.Time 的时间，方便测试 |
| Web 适配 | `plugins/http`、`plugins/gin` | core 与具体 Web 框架解耦 |
| Refresh 流程 | 不进入 core | 架构定稿明确 RefreshToken 不进入当前版本 |
| Options 处理 | 本地 nil-safe apply helper | public constructor 允许传入 nil option 时不 panic |
| Store 构造 | nil provider fail fast | Redis/database provider 为 nil 时立即 panic，避免 typed nil Store 绕过 Manager 校验 |
| JWT 算法 | 未知算法显式返回错误 | 不 fallback 到 ES256，避免安全语义不清晰 |

---

## 2. Architecture

### 2.1 System Context

目标架构：

```text
Application / Web framework
  |
  | plugins/http or plugins/gin
  | - extract token from request
  | - call core.Manager.GetTokenState
  | - write core.AuthContext into context.Context
  v
core.Manager
  |
  | Config / Store / Authorizer / EventBus / Runtime
  v
core.Store
  |
  | storage/memory, storage/redis, storage/database
  v
Persistent medium
```

架构边界：

- `core` 不 import `storage/*`、`plugins/*`、`token`。
- `storage/*` 只依赖 `core` 与各自底层客户端/provider。
- `plugins/*` 只依赖 `core` 与对应 Web 框架。
- `token` 不依赖 `core`，可独立使用。
- 根目录不再保留旧 Manager、旧 config、旧 permission/event/context 包作为 public API。

### 2.2 Component Design

#### core.Manager

职责：

- 登录态状态机：Login、GetTokenState、Logout、LogoutByLoginID、Renew、IsValid。
- Session 门面：GetSession、SaveSession、DeleteSession。
- 授权门面：CheckAuthority、CheckPermission、CheckRole、CheckAll、CheckAny。
- 事件发布：在状态变更成功后发布事件。
- 认证上下文创建：由插件调用 `NewAuthContext(state)` 完成。

不负责：

- token/JWT 生成与解析。
- HTTP/Gin token 提取。
- RBAC 数据库建模。
- refresh token、nonce、OAuth2、SSO。

#### core.Store

职责：

- 保存、读取、删除 `TokenState`。
- 按 `loginID + loginType` 列出或批量删除登录态。
- 保存、读取、删除 `Session`。
- 隐藏 Memory map、Redis Set、SQL index 等索引细节。
- 只返回有效登录态；过期登录态必须统一视为不存在。

#### core.Authorizer

职责：

- 对统一 `Authority` 做授权判断。
- 支持 role、permission，并允许后续扩展 scope、policy。
- 提供默认 `NoopAuthorizer` 与测试/小项目使用的内存实现。

#### core.EventBus

职责：

- 注册监听器。
- 同步发布事件。
- 监听器错误被吞掉或记录，不破坏主流程。
- 当前版本不提供异步、重试、持久化。

#### plugins/http and plugins/gin

职责：

- 根据配置提取 token。
- 调用 `Manager.GetTokenState`。
- 生成并注入 `AuthContext`。
- 未登录时中断请求并返回 401。

### 2.3 Module Interactions

#### Login

```text
Application
  -> token.Generator or token.JwtManager generates token string
  -> core.Manager.Login(ctx, loginID, core.TokenValue(token), opts...)
  -> core.Store.SaveTokenState
  -> core.EventBus.Publish(EventLogin)
  -> returns *core.TokenState
```

#### Auth Middleware

```text
HTTP/Gin request
  -> ExtractToken
  -> Manager.GetTokenState
  -> NewAuthContext
  -> WithAuth(request.Context(), auth)
  -> next handler
```

#### Authorization

```text
Handler
  -> core.RequireAuth(ctx)
  -> Manager.CheckPermission(ctx, "user:create")
  -> Authorizer.HasAuthority(ctx, loginID, Permission("user:create"))
```

### 2.4 File Structure

```text
better-token/
  token/                          [MODIFY: keep independent token/JWT package]
    jwt.go
    generator.go
    errors.go

  core/                           [NEW]
    config.go
    manager.go
    token_state.go
    session.go
    store.go
    context.go
    authorizer.go
    event.go
    runtime.go
    option.go
    errors.go
    manager_test.go
    store_contract_test.go
    authorizer_test.go
    context_test.go

  storage/
    memory/
      store.go                    [implements core.Store]
      store_test.go
    redis/
      store.go                    [implements core.Store]
      store_test.go
    database/
      store.go                    [implements core.Store]
      store_test.go

  plugins/
    http/
      middleware.go               [NEW]
      extractor.go                [NEW]
      options.go                  [NEW]
      middleware_test.go          [NEW]
    gin/
      middleware.go               [NEW]
      extractor.go                [NEW]
      options.go                  [NEW]
      middleware_test.go          [NEW]

  manager.go                      [REMOVE legacy root manager]
  config.go                       [REMOVE legacy root config]
  permission/                     [REMOVE legacy permission package]
  event/                          [REMOVE legacy event package]
  context/                        [REMOVE legacy context package]
```

---

## 3. Data Model

### 3.1 Schema Changes

`storage/memory` 使用进程内结构保存状态。

建议 Memory 内部结构：

```go
type Store struct {
    mu       sync.RWMutex
    tokens   map[core.TokenValue]*tokenItem
    indexes  map[indexKey]map[core.TokenValue]struct{}
    sessions map[string]*sessionItem
    now      func() time.Time
}

type indexKey struct {
    LoginID   string
    LoginType string
}
```

Redis/KV key：

```text
bt:token:{token}                        -> TokenState JSON
bt:login:tokens:{login_type}:{login_id} -> token set/list
bt:session:{login_type}:{login_id}      -> Session JSON
```

SQL schema：

```sql
CREATE TABLE token_states (
    token TEXT PRIMARY KEY,
    login_id TEXT NOT NULL,
    login_type TEXT NOT NULL,
    device TEXT,
    state_json TEXT NOT NULL,
    expires_at TIMESTAMP NULL,
    last_active_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_token_states_login
ON token_states(login_id, login_type);
```

### 3.2 Entity Definitions

#### LoginSubject

```go
type LoginSubject struct {
    LoginID   string `json:"login_id"`
    LoginType string `json:"login_type"`
}
```

Rules:

- `LoginSubject` 是 token 与 session 在服务端归属的领域主体。
- `Store` 以 `LoginSubject` 表达「查找/删除某个登录主体的状态」，不暴露索引 key。
- `LoginType` 为空时归一化为默认值 `"login"`。

#### TokenValue and TokenState

```go
type TokenValue string

type TokenState struct {
    Token        TokenValue      `json:"token"`
    LoginID      string          `json:"login_id"`
    LoginType    string          `json:"login_type"`
    Device       string          `json:"device,omitempty"`
    CreatedAt    time.Time       `json:"created_at"`
    LastActiveAt time.Time       `json:"last_active_at"`
    ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
    Metadata     json.RawMessage `json:"metadata,omitempty"`
}
```

Rules:

- `Token` 必填，保存原始 token 字符串。
- `LoginID` 必填，调用方业务主体标识。
- `LoginType` 为空时使用默认值，建议 `"login"`。
- `ExpiresAt == nil` 表示永不过期。
- `Metadata` 只保存登录态元数据，不保存用户资料、权限列表、refresh token、nonce、JWT claims。

#### Session

```go
type Session struct {
    Subject LoginSubject   `json:"subject"`
    Data    map[string]any `json:"data"`
}
```

Rules:

- `Session` 按 `LoginSubject`（`loginID` + `loginType`）分键，同一 `loginID` 的不同登录类型拥有独立 Session。
- `Session.Subject.LoginType` 为空时归一化为默认值。
- `Data == nil` 时保存前初始化为空 map。
- 删除单个 token 不默认删除 Session。
- `LogoutByLoginID` 可通过选项控制是否删除 Session，且只影响所指定登录类型对应的主体。

#### Authority

```go
type AuthorityType string

const (
    AuthorityPermission AuthorityType = "permission"
    AuthorityRole       AuthorityType = "role"
)

type Authority struct {
    Type  AuthorityType `json:"type"`
    Value string        `json:"value"`
}
```

### 3.3 Relationships

- 一个 `loginID + loginType` 可以对应多个 `TokenState`。
- `Config.Concurrent=false` 时，一个 `loginID + loginType` 只能保留一个有效 `TokenState`。
- `Config.ShareToken=true` 时，同一 `loginID + loginType` 的有效 `TokenState` 可被复用。
- `Session` 是登录主体级 KV 容器，按 `LoginSubject`（`loginID` + `loginType`）获取。
- `AuthContext` 是请求快照，由 `TokenState` 派生，不直接回写 `Store`。

### 3.4 Legacy Removal Plan

本项目按全新架构实现，不设计旧 API 迁移层。

1. 保留 `token/`，但只作为独立 token/JWT 能力包。
2. 以 `core.Manager` 作为唯一登录态入口。
3. 以 `core.Authorizer` 替代旧权限管理模型。
4. 以 `core.EventBus` 替代旧事件模型。
5. 以 `core.AuthContext` 和 context helper 替代旧 context 包。
6. 移除根包旧 `BetterTokenManager`、旧 config、旧 permission/event/context 目录和无用 storage 抽象。
7. README 与示例只展示最终架构 API。

Rollback strategy:

- 回滚以 git revert 为准；不在代码内保留双实现或兼容 facade。

---

## 4. API Design

### 4.1 Public Go APIs

| API | Signature | Description | Auth |
| --- | --- | --- | --- |
| Constructor | `NewManager(store Store, opts ...Option) *Manager` | 创建核心 Manager | none |
| Login | `Login(ctx context.Context, loginID string, token TokenValue, opts ...LoginOption) (*TokenState, error)` | 建立服务端登录态 | caller-controlled |
| Logout | `Logout(ctx context.Context, token TokenValue) error` | 删除单 token 登录态 | token |
| LogoutByLoginID | `LogoutByLoginID(ctx context.Context, loginID string, opts ...LogoutOption) error` | 按用户删除登录态 | caller-controlled |
| GetTokenState | `GetTokenState(ctx context.Context, token TokenValue) (*TokenState, error)` | 校验并返回登录态 | token |
| IsValid | `IsValid(ctx context.Context, token TokenValue) bool` | 判断 token 是否有效 | token |
| Renew | `Renew(ctx context.Context, token TokenValue, ttl time.Duration) error` | 主动续期 | token |
| GetSession | `GetSession(ctx context.Context, loginID string, opts ...SessionOption) (*Session, error)` | 获取登录主体 Session | loginID + loginType |
| SaveSession | `SaveSession(ctx context.Context, session *Session) error` | 保存登录主体 Session | session.Subject |
| DeleteSession | `DeleteSession(ctx context.Context, loginID string, opts ...SessionOption) error` | 删除登录主体 Session | loginID + loginType |
| CheckAuthority | `CheckAuthority(ctx context.Context, authority Authority) error` | 校验单个授权项 | context auth |
| CheckPermission | `CheckPermission(ctx context.Context, permission string) error` | 校验权限 | context auth |
| CheckRole | `CheckRole(ctx context.Context, role string) error` | 校验角色 | context auth |
| CheckAll | `CheckAll(ctx context.Context, authorities ...Authority) error` | 全部授权项必须满足 | context auth |
| CheckAny | `CheckAny(ctx context.Context, authorities ...Authority) error` | 任意授权项满足即可 | context auth |

### 4.2 Config Schema

```go
type Config struct {
    TokenName   string
    TokenPrefix string

    Timeout       time.Duration
    ActiveTimeout time.Duration
    AutoRenew     bool

    Concurrent bool
    ShareToken bool
}
```

Default values:

```go
Config{
    TokenName:     "token",
    TokenPrefix:   "",
    Timeout:       30 * 24 * time.Hour,
    ActiveTimeout: 0,
    AutoRenew:     false,
    Concurrent:    true,
    ShareToken:    false,
}
```

Validation:

- `Timeout <= 0` 表示 `TokenState` 不过期。
- `ActiveTimeout > 0` 才参与自动续期。
- `ShareToken=true` 优先于新建 token；但若没有有效 token，仍创建新的 `TokenState`。
- `Concurrent=false` 时 Login 成功前必须删除旧 token；删除产生的事件为 `EventReplaced`。

### 4.3 Store Contract

```go
type Store interface {
    SaveTokenState(ctx context.Context, state *TokenState, ttl time.Duration) error
    GetTokenState(ctx context.Context, token TokenValue) (*TokenState, bool, error)
    DeleteTokenState(ctx context.Context, token TokenValue) error

    FindTokenStates(ctx context.Context, subject LoginSubject) ([]*TokenState, error)
    DeleteTokenStates(ctx context.Context, subject LoginSubject) error

    SaveSession(ctx context.Context, session *Session, ttl time.Duration) error
    GetSession(ctx context.Context, subject LoginSubject) (*Session, bool, error)
    DeleteSession(ctx context.Context, subject LoginSubject) error
}
```

Rules:

- `Get*` 返回 `(nil, false, nil)` 表示不存在。
- `GetTokenState` 不得返回过期数据；过期数据必须被过滤或惰性清理，并以 `(nil, false, nil)` 表示。
- Manager 不区分「已过期」和「不存在」，两者统一映射为 `ErrTokenNotFound`。
- 返回给调用方的对象必须是 copy，避免外部修改 Store 内部状态。
- `Delete*` 对不存在 key 应幂等返回 nil。
- `FindTokenStates` 必须过滤过期数据。
- `DeleteTokenStates` 必须维护索引一致性。
- `SaveSession` 以 `session.Subject` 为键；`Get/DeleteSession` 以入参 `LoginSubject` 为键。
- 索引维护（Set/Map/SQL Index）由各 Store 实现负责，`Manager` 不感知。
- `storage/redis.NewStore(nil)` 和 `storage/database.NewStore(nil)` 必须 panic，避免 typed nil Store 进入 `core.NewManager`。
- public constructors 和 option merge helper 必须忽略 nil option。

### 4.4 HTTP Plugin Contract

```go
func Middleware(manager *core.Manager, opts ...Option) func(http.Handler) http.Handler
```

Token extraction order:

```text
1. Header[TokenName]
2. Header Authorization
3. Cookie[TokenName]
4. Query[TokenName]
```

Prefix rule:

- `TokenPrefix == ""` 时直接使用提取值。
- `TokenPrefix == "Bearer"` 时支持 `Authorization: Bearer xxx`。
- 前缀匹配应 trim 空格，大小写建议按 HTTP 常见写法兼容 `Bearer`。

Unauthenticated response:

- HTTP status: 401
- Body: 可为空或简单文本，不强制 JSON。

### 4.5 Gin Plugin Contract

```go
func Middleware(manager *core.Manager, opts ...Option) gin.HandlerFunc
```

成功认证后：

- `c.Request.Context()` 包含 `core.AuthContext`。
- `c.Set("auth", auth)` 写入同一个 `*core.AuthContext`。

失败认证后：

- `c.AbortWithStatus(401)`。

### 4.6 Removed Legacy APIs

- `core.Manager.Login` 不生成 access token 或 refresh token；调用方先生成 token，再写入服务端登录态。
- core 不提供 `Refresh(refreshToken)`。
- `token.TokenInfo` 不作为登录态核心模型。
- 旧 `permission.Manager` 不进入最终架构；授权统一由 `core.Authorizer` 处理。
- 旧 `context` 包的全局当前用户心智不进入最终架构；请求态统一使用 `core.AuthContext`。
- 根包 `BetterTokenManager` 不作为最终 public API。

最终用法：

- 使用 `token.TokenGenerator` 或 `token.JwtManager` 先生成 token，再调用 `core.Manager.Login`。
- 需要 refresh token 的使用方应在应用层或后续独立 refresh 模块中实现。

---

## 5. Business Logic

### 5.1 Core Algorithms

#### Login

```text
Login(ctx, loginID, token, opts...)
  1. ensure ctx, config, runtime, store, authorizer, eventBus are available
  2. trim and validate loginID; empty -> ErrEmptyLoginID
  3. validate token; empty -> ErrEmptyToken
  4. merge LoginOption with Config defaults
  5. now := m.now()
  6. if ShareToken=true:
       list existing states by loginID/loginType
       return first state that is not expired
  7. if Concurrent=false:
       delete existing states by loginID/loginType
       publish EventReplaced after deletion succeeds
  8. compute ExpiresAt from Timeout
  9. build TokenState
  10. store.SaveTokenState(ctx, state, ttl)
  11. ensure session exists for loginID
  12. publish EventLogin
  13. return copied state
```

#### GetTokenState

```text
GetTokenState(ctx, token)
  1. validate token; empty -> ErrEmptyToken
  2. state, ok, err := store.GetTokenState(ctx, token)
  3. !ok -> ErrTokenNotFound
  4. now := m.now()
  5. if state expired:
       store.DeleteTokenState(ctx, token)
       return ErrTokenNotFound
  6. if AutoRenew=true:
       update LastActiveAt
       if ActiveTimeout > 0, update ExpiresAt = now + ActiveTimeout
       store.SaveTokenState(ctx, state, ttl)
       publish EventRenewTimeout
  7. return copied state
```

#### Logout

```text
Logout(ctx, token)
  1. validate token
  2. read state; not found/expired -> same error as GetTokenState
  3. store.DeleteTokenState(ctx, token)
  4. publish EventLogout
```

#### LogoutByLoginID

```text
LogoutByLoginID(ctx, loginID, opts...)
  1. validate loginID
  2. resolve loginType and deleteSession option
  3. subject := LoginSubject{loginID, loginType}.Normalize()
  4. store.DeleteTokenStates(ctx, subject)
  5. if deleteSession=true, store.DeleteSession(ctx, subject)
  6. publish EventKickOut
```

#### Renew

```text
Renew(ctx, token, ttl)
  1. validate token
  2. ttl <= 0 means no expiration
  3. load current state
  4. update LastActiveAt and ExpiresAt
  5. store.SaveTokenState(ctx, state, ttl)
  6. publish EventRenewTimeout
```

#### Authorization

```text
CheckAuthority(ctx, authority)
  1. auth := RequireAuth(ctx)
  2. validate authority.Type and authority.Value
  3. ok, err := authorizer.HasAuthority(ctx, auth.LoginID, authority)
  4. err -> return err
  5. !ok -> AuthorityDeniedError{Authority: authority}
```

### 5.2 Validation Rules

- `loginID` trim 后不能为空。
- `TokenValue` trim 后不能为空。
- `Authority.Value` trim 后不能为空。
- `Authority.Type` 当前版本支持 `permission` 和 `role`；未知类型允许透传给自定义 Authorizer，但默认实现返回 false。
- `Session.ID` trim 后不能为空。
- `Metadata` 必须是合法 JSON raw message；空值允许。
- `LoginOption.Device`、`LoginOption.LoginType` trim 后使用。

### 5.3 State Machine

`TokenState` 状态：

```text
Created -> Active -> Expired
Created -> Active -> LoggedOut
Created -> Active -> Replaced
```

Transitions:

| From | Event | To | Side effects |
| --- | --- | --- | --- |
| none | Login | Active | SaveTokenState, optional SaveSession, EventLogin |
| Active | GetTokenState with expiry passed | Expired | DeleteTokenState |
| Active | Logout | LoggedOut | DeleteTokenState, EventLogout |
| Active | LogoutByLoginID | LoggedOut | DeleteTokenStates, optional DeleteSession, EventKickOut |
| Active | Login with Concurrent=false | Replaced | DeleteTokenStates, EventReplaced |
| Active | Renew or AutoRenew | Active | SaveTokenState, EventRenewTimeout |

### 5.4 Edge Cases

- `ShareToken=true` 且已有 token 已过期：必须清理或忽略过期 token，并创建新状态。
- `Concurrent=false` 且删除旧 token 成功、保存新 token 失败：旧 token 已失效；返回保存错误，事件只发布已完成的替换事件。
- `AutoRenew=true` 但保存续期状态失败：返回错误，避免调用方误以为续期成功。
- Store 返回不存在：Manager 映射为 `ErrTokenNotFound` 或 session not found。
- Event listener panic：当前版本应避免 panic 破坏主流程；可 recover 或要求 listener 自行处理。推荐 recover。
- `Metadata` copy：返回 `TokenState` 或创建 `AuthContext` 时必须深拷贝 `json.RawMessage`。
- nil context：内部按 `context.Background()` 处理。
- nil Store：构造函数应 panic 或返回不可用 Manager；推荐 `NewManager` 对 nil store panic，避免运行期隐蔽失败。
- nil option：所有 public constructor 和 Manager 方法应忽略 nil option，不因调用方动态拼接 option 而 panic。
- nil Redis/database provider：Store 构造函数必须 panic，避免 typed nil 绕过接口 nil 判断。

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error Code | Go Error | HTTP Status | Condition | User Message |
| --- | --- | --- | --- | --- |
| EMPTY_LOGIN_ID | `ErrEmptyLoginID` | 400 | loginID 为空 | empty login id |
| EMPTY_TOKEN | `ErrEmptyToken` | 401 | token 为空 | empty token |
| TOKEN_NOT_FOUND | `ErrTokenNotFound` | 401 | Store 无此 token 或 token 已过期 | token not found |
| NOT_LOGIN | `ErrNotLogin` | 401 | context 无 AuthContext | not login |
| AUTHORITY_DENIED | `ErrAuthorityDenied` | 403 | 授权不通过 | authority denied |
| UNSUPPORTED_TOKEN_STYLE | `token.ErrUnsupportedTokenStyle` | n/a | token style 不支持 | unsupported token style |
| UNSUPPORTED_JWT_ALGORITHM | `token.ErrUnsupportedJWTAlgorithm` | n/a | JWT algorithm 不支持 | unsupported jwt algorithm |
| STORE_ERROR | wrapped store error | 500 | Store 操作失败 | internal error |
| EVENT_ERROR | not returned in main flow | none | listener error | ignored or logged |

### 6.2 Retry Strategy

- `storage/memory` 操作不做 retry。
- Store 接口不内置 retry；Redis/database 由具体 Store 或调用方控制。
- EventBus 不 retry。
- HTTP/Gin middleware 不 retry。

### 6.3 Failure Modes

- Store 保存失败：Login/Renew 返回错误，不发布成功事件。
- Store 删除失败：Logout/LogoutByLoginID 返回错误，不发布成功事件。
- Authorizer 失败：授权 API 返回原始错误。
- Event listener 失败：不影响 Manager 主流程；如果实现返回错误，Manager 应忽略或记录。
- Middleware token 提取失败：返回 401，不调用下游 handler。

---

## 7. Security

### 7.1 Authentication & Authorization

- core 只承认 Store 中存在且未过期的 `TokenState`。
- JWT 是否有效不由 core 判断；如果业务需要 JWT 校验，应在生成/登录前或插件外层完成。
- `token.JwtManager` 只接受明确支持的签名算法；未知算法必须返回 `ErrUnsupportedJWTAlgorithm`，不得静默 fallback。
- `CheckPermission`、`CheckRole` 基于请求 context 中的 `AuthContext.LoginID`。
- `NoopAuthorizer` 默认拒绝所有授权检查。

### 7.2 Input Validation

- 所有外部字符串入参 trim 空白。
- HTTP token 前缀解析必须避免把 `Bearer` 本身当 token。
- Cookie/Query token 只作为兼容提取来源，Header 优先。
- `Metadata` 不参与授权判断，避免调用方误把客户端元数据作为可信权限来源。

### 7.3 Data Protection

- `TokenState.Token` 属于敏感凭证，日志与事件处理器不应明文打印。
- 默认 Event 包含 TokenValue 是架构文档要求；实现时应在文档注释中提醒监听器谨慎处理。
- Session 不保存密码、密钥、refresh token。
- Redis/database 需支持 key prefix 或表隔离策略，避免不同应用共享存储时冲突。

---

## 8. Performance

### 8.1 Expected Load

当前版本目标为库核心能力，未定义业务 QPS。实现默认应满足：

- `storage/memory` 支持并发读写，无 data race。
- `GetTokenState` 是高频路径，应使用 `RWMutex` 或等价机制减少读路径串行化。
- `ListTokenStates` 只用于 Login/LogoutByLoginID 等低频路径，可接受按用户索引扫描。

### 8.2 Optimization Strategy

- `storage/memory` 维护 `loginID + loginType -> token set` 索引，避免全量扫描。
- Get/List 返回 copy，避免共享内存导致并发修改。
- 过期清理可懒执行：Get/List 遇到过期状态时删除。
- EventBus 发布不得持有 Manager 或 Store 锁。

### 8.3 Redis and Database Considerations

- Redis 用 Set 维护 login token index。
- SQL 用 `(login_id, login_type)` 索引。
- 删除 token 时同步维护索引。
- 过期 token 可通过 TTL 或定期清理完成。
- Redis provider/database provider 为 nil 时 fail fast。
- Redis/database 适配器不向 core 泄露底层客户端、表结构或 key 结构。

---

## 9. Testing Strategy

### 9.1 Unit Tests

Core:

- `DefaultConfig` 默认值。
- `Runtime.Now` nil fallback 与 UTC 语义。
- `Login` 保存 `TokenState` 并初始化 Session。
- `Login` 的 `Concurrent=false` 替换旧 token。
- `Login` 的 `ShareToken=true` 复用有效 token。
- `GetTokenState` 返回有效状态、识别不存在、识别过期。
- `AutoRenew` 更新 `LastActiveAt` 与 `ExpiresAt`。
- `Logout` 删除指定 token。
- `LogoutByLoginID` 删除指定用户状态。
- `CheckPermission`、`CheckRole`、`CheckAll`、`CheckAny`。
- `WithAuth`、`RequireAuth`、`LoginIDFromContext`。

Storage:

- `SaveTokenState` / `GetTokenState` / `DeleteTokenState`。
- `ListTokenStates` 按 `loginID/loginType` 返回。
- `DeleteTokenStates` 清理 token 与索引。
- Session 存取删。
- copy 语义：修改返回对象不影响内部数据。
- 并发测试与 `go test -race`。
- 过期 token 通过普通 Get/List 被过滤或惰性清理，并统一表现为 not found。
- Redis/database nil provider panic。
- nil option 不 panic。

Token:

- 默认 opaque token 生成。
- JWT secret 为空时报错。
- 未知 JWT algorithm 返回 `ErrUnsupportedJWTAlgorithm`。
- token package 不 import core。

Plugins:

- Header[TokenName] 提取。
- Authorization Bearer 提取。
- Cookie 提取。
- Query 提取。
- 未认证中断请求。
- 已认证 context 可读取 `AuthContext`。

### 9.2 Integration Tests

- `token.TokenGenerator` 生成 token 后调用 `core.Manager.Login`，再通过 HTTP middleware 认证。
- `token.JwtManager` 签发 JWT 字符串，作为 `TokenValue` 保存到 core，再通过 middleware 认证。
- Gin middleware 认证后 `c.Request.Context()` 与 `c.Get("auth")` 均可读取。

### 9.3 Edge Case Tests

- 空 loginID、空 token、空 authority。
- 已过期 token 被 Get/List 过滤。
- Event listener 返回错误不影响 Login/Logout。
- listener panic 不影响主流程，如果实现选择 recover。
- `Metadata` 深拷贝。
- nil context。
- nil store 构造行为。
- nil option。
- nil Redis/database provider。

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type | Description |
| --- | --- | --- | --- |
| US-001 / FR-001 | `TestManagerLoginSavesExternalToken` | unit | 外部 token 登录后 Store 中存在 TokenState |
| US-002 | `TestManagerGetTokenStateValidatesServerState` | unit | 只有 Store 承认且未过期的 token 有效 |
| US-003 | `TestManagerLogoutAndLogoutByLoginID` | unit | 单 token 与按用户注销均生效 |
| US-004 / FR-004 | `TestManagerSessionLifecycle` | unit | Session 独立于 TokenState 存取 |
| US-005 / FR-006 | `TestManagerAuthorizerChecks` | unit | role/permission/all/any 语义正确 |
| US-006 | `TestSyncEventBusPublishesStateChanges` | unit | 登录、登出、续期事件可被订阅 |
| US-007 | `TestHTTPMiddlewareInjectsAuthContext` | integration | net/http 注入 AuthContext |
| US-007 | `TestGinMiddlewareInjectsAuthContext` | integration | gin 注入 AuthContext 和 c.Set |
| US-008 | `TestTokenPackageDoesNotImportCore` | static/unit | token 包不依赖 core |
| FR-002 | `TestCoreConfigExcludesJWTFields` | unit/static | core.Config 不包含 JWT/token style 配置 |
| FR-003 | `TestStoreContractMaintainsIndex` | unit | Store 隐藏并维护 token index |
| FR-005 | `TestAuthContextIsSnapshot` | unit | AuthContext 与 TokenState 分离且 copy metadata |
| FR-008 | `TestCoreHasNoRefreshAPI` | static | core 不暴露 RefreshToken API |

---

## 10. Implementation Plan

### 10.1 Phases

1. 建立 `core` 基础模型：errors、config、runtime、token_state、session。
2. 建立端口：store、authorizer、event、context、option。
3. 实现 `core.Manager` 状态机。
4. 实现 `storage/memory` 的 `core.Store`。
5. 实现 `storage/redis` 的 `core.Store`。
6. 实现 `storage/database` 的 `core.Store`。
7. 为 core 与三种 Store 补齐单元测试和 contract tests。
8. 整理 `token` 包边界，确保它不依赖 core。
9. 实现 `plugins/http`。
10. 实现 `plugins/gin`。
11. 移除 legacy 根包和旧目录。
12. 补齐集成测试、race 测试、vet/staticcheck。

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
| --- | --- | --- | --- |
| #1 core models and errors | 3.2, 4.2, 6.1 | high | none |
| #2 core Store/Event/Authorizer/Context contracts | 2.2, 4.3, 5.1 | high | #1 |
| #3 core.Manager implementation | 5.1, 5.2, 5.3 | high | #1, #2 |
| #4 storage/memory core.Store | 3.1, 4.3, 8.2 | high | #2 |
| #5 storage/redis core.Store | 3.1, 4.3, 8.3 | high | #2 |
| #6 storage/database core.Store | 3.1, 4.3, 8.3 | high | #2 |
| #7 core tests and store contract tests | 9.1, 9.3, 9.4 | high | #3-#6 |
| #8 token package boundary cleanup | 2.4, 4.6 | medium | #1 |
| #9 plugins/http | 4.4, 7.2, 9.2 | medium | #3, #4 |
| #10 plugins/gin | 4.5, 7.2, 9.2 | medium | #3, #4 |
| #11 remove legacy code and docs | 3.4, 4.6, 10.3 | medium | #3-#10 |

### 10.3 Incremental Delivery

- 新增并保留最终架构需要的 `core`、`token`、`storage/*`、`plugins/*`。
- 删除与最终架构冲突的旧根包、旧 storage 抽象、旧 permission/event/context 目录。
- 文档示例优先展示新用法：

```go
tokenStr, err := token.NewTokenGenerator[any]().GenerateToken(loginID, nil)
state, err := manager.Login(ctx, loginID, core.TokenValue(tokenStr))
```

- Redis/database 与 memory 使用同一 Store contract 验收。

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- `EventBus` listener panic 是否必须 recover？本 SPEC 推荐 recover，但架构文档只要求错误不破坏主流程。
- `LoginType` 默认值是否确定为 `"login"`？架构文档未指定具体默认值。
- HTTP/Gin 插件未认证响应是否需要标准 JSON 错误体？本 SPEC 暂不强制。

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| 误保留旧 API 造成使用困惑 | 文档和示例不一致 | 删除旧入口；README 只展示最终架构 |
| `TokenState` 与 `AuthContext` copy 处理遗漏 | 并发数据污染或元数据被外部修改 | 单测覆盖 copy 语义 |
| Store 索引维护不一致 | LogoutByLoginID 或 ShareToken 行为错误 | contract tests 覆盖 Save/Delete/List/DeleteTokenStates |
| EventBus 在锁内发布 | listener 回调导致死锁或长尾延迟 | Manager 实现规则：释放锁后发布事件 |
| AutoRenew 写入失败 | 调用方误判登录有效 | GetTokenState 返回错误，不吞续期失败 |
| 当前版本不提供 RefreshToken 能力 | 旧用户迁移成本 | 不提供兼容层；refresh 作为后续独立模块 |

### 11.3 Assumptions

- `tasks/better-token-final-architecture.md` 是最终实现基线，优先级高于旧代码形态。
- 本项目按全新架构实现，允许删除旧根包实现和旧目录。
- Redis/database 与 memory 都是最终架构 Store 适配器。
- 应继续使用 Go 标准 `testing`，当前仓库没有引入额外测试框架。
- 当前模块名保持 `github.com/apus-run/better-token`。
