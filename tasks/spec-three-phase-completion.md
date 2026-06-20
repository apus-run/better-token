# SPEC: better-token 三阶段补齐技术规格

> 技术规格，源自：`tasks/prd-three-phase-completion.md`
> 生成时间：2026-06-20 | 目标分支：main | 起始 commit：9e2246d
> 语言：Go 1.24+

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 描述如何完成 PRD 的三阶段补齐，核心是**采用技术文档第 8、9 节的统一 `TokenState` 模型**：

- **统一状态模型**：`TokenState` 用 `TokenKind` + `TokenStatus` + 可选 `RefreshInfo`/`NonceInfo`/`OnlineInfo` 表达 access/refresh/nonce/online；移除独立的 `RefreshTokenState`/`NonceState`。
- **统一 Store**：`Store` 收敛为 TokenState + Session 方法（`kinds` 过滤 + 原子 `ConsumeTokenState`）；移除 `RefreshStore`/`NonceStore`；重写 memory/redis/database 三个后端。
- **统一 Manager**：移除 `RefreshManager`/`NonceManager`，refresh/nonce/online 收敛为 `*core.Manager` 方法；定义统一 `TokenManager` 接口（含 `Check*`）。
- **plugins 契约**：抽出 `plugins/contract.go`，http/gin/gRPC 复用；插件要求 `Kind==access`。
- **第三阶段**：新增 `plugins/grpc` 拦截器；新增 `audit/` 包（独立 `AuditEventType` + 默认 slog Sink）。
- **第二阶段补齐**：DistributedSession 语义 + redis/database 一致性测试。
- **文档与示例**：三阶段索引、gRPC/审计示例、refresh/nonce/online 示例迁移。

### 1.2 PRD Reference

- Source: `tasks/prd-three-phase-completion.md`
- User Stories: US-001 ~ US-017
- Functional Requirements: FR-1 ~ FR-26

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|---|---|---|
| 状态模型 | **完整统一 TokenState**（文档§8+§9，用户确认） | 文档核心思想；消除分裂的 state 类型 |
| Store | 统一 TokenState+Session，`kinds` 过滤 + `ConsumeTokenState`，移除 Refresh/NonceStore | 文档§9.1 + §18 原子消费 |
| 旧 `RefreshManager`/`NonceManager` | 直接移除 | 统一到 Manager（pre-1.0） |
| `TokenManager` 含 `Check*` | 纳入 | 单一门面 |
| 审计 | 独立 `AuditEventType` + 默认 slog Sink | 与运行态事件解耦、开箱即用 |
| LoginSubject/命名 | 代码不变（`loginID string`/`LogoutByLoginID`），文档对齐 | 现状为准 |
| 第三阶段框架 | 仅 gRPC | 用户缩小范围 |
| refresh/nonce 是否需独立 store 开关 | **不需要**——统一 Store 天然支持所有 kind | 简化构造，无 `WithRefresh`/`WithNonce` store 开关 |

---

## 2. Architecture

### 2.1 System Context

分层不变：`token` / `core` / `storage` / `plugins` / `rbac` `audit`。本次变更集中在：`core` 的统一状态模型 + 方法门面收敛；`storage` 三后端按统一 Store 重写；`plugins` 契约抽取与 gRPC；新增 `audit`。

### 2.2 Component Design

**`core.Manager`（重构后）**

```go
type Manager struct {
    config        Config
    store         Store
    authorizer    Authorizer
    eventBus      EventBus
    runtime       Runtime
    refreshConfig RefreshConfig   // refresh 行为默认值
    nonceConfig   NonceConfig     // nonce 行为默认值
}
```

- 删除字段 `nonceConsumer`、`refreshRevoker`；refresh/nonce 逻辑内联为 Manager 方法。
- refresh/nonce **无需独立 store 或开关**：统一 `Store` 对所有 `TokenKind` 一视同仁。
- 选项：`WithRefreshConfig(RefreshConfig)`、`WithNonceConfig(NonceConfig)`；删除 `WithNonceConsumer`。

**`plugins/contract.go`**（框架无关认证内核）

```go
package plugins

type Source string
const (SourceHeader, SourceAuthorization, SourceCookie, SourceQuery Source = ...)

type TokenLookup struct {
    TokenName        string   // 默认 core.Config.TokenName
    TokenPrefix      string   // 如 "Bearer"
    AuthorizationKey string   // 默认 "Authorization"
    Order            []Source // 默认 Header/Authorization/Cookie/Query
}

type Getters struct {
    Header func(key string) string
    Cookie func(key string) string
    Query  func(key string) string
}

func (l TokenLookup) Resolve(g Getters) (core.TokenValue, bool)

// Authenticate 完成 token -> AuthContext，并要求 Kind==access。
func Authenticate(ctx context.Context, m *core.Manager, token core.TokenValue) (*core.AuthContext, error)
```

`plugins/http` 的 `Extractor` 改为基于 `TokenLookup.Resolve`；gin 复用 http；gRPC 用 metadata 构造 `Getters`（仅 Header 维度）。

**`audit/` 包**（新增，sibling of `rbac/`）

```go
package audit
type Listener struct{ sink Sink; mapper Mapper }   // 实现 core.Listener
func New(opts ...Option) *Listener
type Sink interface { Write(ctx context.Context, e AuditEvent) error }
func NewSlogSink(logger *slog.Logger) Sink           // 默认 Sink
```

### 2.3 Module Interactions

- 认证中间件（http/gin/gRPC 统一）：`extract token → plugins.Authenticate(manager)（含 Kind==access 校验）→ core.WithAuth → next`。
- 审计旁路：`Manager.publish(core.Event) → EventBus → audit.Listener.Handle → 映射 AuditEvent → Sink.Write`。只读，不参与主流程控制流。

### 2.4 File Structure

```
core/
  token_state.go     [MODIFY: 统一 TokenState + TokenKind/TokenStatus + RefreshInfo/NonceInfo/OnlineInfo + 方法]
  store.go           [MODIFY: 统一 Store，kinds 过滤 + ConsumeTokenState]
  refresh_state.go   [DELETE: 并入 token_state.go 的 RefreshInfo]
  refresh_store.go   [DELETE]
  nonce_state.go     [DELETE: 并入 NonceInfo]
  nonce_store.go     [DELETE]
  refresh.go         [MODIFY: RefreshManager → Manager 方法（基于统一模型）]
  nonce.go           [MODIFY: NonceManager → Manager 方法]
  online.go          [MODIFY: ListTokenStates 按 kind 过滤 + MarkOnline/MarkOffline]
  manager.go         [MODIFY: 字段精简、nonce/refresh 内联、按 kind 过滤]
  option.go          [MODIFY: WithRefreshConfig/WithNonceConfig，删 WithNonceConsumer]
  token_manager.go   [NEW: TokenManager 接口 + 编译期断言]
  event.go           [MODIFY: 新增 EventOnline/EventOffline/EventNonceConsumed]
  errors.go          [MODIFY: 新增 ErrTokenInvalid/ErrUnsupportedKind，删 ErrNonceConsumerNotConfigured]
  *_test.go          [MODIFY: 适配统一模型]
storage/
  memory/store.go    [REWRITE: 统一 Store]
  redis/store.go     [REWRITE: 统一 Store，Lua/SETNX 原子消费]
  database/store.go  [REWRITE: token_states(kind,status) + UPDATE...WHERE 原子消费]
  */store_test.go    [MODIFY/ADD]
plugins/
  contract.go        [NEW]
  http/extractor.go  [MODIFY: 基于 contract]
  http/middleware.go [MODIFY: 复用 contract + Kind==access]
  grpc/{interceptor_server.go,interceptor_client.go,options.go,go.mod} [NEW: 独立 module，server+client]
audit/{audit.go,listener.go,slog_sink.go,audit_test.go} [NEW]
token/config.go      [NEW: 迁移 TokenConfig/JwtConfig（US-003，可选）]
examples/{refresh-token,nonce,online-manager}/main.go [MODIFY]
examples/{grpc,audit}/main.go [NEW]
docs/better-token-technical-implementation.md [MODIFY: 对齐代码命名]
README.md            [MODIFY: 三阶段索引]
```

---

## 3. Data Model

### 3.1 统一 TokenState（US-004，FR-4/5）

```go
type TokenValue string

type TokenKind string
const (
    TokenKindAccess  TokenKind = "access"
    TokenKindRefresh TokenKind = "refresh"
    TokenKindNonce   TokenKind = "nonce"
)

type TokenStatus string
const (
    TokenStatusActive   TokenStatus = "active"
    TokenStatusRevoked  TokenStatus = "revoked"
    TokenStatusConsumed TokenStatus = "consumed"
)

type TokenState struct {
    Token        TokenValue      `json:"token"`
    Kind         TokenKind       `json:"kind"`
    LoginID      string          `json:"login_id"`        // 保留扁平字段（代码现状）
    LoginType    string          `json:"login_type"`
    Device       string          `json:"device,omitempty"`
    CreatedAt    time.Time       `json:"created_at"`
    LastActiveAt time.Time       `json:"last_active_at"`
    ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
    Status       TokenStatus     `json:"status"`
    Metadata     json.RawMessage `json:"metadata,omitempty"`
    Refresh      *RefreshInfo    `json:"refresh,omitempty"`
    Nonce        *NonceInfo      `json:"nonce,omitempty"`
    Online       *OnlineInfo     `json:"online,omitempty"`
}

type RefreshInfo struct {
    AccessToken TokenValue `json:"access_token,omitempty"`
    RotatedFrom TokenValue `json:"rotated_from,omitempty"`
    RotatedTo   TokenValue `json:"rotated_to,omitempty"`
    LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
    RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}
type NonceInfo struct {
    Purpose    string     `json:"purpose,omitempty"`
    ConsumedAt *time.Time `json:"consumed_at,omitempty"`
}
type OnlineInfo struct {
    OnlineAt     *time.Time `json:"online_at,omitempty"`
    OfflineAt    *time.Time `json:"offline_at,omitempty"`
    ConnectionID string     `json:"connection_id,omitempty"`
    UserAgent    string     `json:"user_agent,omitempty"`
    IP           string     `json:"ip,omitempty"`
}
```

方法（文档§8.8，按代码 LoginID/LoginType 调整）：

```go
func (s TokenState) IsExpired(now time.Time) bool
func (s TokenState) IsRevoked() bool                 // Status==revoked
func (s TokenState) IsConsumed() bool                // Status==consumed
func (s TokenState) IsActive(now time.Time) bool     // !expired && !revoked && !consumed
func (s TokenState) Subject() LoginSubject
func (s *TokenState) Touch(now time.Time)
func (s *TokenState) MarkRevoked(now time.Time)      // Status=revoked；Refresh!=nil 时写 RevokedAt
func (s *TokenState) MarkConsumed(now time.Time)     // Status=consumed；Nonce!=nil 时写 ConsumedAt
func (s *TokenState) MarkOnline(now time.Time, info OnlineInfo) // OnlineAt=now、合并 info、Status=active、Touch
func (s *TokenState) MarkOffline(now time.Time)      // OfflineAt=now、Touch
func (s *TokenState) Clone() *TokenState             // 深拷贝 Metadata 与 Refresh/Nonce/Online/时间指针
```

> 兼容性：新增 `Kind`/`Status` 为非指针字段。旧序列化数据缺失这两列时，反序列化得空字符串——storage 读取后由 Manager 视为 `Kind=access`/`Status=active`（规范化），或在 database 迁移中回填默认值。

### 3.2 统一 Store（US-005，FR-6/7）

```go
type Store interface {
    SaveTokenState(ctx context.Context, state *TokenState, ttl time.Duration) error
    GetTokenState(ctx context.Context, token TokenValue) (*TokenState, bool, error)
    // ConsumeTokenState：原子地将 active 置为 consumed 并返回消费前状态；
    // 若已 consumed/revoked/不存在，返回当前状态供上层判定（found=false 表示不存在）。
    ConsumeTokenState(ctx context.Context, token TokenValue) (*TokenState, bool, error)
    DeleteTokenState(ctx context.Context, token TokenValue) error

    FindTokenStates(ctx context.Context, subject LoginSubject, kinds ...TokenKind) ([]*TokenState, error)
    DeleteTokenStates(ctx context.Context, subject LoginSubject, kinds ...TokenKind) error

    SaveSession(ctx context.Context, session *Session, ttl time.Duration) error
    GetSession(ctx context.Context, subject LoginSubject) (*Session, bool, error)
    DeleteSession(ctx context.Context, subject LoginSubject) error
}
```

删除：`RefreshStore`、`NonceStore`、`StoreWithRefresh`、`StoreWithNonce`、`NonceConsumer`。
`kinds` 为空表示不过滤（返回该 subject 全部 kind）。

### 3.3 AuditEvent（US-015，FR-22，独立类型）

```go
package audit
type AuditEventType string
const (
    AuditLogin, AuditLogout, AuditKickOut, AuditReplaced, AuditRenew,
    AuditRefreshIssued, AuditRefresh, AuditRefreshRevoked,
    AuditOnline, AuditOffline, AuditNonceConsumed, AuditUnknown AuditEventType = ...
)
type AuditEvent struct {
    Type      AuditEventType `json:"type"`
    LoginID   string         `json:"login_id,omitempty"`
    LoginType string         `json:"login_type,omitempty"`
    Token     core.TokenValue`json:"token,omitempty"`
    Device    string         `json:"device,omitempty"`
    IP        string         `json:"ip,omitempty"`     // 取自 Event.Metadata["ip"]
    Time      time.Time      `json:"time"`
    Result    string         `json:"result"`           // 默认 "success"
    Detail    map[string]any `json:"detail,omitempty"` // 透传 Event.Metadata
}
```

### 3.4 Migration Plan

- `TokenState` 增 `Kind`/`Status` 及 Info 指针：memory/redis 以整体 JSON 承载，旧数据规范化为 access/active。
- database `token_states` 表新增/确认 `kind`、`status` 列（文档§22 已含）；提供 `Migrate` 兼容旧表（缺列则 `ALTER TABLE ADD COLUMN` + 回填 `'access'`/`'active'`）。
- 删除 `RefreshTokenState`/`NonceState`/`RefreshStore`/`NonceStore`/`RefreshManager`/`NonceManager` 为编译期破坏性变更；同步迁移 tests、examples、文档。
- 无独立 refresh/nonce 表：refresh/nonce 行均存于 `token_states`，以 `kind` 区分。

---

## 4. API Design

### 4.1 统一 TokenManager 接口（US-007，FR-9）

`core/token_manager.go`：

```go
type TokenManager interface {
    // access（v1，签名兼容）
    Login(ctx context.Context, loginID string, token TokenValue, opts ...LoginOption) (*TokenState, error)
    Logout(ctx context.Context, token TokenValue) error
    LogoutByLoginID(ctx context.Context, loginID string, opts ...LogoutOption) error
    LogoutByDevice(ctx context.Context, loginID, device string, opts ...LogoutOption) error
    GetTokenState(ctx context.Context, token TokenValue) (*TokenState, error)
    IsValid(ctx context.Context, token TokenValue) bool
    Renew(ctx context.Context, token TokenValue, ttl time.Duration) error
    ListTokenStates(ctx context.Context, loginID string, opts ...ListTokenOption) ([]*TokenState, error)

    // refresh（Kind=refresh 的 TokenState）
    LoginWithRefresh(ctx context.Context, loginID string, accessToken, refreshToken TokenValue, opts ...LoginOption) (*LoginResult, error)
    Refresh(ctx context.Context, refreshToken, nextAccessToken TokenValue, opts ...RefreshFlowOption) (*LoginResult, error)
    RevokeRefreshToken(ctx context.Context, refreshToken TokenValue) error
    RevokeRefreshByLoginID(ctx context.Context, loginID string, opts ...LogoutOption) error

    // nonce（Kind=nonce 的 TokenState）
    GenerateNonce(ctx context.Context, opts ...GenerateNonceOption) (TokenValue, error)
    ConsumeNonce(ctx context.Context, nonce TokenValue) (*TokenState, error)   // 返回 Kind=nonce 的 TokenState

    // online（Kind=access 的 Online 投影）
    MarkOnline(ctx context.Context, token TokenValue, info OnlineInfo) error
    MarkOffline(ctx context.Context, token TokenValue) error

    // session
    GetSession(ctx context.Context, loginID string, opts ...SessionOption) (*Session, error)
    SaveSession(ctx context.Context, session *Session) error
    DeleteSession(ctx context.Context, loginID string, opts ...SessionOption) error

    // authority（决策2纳入）
    CheckAuthority(ctx context.Context, authority Authority) error
    CheckPermission(ctx context.Context, permission string) error
    CheckRole(ctx context.Context, role string) error
    CheckAll(ctx context.Context, authorities ...Authority) error
    CheckAny(ctx context.Context, authorities ...Authority) error
}
var _ TokenManager = (*Manager)(nil)
```

`LoginResult`（统一后）：

```go
type LoginResult struct {
    TokenState   *TokenState `json:"token_state"`             // Kind=access
    RefreshState *TokenState `json:"refresh_state,omitempty"` // Kind=refresh
}
```

### 4.2 Manager 方法行为

| 方法 | 行为（统一模型） |
|---|---|
| `Login` | 建 `TokenState{Kind:access,Status:active}`；ShareToken/Concurrent 逻辑用 `FindTokenStates(subject, access)` / `DeleteTokenStates(subject, access)`；发 `EventLogin` |
| `LoginWithRefresh` | 先 `Login`，再存 `TokenState{Kind:refresh,Status:active,Refresh:{AccessToken}}`；失败回滚 access；发 `EventRefreshIssued` |
| `Refresh` | `GetTokenState(refreshToken)`→校验 `Kind==refresh` 且 active→轮换时 `ConsumeTokenState` 旧 refresh→（可选）`Logout` 旧 access→`Login(nextAccessToken)`→写新/旧 refresh 的 `RotatedFrom/RotatedTo/LastUsedAt`；发 `EventRefresh` |
| `RevokeRefreshToken` | 取 refresh 状态→`MarkRevoked`→保存；发 `EventRefreshRevoked` |
| `RevokeRefreshByLoginID` | `DeleteTokenStates(subject, refresh)`；发事件 |
| `GenerateNonce` | 建 `TokenState{Kind:nonce,Status:active,Nonce:{Purpose}}`，TTL=`nonceConfig.Timeout` |
| `ConsumeNonce` | `ConsumeTokenState(nonce)`→`!found`→`ErrNonceNotFound`；`Kind!=nonce`→`ErrUnsupportedKind`；消费前已 consumed→`ErrNonceReplayed`；expired→`ErrNonceExpired`；否则返回消费后的 `*TokenState`；发 `EventNonceConsumed` |
| `MarkOnline` | `getTokenState`（要求 access）→`state.MarkOnline(now,info)`→`SaveTokenState`（TTL=剩余寿命）；发 `EventOnline` |
| `MarkOffline` | 同上 `MarkOffline`；发 `EventOffline` |
| `Logout`/`LogoutByLoginID`/`LogoutByDevice` | 删 access 后内联撤销关联 refresh：`FindTokenStates(subject, refresh)` 过滤 `Refresh.AccessToken==token` 删除（按 `RefreshConfig.RevokeRefreshOnLogout`） |

- `Login` 的 `RequireNonce` 路径：直接调用 `m.ConsumeNonce(ctx, nonce)`（缺失→`ErrEmptyNonce`）。
- `ListTokenStates`：`FindTokenStates(subject, TokenKindAccess)` 过滤过期。

### 4.3 Manager 构造选项

```go
func WithRefreshConfig(c RefreshConfig) Option
func WithNonceConfig(c NonceConfig) Option
// 删除 WithNonceConsumer
```

无 refresh/nonce store 开关：统一 `Store` 始终支持全部 kind。`NewManager(store, opts...)` 签名不变。

### 4.4 gRPC 拦截器（US-014，FR-18~21）

`plugins/grpc`（独立 go module），server + client 双向：

```go
// server 端：校验 metadata 中的 token
func UnaryServerInterceptor(m *core.Manager, opts ...Option) grpc.UnaryServerInterceptor
func StreamServerInterceptor(m *core.Manager, opts ...Option) grpc.StreamServerInterceptor
type Option // WithMetadataKey(string)（默认 "authorization"）、WithTokenLookup(plugins.TokenLookup)

// client 端：把 token 注入 outgoing metadata
func UnaryClientInterceptor(opts ...ClientOption) grpc.UnaryClientInterceptor
func StreamClientInterceptor(opts ...ClientOption) grpc.StreamClientInterceptor
type ClientOption // WithClientMetadataKey(string)、WithTokenPrefix(string)、WithTokenSource(func(ctx) (core.TokenValue, bool))
```

- **server 流程**：`metadata.FromIncomingContext` 取 key →`plugins` 解析（去 Bearer 前缀）→`plugins.Authenticate`（含 `Kind==access`）→失败 `status.Error(codes.Unauthenticated,...)`→成功 `core.WithAuth` 注入；Stream 用包装的 `grpc.ServerStream` 替换 context。
- **client 流程**：从 `TokenSource(ctx)` 取 token（默认实现走 `core.TokenFromContext`），经 `metadata.AppendToOutgoingContext(ctx, key, prefix+token)` 注入后调用 `invoker`/`streamer`；无 token 时透传不报错（鉴权交服务端）。

### 4.5 Breaking Changes

- 移除：`RefreshTokenState`、`NonceState`、`RefreshStore`、`NonceStore`、`StoreWithRefresh`、`StoreWithNonce`、`NonceConsumer`、`RefreshManager`、`NewRefreshManager`、`NonceManager`、`NewNonceManager`、`WithNonceConsumer`。
- `Store` 接口签名变更（`FindTokenStates`/`DeleteTokenStates` 增 `kinds`，新增 `ConsumeTokenState`）。
- 迁移示例：`NewNonceManager(store).Generate(...)` → `NewManager(store).GenerateNonce(...)`；`NewRefreshManager(m,store).Login(...)` → `m.LoginWithRefresh(...)`。

---

## 5. Business Logic

### 5.1 ConsumeTokenState 原子语义（FR-7/11）

- **memory**：`mu.Lock()` 内：取 entry；不存在→`(nil,false)`；若 `Status==active` 则置 `consumed`+`Nonce.ConsumedAt`/`MarkConsumed`，保存，返回消费前快照 `(prior,true)`；否则返回当前 `(state,true)`。
- **redis**：Lua 脚本读取 JSON、判 `status=="active"`、置 `consumed` 回写，返回旧值；保证单赢家。
- **database**：`UPDATE token_states SET status='consumed', state_json=? WHERE token=? AND status='active'`；`RowsAffected==1` 表示本次消费成功，再 `SELECT` 返回；`==0` 则 `SELECT` 现状返回供上层判 replay。

### 5.2 GetTokenState 流程（文档§16，code-aligned）

```
GetTokenState(token):
  store.GetTokenState → !ok → ErrTokenNotFound
  IsRevoked()||IsConsumed() → ErrTokenInvalid
  IsExpired(now) → DeleteTokenState → ErrTokenNotFound   // 保留 v1 行为
  AutoRenew → Touch + 续 ExpiresAt + Save + sync session TTL + EventRenewTimeout
  return Clone()
```

### 5.3 MarkOnline/MarkOffline（US-010）

```
MarkOnline(token, info):
  state := getTokenState(token, autoRenew=false)        // 过期/无效 -> 对应错误
  if state.Kind != access -> ErrUnsupportedKind
  state.MarkOnline(now, info); ttl := 剩余寿命(state.ExpiresAt, now)
  store.SaveTokenState(state, ttl); publish(EventOnline)
```

### 5.4 审计映射（US-015/016）

```
Handle(ctx, ev core.Event):
  ae := AuditEvent{Type: mapType(ev.Type), LoginID: ev.LoginID, LoginType: ev.LoginType,
                   Token: ev.Token, Time: ev.Time, Result: "success", Detail: ev.Metadata}
  if ev.Metadata != nil { ae.Device,_ = ev.Metadata["device"].(string); ae.IP,_ = ev.Metadata["ip"].(string) }
  return sink.Write(ctx, ae)   // 默认 slog：logger.LogAttrs(...)，异步时 panic 由 bus recover
```

### 5.5 Edge Cases

- nonce 并发二次消费：依赖 §5.1 原子语义，仅一个调用拿到 active 快照，其余 `ErrNonceReplayed`。
- refresh 轮换 `nextRefreshToken==refreshToken`：`ErrNextRefreshTokenReuse`（保留）。
- `MarkOnline` 目标非 access：`ErrUnsupportedKind`；过期：`ErrTokenNotFound`。
- gRPC metadata 缺 key/空值：`codes.Unauthenticated`。
- plugins 取到的 token 其 `Kind!=access`（如误传 refresh）：`Authenticate` 返回未授权。

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error | 触发 | 框架映射 |
|---|---|---|
| `ErrTokenInvalid`（新增） | GetTokenState 命中 revoked/consumed | 401 / gRPC `Unauthenticated` |
| `ErrUnsupportedKind`（新增） | kind 不匹配（如对 nonce 调 MarkOnline） | 400/500 |
| `ErrTokenNotFound` | 不存在/已过期 | 401 / `Unauthenticated` |
| 既有 refresh/nonce 错误 | 语义不变，改由 Manager 从统一 state 推导 | 既有映射 |
| 删除 `ErrNonceConsumerNotConfigured` | 不再需要（无 consumer） | — |

### 6.2 Failure Modes

- 审计 Sink 写失败：`Listener.Handle` 返回 error，同步 bus 吞掉、异步 bus 交 `EventErrorHandler`；**绝不影响主流程**（FR-24）。
- gRPC manager 为 nil：直接 `Unauthenticated`。

---

## 7. Security

- http/gin/gRPC 共用 `plugins.Authenticate`，统一校验 token→state→`Kind==access`→AuthContext。
- nonce 原子消费防重放（§5.1）。
- 默认 slog Sink 会记录 token——文档提示生产可注入自定义 Sink 脱敏（截断/哈希）。

---

## 8. Performance

- online 投影通过既有 `state_json`/JSON 承载，无新增查询；`MarkOnline` 为 O(1) 读改写。
- `FindTokenStates` 的 kind 过滤：memory/redis 在取出后内存过滤，database 走 `idx_token_states_subject(subject_type,subject_id,kind)`。
- 审计建议用 `AsyncEventBus` 避免 Sink I/O 阻塞登录路径。

---

## 9. Testing Strategy

### 9.1 Unit Tests

- `core/token_state_test.go`：`Kind/Status` 方法、`MarkRevoked/MarkConsumed/MarkOnline/MarkOffline`、`Clone` 深拷贝。
- `core/token_manager_test.go`：`var _ TokenManager = (*Manager)(nil)` 断言。
- `core/refresh_test.go`/`nonce_test.go`：迁移到 `m.LoginWithRefresh`/`m.Refresh`/`m.GenerateNonce`/`m.ConsumeNonce`，等价行为 + 重放/过期/撤销。
- `core/online_test.go`：`MarkOnline/MarkOffline` + 事件 + `Kind!=access` 报错。
- `audit/audit_test.go`：类型映射、slog Sink 输出、panic 隔离。

### 9.2 Integration Tests

- `storage/{memory,redis,database}`：统一 Store 全量（按 kind 过滤、`ConsumeTokenState` 原子性、TTL、撤销）。
- DistributedSession（US-012）：双 Manager 共享 store 的 Session 读写 + TTL（redis: miniredis；database: sqlite 内存）。
- `plugins/grpc`：bufconn 验证有效/无效/缺失 token 与 AuthContext 注入。

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type |
|---|---|---|
| US-004/FR-4,5 | TokenState Kind/Status/Info 方法 | unit |
| US-005/FR-6,7 | Store kinds 过滤 + ConsumeTokenState | unit/integration |
| US-006/FR-8 | 三后端统一 Store 行为 | integration |
| US-007/FR-9 | TokenManager 编译断言 | unit |
| US-008/FR-10 | LoginWithRefresh/Refresh 等价 + 回滚 | unit |
| US-009/FR-11 | GenerateNonce/ConsumeNonce 原子 + 重放 | unit/integration |
| US-010/FR-12 | MarkOnline/MarkOffline | unit |
| US-011/FR-14 | DistributedSession 语义文档 | doc |
| US-012/FR-15 | 跨实例 Session | integration |
| US-013/FR-16,17 | redis/db TTL/撤销/nonce 原子 | integration |
| US-014/FR-18~21 | gRPC bufconn | integration |
| US-015/FR-22 | AuditEvent 映射 | unit |
| US-016/FR-23,24 | slog Sink + panic 隔离 | unit |
| US-017/FR-25,26 | examples 可构建 | build |

---

## 10. Implementation Plan

### 10.1 Phases（按依赖排序）

1. **统一数据模型（基础，阻塞全部）**：US-004 TokenState → US-005 Store 接口 → US-006 三后端重写。
2. **统一 Manager**：US-007 接口 → US-008 refresh → US-009 nonce → US-010 online；同步改 `option.go`/`event.go`/`errors.go`/tests/examples。
3. **plugins 契约**：US-001（http/gin 复用 + Kind==access）。
4. **gRPC**：US-014（依赖 1/2/3）。
5. **审计**：US-015 → US-016（依赖 2 的事件类型）。
6. **第二阶段补齐**：US-011 → US-012 → US-013（依赖 1，可与 4/5 并行）。
7. **文档收尾**：US-002、US-003、US-017。

### 10.2 Issue Mapping

| Issue 候选 | SPEC 章节 | 依赖 |
|---|---|---|
| 统一 TokenState 模型 | 3.1 | — |
| 统一 Store 接口 | 3.2, 5.1 | TokenState |
| 重写 memory/redis/database | 3.4, 5.1 | Store |
| 统一 TokenManager 接口 | 4.1 | 模型 |
| refresh 并入 Manager | 4.2, 4.5 | 接口 |
| nonce 并入 Manager | 4.2, 5.1 | 接口 |
| online MarkOnline/Offline | 3.1, 5.3 | 接口 |
| plugins/contract.go | 2.2 | — |
| gRPC 拦截器 | 4.4 | contract |
| 审计模型 + slog 监听 | 3.3, 5.4 | 事件类型 |
| DistributedSession 语义+测试 | 9.2 | Store |
| redis/db 一致性测试 | 9.2 | 后端 |
| 文档与示例整合 | 2.4 | 全部 |

### 10.3 Incremental Delivery

阶段 1+2 完成即可发布统一内核；gRPC、审计、第二阶段测试按 Issue 独立合入。统一模型是一次性破坏变更，建议集中在一个里程碑完成后再叠加增量能力。

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

全部已确认：`GetTokenState` 命中 `revoked`/`consumed` 返回 `ErrTokenInvalid`；refresh 登录入口命名 `LoginWithRefresh`；`ConsumeTokenState` 复用于 refresh 轮换；gRPC 同时提供 server + client 端拦截器。无剩余阻塞性技术问题。

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|---|---|---|
| 三后端重写引入回归 | 高 | 统一 Store 测试套件覆盖全部 kind + 原子性；逐后端对拍 |
| 旧 import 破坏 | 中（pre-1.0） | 文档迁移示例；一次性改 tests/examples |
| database 旧表无 kind/status 列 | 中 | `Migrate` 做 `ALTER TABLE` + 回填默认值 |
| redis 原子消费脚本正确性 | 中 | Lua 单元测试 + miniredis 并发用例 |
| 审计记录敏感 token | 中 | 默认 Sink 文档提示脱敏；预留自定义 Sink |

### 11.3 Assumptions

- memory/redis 以整体 JSON 序列化 `TokenState`，新增 Info 字段无需结构变更。
- database `token_states` 采用文档§22 表结构（含 `kind`/`status`/`state_json`），`Migrate` 负责兼容旧表。
- `pkg/option` 的 `option.Apply` 适用于新增的 `WithRefreshConfig`/`WithNonceConfig`。
- 统一 `Store` 的 `ConsumeTokenState` 足以支撑 nonce 与 refresh 轮换的原子需求，无需额外事务方法。
